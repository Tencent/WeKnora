package modelcontext

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
)

func TestRegistryOwnsEncodingOrderForSummarySlugs(t *testing.T) {
	const knowledgeID = "36283d3d-a495-40d2-a13f-e925fc322996"
	registry := NewRegistry(true)
	require.Equal(t, "d1", registry.RegisterDocument(knowledgeID))

	messages := registry.EncodeMessages([]chat.Message{{
		Role:    "tool",
		Content: "[[summary/" + knowledgeID + "|Summary]] document=" + knowledgeID,
	}})
	require.Contains(t, messages[0].Content, "[[res://0001|Summary]]")
	require.Contains(t, messages[0].Content, "document=d1")
	require.NotContains(t, messages[0].Content, "summary/d1")

	calls := []types.LLMToolCall{{Function: types.FunctionCall{
		Name:      "wiki_read_source_doc",
		Arguments: `{"slugs":["res://0001"],"knowledge_id":"d1"}`,
	}}}
	registry.DecodeToolCalls(calls)
	require.JSONEq(t, `{"slugs":["summary/`+knowledgeID+`"],"knowledge_id":"`+knowledgeID+`"}`, calls[0].Function.Arguments)
	require.JSONEq(t, `{"slugs":["res://0001"],"knowledge_id":"d1"}`, calls[0].ModelArguments)
	require.Equal(t, ArgumentResolutionResolved, calls[0].ArgumentResolution)
	require.Empty(t, calls[0].UnresolvedHandles)
	persisted, err := json.Marshal(calls[0])
	require.NoError(t, err)
	require.NotContains(t, string(persisted), `"res://0001"`)
	require.NotContains(t, string(persisted), `"d1"`)
	require.NotContains(t, string(persisted), ArgumentResolutionResolved)
}

func TestRegistryProtocolOwnsResourceHandleRules(t *testing.T) {
	prompt := NewRegistry(true).ProtocolPrompt()
	require.Contains(t, prompt, "Source handling protocol")
	require.Contains(t, prompt, "Resource handle protocol")
	require.Contains(t, prompt, "res://NNNN")
}

func TestRegistryAuditsUnresolvedAndPartiallyResolvedToolHandles(t *testing.T) {
	registry := NewRegistry(true)
	registry.RegisterKnowledgeBase("kb-real")

	calls := []types.LLMToolCall{{Function: types.FunctionCall{
		Name:      "knowledge_search",
		Arguments: `{"knowledge_base_ids":["b1","b99"],"query":"b98","asset":"res://0099"}`,
	}}}
	registry.DecodeToolCalls(calls)

	require.JSONEq(t,
		`{"knowledge_base_ids":["kb-real","b99"],"query":"b98","asset":"res://0099"}`,
		calls[0].Function.Arguments,
	)
	require.Equal(t, ArgumentResolutionPartiallyResolved, calls[0].ArgumentResolution)
	require.Equal(t, []string{"b99", "res://0099"}, calls[0].UnresolvedHandles)
	// Alias-shaped free text is not an ID and must not be reported as a source
	// resolution failure.
	require.NotContains(t, calls[0].UnresolvedHandles, "b98")
}

func TestRegistryAuditsUnchangedJSONFormattingAsUnchanged(t *testing.T) {
	registry := NewRegistry(true)
	calls := []types.LLMToolCall{{Function: types.FunctionCall{Arguments: `{ "query": "hello" }`}}}
	registry.DecodeToolCalls(calls)
	require.Equal(t, ArgumentResolutionUnchanged, calls[0].ArgumentResolution)
}

