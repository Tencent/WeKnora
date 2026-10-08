package modelcontext

import (
	"fmt"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestMCPAnswerCannotBorrowDirectoryOrHistoricalKnowledgeCitations(t *testing.T) {
	r := NewRegistry(true)
	r.RegisterContextChunk(ChunkReference{ChunkID: "faq-1", DocumentTitle: "什么是 WeKnora？"})
	r.RegisterContextChunk(ChunkReference{ChunkID: "faq-2", DocumentTitle: "如何创建知识库？"})
	history := []chat.Message{{Role: "assistant", Content: `物业工作 <kb doc="9月13日周报.docx" chunk_id="weekly-report" />`}}
	r.EncodeMessages(history)
	const article = "https://km.woa.com/articles/show/669504?jumpfrom=kmmcp"
	result := &types.ToolResult{Success: true, Output: "标题: AI 玩法\nAI摘要: Computer Use 案例\n链接: " + article}
	modelOutput := r.ModelToolResultForTool("call_mcp_tool", result)
	require.True(t, strings.HasPrefix(modelOutput, result.Output), "external payload is preserved")
	require.Contains(t, modelOutput, `<source id="w1" url="`+article+`"/>`)
	require.Equal(t, modelOutput, r.ModelToolResultForTool("call_mcp_tool", result),
		"rebuilding context keeps stable handles")
	r.EncodeMessages(history) // Replaying history must not promote its citations.
	raw := `玩法<ref id="c3"/> 动画<ref id="c1"/> 对话<ref id="c2"/> 来源<ref id="w1"/>`
	want := `玩法 动画 对话 来源<web url="` + article + `" title="" />`
	require.Equal(t, want, r.DecodeOutputText(raw))
	decoder := r.StreamDecoder()
	var streamed strings.Builder
	for _, char := range raw {
		streamed.WriteString(decoder.Feed(string(char)))
	}
	streamed.WriteString(decoder.Flush())
	require.Equal(t, want, streamed.String(), "SSE and final text apply identical evidence checks")

	// Directory handles remain usable to retrieve the actual FAQ.
	calls := []types.LLMToolCall{{Function: types.FunctionCall{
		Name: "list_knowledge_chunks", Arguments: `{"faq_id":"c1"}`,
	}}}
	r.DecodeToolCalls(calls)
	require.JSONEq(t, `{"faq_id":"faq-1"}`, calls[0].Function.Arguments)
	r.ModelToolResultForTool("knowledge_search", &types.ToolResult{Success: true, Data: map[string]interface{}{
		"display_type": "search_results",
		"results": []map[string]interface{}{{
			"chunk_id": "faq-1", "knowledge_title": "什么是 WeKnora？", "content": "知识库管理系统",
		}},
	}})
	r.RegisterContextChunk(ChunkReference{ChunkID: "faq-1"})
	r.EncodeMessages(history)
	require.Contains(t, r.DecodeOutputText(`<ref id="c1"/>`), `chunk_id="faq-1"`,
		"fresh KB evidence is still citable in a mixed-source answer")
	require.Empty(t, r.DecodeOutputText(`<ref id="c2"/><ref id="c3"/>`))
}

func TestToolArgumentsAloneDoNotAuthorizeCitations(t *testing.T) {
	r := NewRegistry(true)
	r.EncodeMessages([]chat.Message{{Role: "assistant", ToolCalls: []chat.ToolCall{
		{Function: chat.FunctionCall{Name: "list_knowledge_chunks", Arguments: `{"faq_id":"faq-real"}`}},
		{Function: chat.FunctionCall{Name: "web_fetch", Arguments: `{"items":[{"url":"https://example.com/unread"}]}`}},
	}}})
	require.Empty(t, r.DecodeOutputText(`<ref id="c1"/><ref id="w1"/>`))
	r.ModelToolResultForTool("call_mcp_tool", &types.ToolResult{Success: false, Error: "https://example.com/error"})
	require.Empty(t, r.DecodeOutputText(`<ref id="w2"/>`))
}

func TestCurrentLegacySourceResultsRemainCitableButHistoryDoesNot(t *testing.T) {
	for _, output := range []string{
		`<chunk chunk_id="chunk-real" knowledge_title="Guide">Evidence</chunk>`,
		`Evidence <kb doc="Guide" chunk_id="chunk-real" />`,
	} {
		r := NewRegistry(true)
		r.EncodeMessages([]chat.Message{{Role: "tool", Name: "wiki_read_page", Content: output}})
		require.Empty(t, r.DecodeOutputText(`<ref id="c1"/>`))
		result := &types.ToolResult{Success: true, Output: output}
		modelOutput := r.ModelToolResultForTool("wiki_read_page", result)
		require.Contains(t, modelOutput, "c1")
		require.Equal(t, output, result.Output)
		require.Contains(t, r.DecodeOutputText(`<ref id="c1"/>`), `chunk_id="chunk-real"`)
	}
}

func TestMCPSourceCandidatesPreserveExternalLinksAndPayload(t *testing.T) {
	for _, tool := range []string{"call_mcp_tool", "mcp_external_search"} {
		t.Run(tool, func(t *testing.T) {
			r := NewRegistry(true)
			body := "[MCP result]\n" + `{"url":"https:\/\/example.com\/article?a=1\u0026b=2","chunk_id":"foreign-id"}` +
				"\n[Article](https://example.com/article?a=1&b=2)\n[Wiki](https://example.com/Function_(math))。\n" +
				"[HTML](https://example.com/page?x=1&amp;y=2)\n" +
				"https://user:pass@example.com/private javascript:alert(1) https://"
			result := &types.ToolResult{Success: true, Output: body}
			got := r.ModelToolResultForTool(tool, result)
			require.Equal(t, body, result.Output)
			require.True(t, strings.HasPrefix(got, body))
			sidecar := strings.TrimPrefix(got, body)
			require.Equal(t, 3, strings.Count(sidecar, "<source "))
			require.Contains(t, sidecar, `url="https://example.com/article?a=1&amp;b=2"`)
			require.Contains(t, sidecar, `url="https://example.com/Function_(math)"`)
			require.Contains(t, sidecar, `url="https://example.com/page?x=1&amp;y=2"`)
			require.NotContains(t, sidecar, "user:pass")
			require.Empty(t, r.ChunkHandle("foreign-id"), "external IDs never become KB references")
		})
	}
}

func TestMCPSourceCandidatesAreBoundedAndOnlyForSuccessfulExternalResults(t *testing.T) {
	var body strings.Builder
	for i := 0; i < maxMCPSourceCandidates+10; i++ {
		fmt.Fprintf(&body, "https://example.com/%d\n", i)
	}
	r := NewRegistry(true)
	got := r.ModelToolResultForTool("call_mcp_tool", &types.ToolResult{Success: true, Output: body.String()})
	require.Equal(t, maxMCPSourceCandidates, strings.Count(got, "<source "))
	for _, tc := range []struct {
		tool             string
		enabled, success bool
	}{
		{"discover_mcp_tools", true, true},
		{"read_file", true, true},
		{"call_mcp_tool", false, true},
		{"call_mcp_tool", true, false},
	} {
		r := NewRegistry(tc.enabled)
		output := r.ModelToolResultForTool(tc.tool, &types.ToolResult{
			Success: tc.success, Output: "https://example.com/page",
		})
		require.NotContains(t, output, "<external_source_candidates>")
		require.Empty(t, r.DecodeOutputText(`<ref id="w1"/>`))
	}
	r = NewRegistry(true)
	require.Equal(t, "No links", r.ModelToolResultForTool("call_mcp_tool", &types.ToolResult{
		Success: true, Output: "No links",
	}))
}
