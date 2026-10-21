package modelcontext

import (
	"encoding/json"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestOutputTextExpandsHandlesAnywhereWithoutTouchingProse(t *testing.T) {
	registry := NewRegistry(true)
	registry.RegisterKnowledgeBase("kb-real")
	registry.RegisterDocument("doc-real")
	registry.RegisterWeb("https://example.com/hot", "Hot list")
	registry.RegisterContextChunk(ChunkReference{ChunkID: "chunk-nav", KnowledgeID: "doc-real"})

	got := registry.DecodeOutputText(
		"维生素 B1 与 W6 型号; 入库 kb://1, 文档 doc://1, 来源 [榜单](url://1), 片段 chunk://1, 未知 kb://9.")
	require.Equal(t,
		"维生素 B1 与 W6 型号; 入库 weknora://kb/kb-real, 文档 weknora://doc/doc-real, "+
			"来源 [榜单](https://example.com/hot), 片段 weknora://doc/doc-real, 未知 .",
		got)
}

func TestOutputTextRendersCurrentEvidenceChunkAsCitation(t *testing.T) {
	registry := NewRegistry(true)
	registry.RegisterChunk(ChunkReference{
		ChunkID: "chunk-real", KnowledgeID: "doc-real", KnowledgeBaseID: "kb-real", DocumentTitle: "Doc",
	})
	require.Equal(t,
		`见 <kb doc="Doc" chunk_id="chunk-real" kb_id="kb-real" />`,
		registry.DecodeOutputText("见 chunk://1"))
}

func TestReasoningIsDecodedLikeContent(t *testing.T) {
	registry := NewRegistry(true)
	registry.RegisterWeb("https://example.com/a", "")
	response := &types.ChatResponse{Content: "url://1", ReasoningContent: "fetch url://1 first"}
	registry.DecodeResponse(response)
	require.Equal(t, "https://example.com/a", response.Content)
	require.Equal(t, "fetch https://example.com/a first", response.ReasoningContent)
}

func TestStreamDecoderExpandsHandlesSplitAtAnyByte(t *testing.T) {
	registry := NewRegistry(false)
	registry.RegisterKnowledgeBase("kb-real")
	registry.RegisterWeb("https://example.com/a", "")
	text := "undo: kb://1 then url://1; doc text ends with k"
	want := "undo: weknora://kb/kb-real then https://example.com/a; doc text ends with k"
	for split := 1; split < len(text); split++ {
		decoder := registry.StreamDecoder()
		got := decoder.Feed(text[:split]) + decoder.Feed(text[split:]) + decoder.Flush()
		require.Equal(t, want, got, "split at %d", split)
	}
	decoder := registry.StreamDecoder()
	var got string
	for _, char := range text {
		got += decoder.Feed(string(char))
	}
	require.Equal(t, want, got+decoder.Flush())

	// A stream that ends inside a scheme prefix keeps the ordinary text.
	decoder = registry.StreamDecoder()
	require.Equal(t, "see kb:/", decoder.Feed("see kb:/")+decoder.Flush())
}

func TestHandleTokenMustBeAWholeNumber(t *testing.T) {
	registry := NewRegistry(false)
	registry.RegisterWeb("https://example.com/a", "")
	// Saved web pages use web://<resource handle>; url://1a is not url://1.
	require.Equal(t, "web://1abc url://1a", registry.DecodeOutputText("web://1abc url://1a"))
	calls := []types.LLMToolCall{{Function: types.FunctionCall{
		Name: "read_file", Arguments: `{"path":"web://1abc"}`,
	}}}
	registry.DecodeToolCalls(calls)
	require.Empty(t, calls[0].UnresolvedHandles)
	require.JSONEq(t, `{"path":"web://1abc"}`, calls[0].Function.Arguments)
}

// An instruction written in one request keeps pointing at the same sources
// when a later request reads it, even though both number handles from 1.
func TestSavedInstructionRoundTripsAcrossRequests(t *testing.T) {
	writer := NewRegistry(true)
	writer.RegisterWeb("https://example.com/other", "")
	writer.RegisterWeb("https://dataxapi.com/zh/tools/hot-search/zhihu.md", "Zhihu")
	writer.RegisterKnowledgeBase("kb-other")
	writer.RegisterKnowledgeBase("kb-archive")

	modelArgs := `{"operation":"automation.create","params":{"name":"Zhihu",` +
		`"instruction":"抓取 url://2 并用 knowledge.create_manual 写入 kb://2，参数 knowledge_base_id: kb://2"}}`
	calls := []types.LLMToolCall{{Function: types.FunctionCall{Name: "run_operation", Arguments: modelArgs}}}
	writer.DecodeToolCalls(calls)
	require.Empty(t, calls[0].UnresolvedHandles)
	var stored struct {
		Params struct {
			Instruction string `json:"instruction"`
		} `json:"params"`
	}
	require.NoError(t, json.Unmarshal([]byte(calls[0].Function.Arguments), &stored))
	instruction := stored.Params.Instruction
	require.Equal(t,
		"抓取 https://dataxapi.com/zh/tools/hot-search/zhihu.md 并用 knowledge.create_manual 写入 "+
			"weknora://kb/kb-archive，参数 knowledge_base_id: weknora://kb/kb-archive",
		instruction)

	// The echo of the saved instruction reads back in the writer's own handles,
	// so copying it into an update is lossless.
	echo := writer.ModelToolResultForTool("run_operation", &types.ToolResult{
		Success: true, Output: `{"instruction":` + mustJSON(t, instruction) + `}`,
	})
	require.Contains(t, echo, "抓取 url://2 并用 knowledge.create_manual 写入 kb://2，参数 knowledge_base_id: kb://2")

	// A scheduled run is a fresh request that renders the instruction as the
	// user turn and gives it its own handle.
	runner := NewRegistry(true)
	encoded := runner.EncodeMessages([]chat.Message{{Role: "user", Content: instruction}})
	require.Contains(t, encoded[0].Content, "写入 kb://1，参数 knowledge_base_id: kb://1")
	runCalls := []types.LLMToolCall{{Function: types.FunctionCall{
		Name:      "run_operation",
		Arguments: `{"operation":"knowledge.create_manual","params":{"knowledge_base_id":"kb://1"}}`,
	}}}
	runner.DecodeToolCalls(runCalls)
	require.JSONEq(t,
		`{"operation":"knowledge.create_manual","params":{"knowledge_base_id":"kb-archive"}}`,
		runCalls[0].Function.Arguments)
}

func TestDelegatedTaskCarriesDurableReferencesToTheChild(t *testing.T) {
	parent := NewRegistry(true)
	parent.RegisterKnowledgeBase("kb-a")
	parent.RegisterDocument("doc-a")
	calls := []types.LLMToolCall{{Function: types.FunctionCall{
		Name: "spawn_agent", Arguments: `{"task":"Summarize doc://1 from kb://1"}`,
	}}}
	parent.DecodeToolCalls(calls)
	require.JSONEq(t, `{"task":"Summarize weknora://doc/doc-a from weknora://kb/kb-a"}`, calls[0].Function.Arguments)

	child := NewRegistry(true)
	child.RegisterKnowledgeBase("kb-child-bound")
	encoded := child.EncodeMessages([]chat.Message{{
		Role: "user", Content: "Summarize weknora://doc/doc-a from weknora://kb/kb-a",
	}})
	require.Equal(t, "Summarize doc://1 from kb://2", encoded[0].Content)
}

func TestUnknownHandleInFreeTextFailsClosed(t *testing.T) {
	registry := NewRegistry(true)
	calls := []types.LLMToolCall{{Function: types.FunctionCall{
		Name: "spawn_agent", Arguments: `{"task":"read kb://3"}`,
	}}}
	registry.DecodeToolCalls(calls)
	require.Equal(t, []string{"kb://3"}, calls[0].UnresolvedHandles)
	require.Equal(t, ArgumentResolutionUnresolved, calls[0].ArgumentResolution)
}

func TestDurableURIInIDFieldDecodesToBareID(t *testing.T) {
	registry := NewRegistry(true)
	calls := []types.LLMToolCall{{Function: types.FunctionCall{
		Name: "list_documents", Arguments: `{"knowledge_base_id":"weknora://kb/kb-real"}`,
	}}}
	registry.DecodeToolCalls(calls)
	require.JSONEq(t, `{"knowledge_base_id":"kb-real"}`, calls[0].Function.Arguments)
}

func TestHistoryAnswerWithDurableURIsReplaysAsHandles(t *testing.T) {
	registry := NewRegistry(true)
	encoded := registry.EncodeMessages([]chat.Message{
		{Role: "assistant", Content: "已写入 weknora://kb/kb-real", ReasoningContent: "target weknora://kb/kb-real"},
	})
	require.Equal(t, "已写入 kb://1", encoded[0].Content)
	require.Equal(t, "target kb://1", encoded[0].ReasoningContent)
	require.Equal(t, "已写入 weknora://kb/kb-real", registry.DecodeOutputText(encoded[0].Content))
}

func mustJSON(t *testing.T, value string) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	require.NoError(t, err)
	return string(encoded)
}