func TestRegistryDecodesKnownHandlesEmbeddedInBuiltInSQL(t *testing.T) {
	registry := NewRegistry(true)
	registry.RegisterDocument("doc-real")
	registry.RegisterKnowledgeBase("kb-real")
	calls := []types.LLMToolCall{{
		Function: types.FunctionCall{
			Name:      "database_query",
			Arguments: `{"sql":"SELECT * FROM knowledges WHERE id = 'd1' AND knowledge_base_id = 'b1'"}`,
		},
	}}
	registry.DecodeToolCalls(calls)
	require.JSONEq(t,
		`{"sql":"SELECT * FROM knowledges WHERE id = 'doc-real' AND knowledge_base_id = 'kb-real'"}`,
		calls[0].Function.Arguments,
	)
	require.Equal(t, ArgumentResolutionResolved, calls[0].ArgumentResolution)
}

func TestRegistryOnlyDecodesQuotedSQLHandlesAndRejectsUnknownOnes(t *testing.T) {
	registry := NewRegistry(true)
	registry.RegisterDocument("doc-real")

	calls := []types.LLMToolCall{{
		Function: types.FunctionCall{
			Name:      "database_query",
			Arguments: `{"sql":"SELECT d1.id FROM documents d1 WHERE d1.id = 'd1' AND d1.parent_id = 'd99'"}`,
		},
	}}
	registry.DecodeToolCalls(calls)
	require.JSONEq(t,
		`{"sql":"SELECT d1.id FROM documents d1 WHERE d1.id = 'doc-real' AND d1.parent_id = 'd99'"}`,
		calls[0].Function.Arguments,
	)
	require.Equal(t, []string{"d99"}, calls[0].UnresolvedHandles)
	require.Equal(t, ArgumentResolutionPartiallyResolved, calls[0].ArgumentResolution)
}

func TestRegistryReplaysStructuredAndPrivateHandlesWithoutAliasDrift(t *testing.T) {
	registry := NewRegistry(true)
	registry.RegisterDocument("doc-real")
	registry.RegisterKnowledgeBase("kb-real")
	registry.ModelToolResultForTool("wiki_read_issue", &types.ToolResult{
		Success: true,
		Output:  `{"id":"issue-real","knowledge_base_id":"kb-real","suspected_knowledge_ids":["doc-real"]}`,
	})

	messages := []chat.Message{
		{
			Role: "assistant",
			ToolCalls: []chat.ToolCall{{Function: chat.FunctionCall{
				Name:      "database_query",
				Arguments: `{"sql":"SELECT * FROM knowledges WHERE id = 'doc-real' AND knowledge_base_id = 'kb-real'"}`,
			}}},
		},
		{
			Role:    "tool",
			Name:    "wiki_read_issue",
			Content: `{"id":"issue-real","knowledge_base_id":"kb-real","suspected_knowledge_ids":["doc-real"]}`,
		},
		{
			Role: "assistant",
			ToolCalls: []chat.ToolCall{{Function: chat.FunctionCall{
				Name:      "wiki_update_issue",
				Arguments: `{"issue_id":"issue-real","status":"resolved"}`,
			}}},
		},
	}

	first := registry.EncodeMessages(messages)
	require.JSONEq(t,
		`{"sql":"SELECT * FROM knowledges WHERE id = 'd1' AND knowledge_base_id = 'b1'"}`,
		first[0].ToolCalls[0].Function.Arguments,
	)
	require.JSONEq(t,
		`{"id":"i1","knowledge_base_id":"b1","suspected_knowledge_ids":["d1"]}`,
		first[1].Content,
	)
	require.JSONEq(t, `{"issue_id":"i1","status":"resolved"}`, first[2].ToolCalls[0].Function.Arguments)

	second := registry.EncodeMessages(first)
	require.JSONEq(t, first[0].ToolCalls[0].Function.Arguments, second[0].ToolCalls[0].Function.Arguments)
	require.JSONEq(t, first[1].Content, second[1].Content)
	require.JSONEq(t, first[2].ToolCalls[0].Function.Arguments, second[2].ToolCalls[0].Function.Arguments)
}

func TestRegistryOwnsWikiIssueHandles(t *testing.T) {
	registry := NewRegistry(true)
	modelResult := registry.ModelToolResultForTool("wiki_read_issue", &types.ToolResult{
		Success: true,
		Output:  `[{"id":"issue-uuid","knowledge_base_id":"kb-real","suspected_knowledge_ids":["doc-real"],"slug":"concept/a","status":"pending"}]`,
	})
	require.JSONEq(t, `[{"id":"i1","knowledge_base_id":"b1","suspected_knowledge_ids":["d1"],"slug":"concept/a","status":"pending"}]`, modelResult)

	calls := []types.LLMToolCall{{Function: types.FunctionCall{
		Name:      "wiki_update_issue",
		Arguments: `{"issue_id":"i1","status":"resolved"}`,
	}}}
	registry.DecodeToolCalls(calls)
	require.JSONEq(t, `{"issue_id":"issue-uuid","status":"resolved"}`, calls[0].Function.Arguments)
	require.Equal(t, ArgumentResolutionResolved, calls[0].ArgumentResolution)

	decoder := registry.StreamDecoder()
	got := decoder.Feed("updated i") + decoder.Feed("1 successfully") + decoder.Flush()
	require.Equal(t, "updated issue-uuid successfully", got)
}

func TestRegistryDecodesWikiReadIssueHandleArgument(t *testing.T) {
	registry := NewRegistry(true)
	registry.ModelToolResultForTool("wiki_read_issue", &types.ToolResult{
		Success: true,
		Output:  `[{"id":"issue-uuid","slug":"concept/a","status":"pending"}]`,
	})

	// The wiki_read_issue schema instructs the model to pass the short iN
	// handle back as issue_id; it must resolve to the durable issue ID before
	// the tool executes.
	calls := []types.LLMToolCall{{Function: types.FunctionCall{
		Name:      "wiki_read_issue",
		Arguments: `{"issue_id":"i1"}`,
	}}}
	registry.DecodeToolCalls(calls)
	require.JSONEq(t, `{"issue_id":"issue-uuid"}`, calls[0].Function.Arguments)
	require.Equal(t, ArgumentResolutionResolved, calls[0].ArgumentResolution)
	require.Empty(t, calls[0].UnresolvedHandles)

	unknown := []types.LLMToolCall{{Function: types.FunctionCall{
		Name:      "wiki_read_issue",
		Arguments: `{"issue_id":"i9"}`,
	}}}
	registry.DecodeToolCalls(unknown)
	require.Equal(t, ArgumentResolutionUnresolved, unknown[0].ArgumentResolution)
	require.Equal(t, []string{"i9"}, unknown[0].UnresolvedHandles)
}

func TestRegistryDecodesWikiReadIssueHandleArray(t *testing.T) {
	registry := NewRegistry(true)
	registry.ModelToolResultForTool("wiki_read_issue", &types.ToolResult{
		Success: true,
		Output:  `[{"id":"issue-a"},{"id":"issue-b"}]`,
	})

	calls := []types.LLMToolCall{{Function: types.FunctionCall{
		Name:      "wiki_read_issue",
		Arguments: `{"issue_ids":["i1","i2"]}`,
	}}}
	registry.DecodeToolCalls(calls)
	require.JSONEq(t, `{"issue_ids":["issue-a","issue-b"]}`, calls[0].Function.Arguments)
	require.Equal(t, ArgumentResolutionResolved, calls[0].ArgumentResolution)
}

func TestRegistryReportsUnknownWikiIssueHandle(t *testing.T) {
	registry := NewRegistry(true)
	calls := []types.LLMToolCall{{Function: types.FunctionCall{
		Name:      "wiki_update_issue",
		Arguments: `{"issue_id":"i9","status":"resolved"}`,
	}}}
	registry.DecodeToolCalls(calls)
	require.Equal(t, ArgumentResolutionUnresolved, calls[0].ArgumentResolution)
	require.Equal(t, []string{"i9"}, calls[0].UnresolvedHandles)
}

func TestRegistryDoesNotApplyBuiltInFieldPoliciesToDynamicTools(t *testing.T) {
	registry := NewRegistry(true)
	registry.RegisterDocument("doc-real")
	registry.RegisterWeb("https://example.com", "Example")
	registry.ModelToolResultForTool("wiki_read_issue", &types.ToolResult{
		Success: true,
		Output:  `{"id":"issue-real"}`,
	})
	calls := []types.LLMToolCall{{Function: types.FunctionCall{
		Name:      "dynamic_mcp_tool",
		Arguments: `{"issue_id":"i1","knowledge_id":"d1","url":"w1","sql":"SELECT 'd1'"}`,
	}}}
	registry.DecodeToolCalls(calls)
	require.JSONEq(t, `{"issue_id":"i1","knowledge_id":"d1","url":"w1","sql":"SELECT 'd1'"}`, calls[0].Function.Arguments)
	require.Equal(t, ArgumentResolutionUnchanged, calls[0].ArgumentResolution)
	require.Empty(t, calls[0].UnresolvedHandles)

	messages := registry.EncodeMessages([]chat.Message{
		{
			Role: "assistant",
			ToolCalls: []chat.ToolCall{{Function: chat.FunctionCall{
				Name:      "dynamic_mcp_tool",
				Arguments: `{"knowledge_id":"doc-real"}`,
			}}},
		},
		{
			Role:    "tool",
			Name:    "dynamic_mcp_tool",
			Content: `<knowledge_id>mcp-owned-id</knowledge_id> doc-real`,
		},
	})
	require.JSONEq(t, `{"knowledge_id":"doc-real"}`, messages[0].ToolCalls[0].Function.Arguments)
	require.Equal(t, `<knowledge_id>mcp-owned-id</knowledge_id> doc-real`, messages[1].Content)

	modelOutput := registry.ModelToolResultForTool("dynamic_mcp_tool", &types.ToolResult{
		Success: true,
		Output:  `<knowledge_id>mcp-owned-id</knowledge_id> doc-real`,
	})
	require.Equal(t, `<knowledge_id>mcp-owned-id</knowledge_id> doc-real`, modelOutput)
}

func TestRegistryCompactsKnownIDsInBuiltInValidationErrors(t *testing.T) {
	registry := NewRegistry(true)
	registry.RegisterDocument("doc-real")
	registry.RegisterKnowledgeBase("kb-real")

	got := registry.ModelToolResultForTool("wiki_write_page", &types.ToolResult{
		Success: false,
		Error:   "document doc-real belongs to knowledge base kb-real",
	})
	require.Equal(t, "Error: document d1 belongs to knowledge base b1", got)
}

func TestRegistryCompactsDatabaseQueryIDColumnsForBuiltInFollowUps(t *testing.T) {
	registry := NewRegistry(true)
	modelOutput := registry.ModelToolResultForTool("database_query", &types.ToolResult{
		Success: true,
		Output:  "knowledge_id | knowledge_base_id\ndoc-real | kb-real",
		Data: map[string]interface{}{
			"display_type": "database_query",
			"rows": []map[string]interface{}{{
				"knowledge_id":      "doc-real",
				"knowledge_base_id": "kb-real",
			}},
		},
	})
	require.Contains(t, modelOutput, "d1 | b1")

	calls := []types.LLMToolCall{{Function: types.FunctionCall{
		Name:      "wiki_read_source_doc",
		Arguments: `{"knowledge_id":"d1"}`,
	}}}
	registry.DecodeToolCalls(calls)
	require.JSONEq(t, `{"knowledge_id":"doc-real"}`, calls[0].Function.Arguments)
}

func TestRegistryDecodesCanonicalArgumentsForEveryBuiltInReferenceTool(t *testing.T) {
	registry := NewRegistry(true)
	registry.RegisterDocument("doc-real")
	registry.RegisterKnowledgeBase("kb-real")
	registry.RegisterChunk(ChunkReference{ChunkID: "chunk-real", KnowledgeID: "doc-real", KnowledgeBaseID: "kb-real"})
	registry.RegisterWeb("https://example.com/page", "Example")
	registry.EncodeMessages([]chat.Message{{Role: "user", Content: "summary/00000000-0000-0000-0000-000000000001"}})
	registry.ModelToolResultForTool("wiki_read_issue", &types.ToolResult{Success: true, Output: `{"id":"issue-real"}`})

	tests := []struct {
		name string
		tool string
		raw  string
		want string
	}{
		{"knowledge search KB", "knowledge_search", `{"queries":["d1"],"knowledge_base_ids":["b1"]}`, `{"queries":["d1"],"knowledge_base_ids":["kb-real"]}`},
		{"list document", "list_knowledge_chunks", `{"knowledge_id":"d1"}`, `{"knowledge_id":"doc-real"}`},
		{"list chunk", "list_knowledge_chunks", `{"chunk_id":"c1"}`, `{"chunk_id":"chunk-real"}`},
		{"document info", "get_document_info", `{"knowledge_ids":["d1"],"faq_ids":["c1"]}`, `{"knowledge_ids":["doc-real"],"faq_ids":["chunk-real"]}`},
		{"knowledge graph", "query_knowledge_graph", `{"knowledge_base_ids":["b1"],"query":"topic"}`, `{"knowledge_base_ids":["kb-real"],"query":"topic"}`},
		{"data analysis SQL", "data_analysis", `{"knowledge_id":"d1","sql":"SELECT * FROM 'd1'"}`, `{"knowledge_id":"doc-real","sql":"SELECT * FROM 'doc-real'"}`},
		{"data schema", "data_schema", `{"knowledge_id":"d1"}`, `{"knowledge_id":"doc-real"}`},
		{"database SQL", "database_query", `{"sql":"SELECT * FROM chunks WHERE knowledge_base_id='b1'"}`, `{"sql":"SELECT * FROM chunks WHERE knowledge_base_id='kb-real'"}`},
		{"web fetch", "web_fetch", `{"items":[{"url":"w1","prompt":"read"}]}`, `{"items":[{"url":"https://example.com/page","prompt":"read"}]}`},
		{"wiki source", "wiki_read_source_doc", `{"knowledge_id":"d1"}`, `{"knowledge_id":"doc-real"}`},
		{"wiki source refs", "wiki_write_page", `{"slug":"res://0001","source_refs":["d1"]}`, `{"slug":"summary/00000000-0000-0000-0000-000000000001","source_refs":["doc-real"]}`},
		{"wiki suspected refs", "wiki_flag_issue", `{"slug":"concept/a","suspected_knowledge_ids":["d1"]}`, `{"slug":"concept/a","suspected_knowledge_ids":["doc-real"]}`},
		{"wiki search KB", "wiki_search", `{"queries":["topic"],"knowledge_base_id":"b1"}`, `{"queries":["topic"],"knowledge_base_id":"kb-real"}`},
		{"wiki issue", "wiki_update_issue", `{"issue_id":"i1","status":"resolved"}`, `{"issue_id":"issue-real","status":"resolved"}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			calls := []types.LLMToolCall{{Function: types.FunctionCall{Name: test.tool, Arguments: test.raw}}}
			registry.DecodeToolCalls(calls)
			require.JSONEq(t, test.want, calls[0].Function.Arguments)
			require.NotEqual(t, ArgumentResolutionUnresolved, calls[0].ArgumentResolution)
		})
	}
}

func TestModelToolResultProtectsSummarySlugBeforeSourceCompaction(t *testing.T) {
	const knowledgeID = "07a20bb1-a662-47cf-9929-06fb5d5b5b5e"
	const kbID = "250368ff-f5a2-4e9e-868a-07bc9b857c44"
	registry := NewRegistry(true)
	registry.RegisterDocument(knowledgeID)
	registry.RegisterKnowledgeBase(kbID)

	got := registry.ModelToolResult(&types.ToolResult{Success: true, Output: "<knowledge_base_id>" + kbID + "</knowledge_base_id>\n" +
		"<link>[[summary/" + knowledgeID + "|Summary]]</link>\n" +
		"<knowledge_id>" + knowledgeID + "</knowledge_id>",
	})
	require.Contains(t, got, "[[res://0001|Summary]]")
	require.Contains(t, got, "<knowledge_base_id>b1</knowledge_base_id>")
	require.Contains(t, got, "<knowledge_id>d1</knowledge_id>")
	require.NotContains(t, got, "summary/d1")
}

func TestRegistryStreamDecoderRestoresSplitResourceAndCitationHandles(t *testing.T) {
	registry := NewRegistry(true)
	registry.EncodeMessages([]chat.Message{{Role: "user", Content: "resource://AbCdEfGhIjKlMnOpQrStUv"}})
	registry.RegisterChunk(ChunkReference{
		ChunkID:         "chunk-real",
		KnowledgeID:     "doc-real",
		KnowledgeBaseID: "kb-real",
		DocumentTitle:   "Doc",
	})

	decoder := registry.StreamDecoder()
	got := decoder.Feed("image res://0") +
		decoder.Feed("001 claim <ref id=\"c") +
		decoder.Feed("1\"/>") + decoder.Flush()
	require.Contains(t, got, "resource://AbCdEfGhIjKlMnOpQrStUv")
	require.Contains(t, got, `chunk_id="chunk-real"`)
}

func TestRegistryDropsUnknownResourceHandlesFromCompleteAndStreamOutput(t *testing.T) {
	registry := NewRegistry(true)
	require.Equal(t, "broken ", registry.DecodeOutputText("broken res://9999"))

	decoder := registry.StreamDecoder()
	got := decoder.Feed("broken res:/") + decoder.Feed("/99") + decoder.Feed("99 end") + decoder.Flush()
	require.Equal(t, "broken  end", got)
}

func TestHandleTableRoundTripAndIsolation(t *testing.T) {
	chunks := NewHandleTable("c", 3, 0)
	require.Equal(t, "c000", chunks.Register("chunk-a"))
	require.Equal(t, "c000", chunks.Register("chunk-a"))
	require.Equal(t, "c001", chunks.Register("chunk-b"))
	require.Equal(t, "chunk-b", mustResolve(t, chunks, "c001"))

	slugs := NewHandleTable("ref-", 0, 1)
	require.Equal(t, "ref-1", slugs.Register("summary/uuid"))
	require.Equal(t, "summary/uuid", mustResolve(t, slugs, "ref-1"))
	require.Equal(t, "", mustNotResolve(t, slugs, "c000"))
}

func TestHandleTableKnownTextRoundTrip(t *testing.T) {
	table := NewHandleTable("i", 0, 1)
	require.Equal(t, "i1", table.Register("issue-uuid"))
	require.Equal(t, "read i1", table.EncodeKnownText("read issue-uuid"))
	require.Equal(t, "read issue-uuid", table.DecodeKnownText("read i1"))
	require.Equal(t, "read issue-$1", func() string {
		literal := NewHandleTable("i", 0, 1)
		literal.Register("issue-$1")
		return literal.DecodeKnownText("read i1")
	}())
}

func mustResolve(t *testing.T, table *HandleTable, handle string) string {
	t.Helper()
	value, ok := table.Resolve(handle)
	require.True(t, ok)
	return value
}

func mustNotResolve(t *testing.T, table *HandleTable, handle string) string {
	t.Helper()
	value, ok := table.Resolve(handle)
	require.False(t, ok)
	return value
}
