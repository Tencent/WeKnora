package modelcontext

import (
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestRegistryChunkAliasIsStableAndExpandsCanonicalCitation(t *testing.T) {
	registry := newSourceRegistry()
	first := registry.RegisterChunk(ChunkReference{
		ChunkID:         "chunk-uuid-1",
		KnowledgeID:     "knowledge-uuid-1",
		KnowledgeBaseID: "kb-uuid-1",
		DocumentTitle:   "Architecture.md",
		ChunkIndex:      7,
	})
	second := registry.RegisterChunk(ChunkReference{
		ChunkID:       "chunk-uuid-1",
		KnowledgeID:   "knowledge-uuid-1",
		DocumentTitle: "Architecture.md",
	})

	require.Equal(t, "c1", first)
	require.Equal(t, first, second)
	require.Equal(t,
		`claim <kb doc="Architecture.md" chunk_id="chunk-uuid-1" kb_id="kb-uuid-1" />`,
		registry.ExpandText(`claim <ref id="c1"/>`),
	)
}

func TestRegisterDoesNotTreatModelAliasesAsNewDurableIdentities(t *testing.T) {
	r := newSourceRegistry()
	require.Equal(t, "c1", r.RegisterChunk(ChunkReference{ChunkID: "chunk-real"}))
	require.Equal(t, "d1", r.RegisterDocument("doc-real"))
	require.Equal(t, "b1", r.RegisterKnowledgeBase("kb-real"))
	require.Equal(t, "w1", r.RegisterWeb("https://example.com", "Example"))

	require.Equal(t, "c1", r.RegisterChunk(ChunkReference{ChunkID: "c1"}))
	require.Equal(t, "d1", r.RegisterDocument("d1"))
	require.Equal(t, "b1", r.RegisterKnowledgeBase("b1"))
	require.Equal(t, "w1", r.RegisterWeb("w1", ""))
	require.Empty(t, r.RegisterDocument("d99"))
	require.Empty(t, r.RegisterDocument("c1"), "a chunk handle must not be accepted as a document identity")
	require.Equal(t, 1, r.chunks.size())
	require.Equal(t, 1, r.docs.size())
	require.Equal(t, 1, r.kbs.size())
	require.Equal(t, 1, r.webs.size())
}

func TestRegistrySuppressesSourceCitationsWhenDisabled(t *testing.T) {
	registry := newSourceRegistry(false)
	registry.RegisterChunk(ChunkReference{ChunkID: "chunk-1", DocumentTitle: "Doc"})
	registry.RegisterWeb("https://example.com", "Example")

	require.Contains(t, sourceProtocolPrompt(false), "Source citations are disabled")
	require.NotContains(t, sourceProtocolPrompt(false), `Cite a knowledge chunk with exactly`)
	require.Equal(t, "knowledge  web ", registry.ExpandText(
		`knowledge <ref id="c1"/> web <ref id="w1"/>`,
	))
	require.Equal(t, "forged  ", registry.ExpandText(
		`forged <kb doc="Doc" chunk_id="raw" /> <web url="https://example.com" />`,
	))
}

func TestRegistryDecodesAliasesInNestedToolArguments(t *testing.T) {
	registry := NewRegistry(true)
	registry.RegisterDocument("knowledge-uuid-1")
	registry.RegisterChunk(ChunkReference{ChunkID: "chunk-uuid-1"})

	// Handles nested inside arrays and objects must decode as long as the key
	// belongs to the named tool's declared source contract.
	calls := []types.LLMToolCall{{
		Function: types.FunctionCall{
			Name:      "list_knowledge_chunks",
			Arguments: `{"knowledge_id":"d1","filters":{"chunk_id":"c1","faq_id":["c1"]}}`,
		},
	}}
	registry.DecodeToolCalls(calls)

	require.JSONEq(t,
		`{"knowledge_id":"knowledge-uuid-1","filters":{"chunk_id":"chunk-uuid-1","faq_id":["chunk-uuid-1"]}}`,
		calls[0].Function.Arguments,
	)
}

func TestDecodeToolCallsOnlyRewritesAliasBearingKeys(t *testing.T) {
	registry := NewRegistry(true)
	registry.RegisterKnowledgeBase("kb-uuid-1")

	// A free-text field (query) whose value coincidentally equals a handle must
	// be preserved verbatim, while declared ID-bearing keys resolve to real IDs.
	calls := []types.LLMToolCall{{
		Function: types.FunctionCall{
			Name:      "knowledge_search",
			Arguments: `{"query":"b1","content":"see b1 for details","knowledge_base_ids":["b1"]}`,
		},
	}}
	registry.DecodeToolCalls(calls)

	require.JSONEq(t,
		`{"query":"b1","content":"see b1 for details","knowledge_base_ids":["kb-uuid-1"]}`,
		calls[0].Function.Arguments,
	)
}

func TestStreamExpanderHoldsSplitReferenceAndDropsUnknown(t *testing.T) {
	registry := newSourceRegistry()
	registry.RegisterChunk(ChunkReference{ChunkID: "chunk-1", DocumentTitle: "Doc"})
	expander := newCitationStreamExpander(registry)

	require.Equal(t, "before ", expander.Feed(`before <ref id="`))
	require.Equal(t, `<kb doc="Doc" chunk_id="chunk-1" /> after`, expander.Feed(`c1"/> after`))
	require.Empty(t, expander.Flush())
	require.Equal(t, "x  y", registry.ExpandText(`x <ref id="c999"/> y`))
	require.Equal(t, "x  y", registry.ExpandText(`x <ref id="d1"/> y`))
	require.Equal(t, "x  y", registry.ExpandText(`x <ref id='c1'/> y`))
	require.Equal(t, "x ", registry.ExpandText(`x <ref id="c1"`))
	require.Equal(t, "x  y", registry.ExpandText(`x <kb doc="forged" chunk_id="forged" /> y`))
	require.Equal(t, "x ", expander.Feed(`x <we`))
	require.Equal(t, " y", expander.Feed(`b url="https://forged" /> y`))
}

func TestEncodeMessagesCompactsCanonicalCitationsFromHistory(t *testing.T) {
	registry := newSourceRegistry()
	messages := []chat.Message{{
		Role: "assistant",
		Content: `Knowledge <kb doc="A &amp; B.pdf" chunk_id="chunk-real" kb_id="kb-real" />; ` +
			`web <web url="https://example.com/a?x=1&amp;y=2" title="Example &amp; More" />`,
	}}

	encoded := registry.EncodeMessagesWithPolicies(messages, nil, nil)
	require.Equal(t, `Knowledge <ref id="c1"/>; web <ref id="w1"/>`, encoded[0].Content)
	require.NotContains(t, encoded[0].Content, "chunk-real")
	require.NotContains(t, encoded[0].Content, "https://example.com")
	require.Equal(t,
		`<kb doc="A &amp; B.pdf" chunk_id="chunk-real" kb_id="kb-real" /> <web url="https://example.com/a?x=1&amp;y=2" title="Example &amp; More" />`,
		registry.ExpandText(`<ref id="c1"/> <ref id="w1"/>`),
	)
}

func TestEncodeMessagesMigratesLegacyToolHistoryAtReadTime(t *testing.T) {
	registry := newSourceRegistry()
	messages := []chat.Message{
		{
			Role: "assistant",
			ToolCalls: []chat.ToolCall{{
				Function: chat.FunctionCall{
					Name:      "knowledge_search",
					Arguments: `{"knowledge_base_ids":["kb-real"],"knowledge_ids":["doc-real"]}`,
				},
			}},
		},
		{
			Role: "tool",
			Name: "knowledge_search",
			Content: `<chunk chunk_id="chunk-real" knowledge_id="doc-real" knowledge_base_id="kb-real" ` +
				`knowledge_title="Legacy Doc">legacy content</chunk>`,
		},
		{
			Role:    "assistant",
			Content: `Legacy answer <kb doc="Legacy Doc" chunk_id="chunk-real" kb_id="kb-real" />`,
		},
	}

	encoded := registry.EncodeMessagesWithPolicies(messages, nil, nil)
	require.JSONEq(t, `{"knowledge_base_ids":["b1"],"knowledge_ids":["d1"]}`,
		encoded[0].ToolCalls[0].Function.Arguments)
	require.Contains(t, encoded[1].Content, `chunk_id="c1"`)
	require.Contains(t, encoded[1].Content, `knowledge_id="d1"`)
	require.Contains(t, encoded[1].Content, `knowledge_base_id="b1"`)
	require.Equal(t, `Legacy answer <ref id="c1"/>`, encoded[2].Content)
	require.Equal(t,
		`<kb doc="Legacy Doc" chunk_id="chunk-real" kb_id="kb-real" />`,
		registry.ExpandText(`<ref id="c1"/>`),
	)
}

func TestEncodeMessagesDoesNotTreatLegacyPromptExampleAsARealSource(t *testing.T) {
	registry := newSourceRegistry()
	messages := []chat.Message{{
		Role:    "system",
		Content: `Old rule: cite <kb doc="..." chunk_id="..." />`,
	}}

	encoded := registry.EncodeMessagesWithPolicies(messages, nil, nil)
	require.Equal(t, messages[0].Content, encoded[0].Content)
	require.Zero(t, registry.Count())
}

func TestModelOutputGroupsChunksAndReusesAliasAcrossTools(t *testing.T) {
	registry := newSourceRegistry()
	search := &types.ToolResult{
		Success: true,
		Output:  "raw UUID output",
		Data: map[string]interface{}{
			"display_type": "search_results",
			"results": []map[string]interface{}{
				{
					"chunk_id":          "chunk-uuid-1",
					"knowledge_id":      "knowledge-uuid-1",
					"knowledge_base_id": "kb-uuid-1",
					"knowledge_title":   "Doc A",
					"chunk_index":       3,
					"content":           "full content",
				},
			},
		},
	}
	first := registry.ModelOutput(search)
	require.Contains(t, first, `<document id="d1" kb="b1" title="Doc A">`)
	require.Contains(t, first, `<chunk id="c1" index="3" view="full">`)
	require.NotContains(t, first, "chunk-uuid-1")
	require.NotContains(t, first, "knowledge-uuid-1")

	deepRead := &types.ToolResult{
		Success: true,
		Data: map[string]interface{}{
			"display_type":    "knowledge_chunks_list",
			"knowledge_id":    "knowledge-uuid-1",
			"knowledge_title": "Doc A",
			"total_chunks":    int64(1),
			"fetched_chunks":  1,
			"chunks": []map[string]interface{}{
				{"chunk_id": "chunk-uuid-1", "knowledge_id": "knowledge-uuid-1", "knowledge_base": "kb-uuid-1", "chunk_index": 3, "content": "deep content"},
			},
		},
	}
	second := registry.ModelOutput(deepRead)
	require.Contains(t, second, `<chunk id="c1"`)
	require.False(t, strings.Contains(second, "c2"))
}

func TestModelOutputWebAliasExpandsToWebCitation(t *testing.T) {
	registry := newSourceRegistry()
	output := registry.ModelOutput(&types.ToolResult{
		Success: true,
		Data: map[string]interface{}{
			"display_type": "web_search_results",
			"results": []map[string]interface{}{
				{"title": "Example", "url": "https://example.com/a", "snippet": "snippet"},
			},
		},
	})
	require.Contains(t, output, `<page id="w1" title="Example">`)
	require.Contains(t, output, `<evidence type="search_summary" verified="false" />`)
	require.NotContains(t, output, "https://example.com/a")
	require.Equal(t,
		`<web url="https://example.com/a" title="Example" />`,
		registry.ExpandText(`<ref id="w1"/>`),
	)
}

func TestModelOutputWebSearchRetainsContentOnlyEvidence(t *testing.T) {
	registry := newSourceRegistry()
	output := registry.ModelOutput(&types.ToolResult{
		Success: true,
		Data: map[string]interface{}{
			"display_type": "web_search_results",
			"results": []map[string]interface{}{
				{
					"title":   "Content-only provider",
					"url":     "https://example.com/content",
					"content": "search evidence from provider content",
				},
			},
		},
	})

	require.Contains(t, output, `<content>search evidence from provider content</content>`)
	require.Contains(t, output, `<evidence type="search_summary" verified="false" />`)
}

func TestModelOutputWebSearchLimitsProviderContent(t *testing.T) {
	registry := newSourceRegistry()
	content := strings.Repeat("provider evidence ", 1000)
	output := registry.ModelOutput(&types.ToolResult{
		Success: true,
		Data: map[string]interface{}{
			"display_type": "web_search_results",
			"results": []map[string]interface{}{{
				"title":   "Long provider result",
				"url":     "https://example.com/long",
				"content": content,
			}},
		},
	})

	require.Contains(t, output, `<content truncated="true">`)
	require.NotContains(t, output, content)
}

func TestModelOutputWebFetchPreservesPartialFailureStatus(t *testing.T) {
	registry := newSourceRegistry()
	registry.RegisterWeb("https://example.com/verified", "Verified")
	registry.RegisterWeb("https://example.com/forbidden", "Forbidden")

	output := registry.ModelOutput(&types.ToolResult{
		Success: true,
		Data: map[string]interface{}{
			"display_type": "web_fetch_results",
			"results": []map[string]interface{}{
				{
					"url":         "https://example.com/verified",
					"status":      "success",
					"raw_content": "verified page content",
				},
				{
					"url":           "https://example.com/forbidden",
					"status":        "failed",
					"retryable":     false,
					"error_code":    "http_403",
					"error_message": "access denied",
				},
			},
		},
	})

	require.Contains(t, output, `<page id="w1" status="success" view="full">`)
	require.Contains(t, output, "verified page content")
	require.Contains(t, output, `<page id="w2" status="failed" retryable="false" error_code="http_403">`)
	require.Contains(t, output, `<error>access denied</error>`)
	require.Contains(t, output, "failed URLs do not invalidate successful evidence")
	require.NotContains(t, output, "https://example.com")
}

func TestModelOutputWebFetchAllFailuresIncludeSearchFallback(t *testing.T) {
	registry := newSourceRegistry()
	output := registry.ModelOutput(&types.ToolResult{
		Success: true,
		Data: map[string]interface{}{
			"display_type": "web_fetch_results",
			"results": []map[string]interface{}{
				{
					"url":           "https://example.com/dns",
					"status":        "failed",
					"retryable":     true,
					"error_code":    "dns_failed",
					"error_message": "DNS lookup failed",
				},
			},
		},
	})

	require.Contains(t, output, `status="failed" retryable="true" error_code="dns_failed"`)
	require.Contains(t, output, "Stop expanding web searches")
	require.Contains(t, output, "page content was not verified")
}

func TestModelOutputWebFetchKeepsContentWhenSummaryFails(t *testing.T) {
	registry := newSourceRegistry()
	output := registry.ModelOutput(&types.ToolResult{
		Success: true,
		Data: map[string]interface{}{
			"display_type": "web_fetch_results",
			"results": []map[string]interface{}{
				{
					"url":                   "https://example.com/specs",
					"status":                "success",
					"summary_status":        "failed",
					"summary_error_code":    "summary_failed",
					"summary_error_message": "model unavailable",
					"raw_content":           "official specifications",
				},
			},
		},
	})

	require.Contains(t, output, `<summary_error code="summary_failed">model unavailable</summary_error>`)
	require.Contains(t, output, `<content>official specifications</content>`)
}

func TestModelOutputWebFetchLimitsRawContentAcrossPages(t *testing.T) {
	registry := newSourceRegistry()
	pageContent := strings.Repeat("verified page content ", 1000)
	output := registry.ModelOutput(&types.ToolResult{
		Success: true,
		Data: map[string]interface{}{
			"display_type": "web_fetch_results",
			"results": []map[string]interface{}{
				{"url": "https://example.com/one", "status": "success", "raw_content": pageContent},
				{"url": "https://example.com/two", "status": "success", "raw_content": pageContent},
			},
		},
	})

	require.Contains(t, output, `truncated="true"`)
	require.Less(t, len([]rune(output)), modelWebFetchTotalMaxRunes+3000)
}

func TestModelOutputDocumentInfoUsesDocumentAndFAQAliases(t *testing.T) {
	registry := newSourceRegistry()
	result := &types.ToolResult{
		Success: true,
		Output:  "raw IDs must not be used",
		Data: map[string]interface{}{
			"display_type": "document_info",
			"documents": []map[string]interface{}{
				{
					"knowledge_id": "doc-real-id",
					"title":        "Architecture",
					"description":  "System overview",
					"type":         "file",
					"chunk_count":  12,
				},
				{
					"faq_id":       "faq-chunk-real-id",
					"knowledge_id": "faq-container-real-id",
					"faq_question": "How does it work?",
					"faq_answers":  []string{"With short aliases."},
					"is_faq":       true,
				},
			},
		},
	}

	output := registry.ModelOutput(result)
	for _, raw := range []string{"doc-real-id", "faq-chunk-real-id", "faq-container-real-id"} {
		require.NotContains(t, output, raw)
	}
	require.Contains(t, output, `<document id="d1" title="Architecture"`)
	require.Contains(t, output, `<chunk id="c1" type="faq">`)
	require.Contains(t, registry.ExpandText(`Answer <ref id="c1"/>`), `chunk_id="faq-chunk-real-id"`)
}

func TestModelOutputCompactsLabeledWikiReferences(t *testing.T) {
	registry := newSourceRegistry()
	result := &types.ToolResult{
		Success: true,
		Output: `<wiki_page>
<knowledge_base_id>kb-real-id</knowledge_base_id>
<sources><source knowledge_id="doc-real-id">Source</source></sources>
</wiki_page>`,
	}

	output := registry.ModelOutput(result)
	require.NotContains(t, output, "kb-real-id")
	require.NotContains(t, output, "doc-real-id")
	require.Contains(t, output, `<knowledge_base_id>b1</knowledge_base_id>`)
	require.Contains(t, output, `knowledge_id="d1"`)

	toolCalls := []types.LLMToolCall{
		{Function: types.FunctionCall{Name: "wiki_read_source_doc", Arguments: `{"knowledge_id":"d1"}`}},
		{Function: types.FunctionCall{Name: "wiki_search", Arguments: `{"knowledge_base_id":"b1"}`}},
	}
	registry.DecodeToolCallsWithPolicy(toolCalls, sourceArgumentAllowed)
	require.JSONEq(t, `{"knowledge_id":"doc-real-id"}`, toolCalls[0].Function.Arguments)
	require.JSONEq(t, `{"knowledge_base_id":"kb-real-id"}`, toolCalls[1].Function.Arguments)
}

func TestModelOutputGraphResultsUseChunkAliases(t *testing.T) {
	registry := newSourceRegistry()
	output := registry.ModelOutput(&types.ToolResult{
		Success: true,
		Data: map[string]interface{}{
			"display_type": "graph_query_results",
			"results": []map[string]interface{}{{
				"chunk_id":          "graph-chunk-real",
				"chunk_index":       4,
				"knowledge_id":      "graph-doc-real",
				"knowledge_base_id": "graph-kb-real",
				"knowledge_title":   "Graph Source",
				"content":           "A relates to B.",
			}},
		},
	})

	require.Contains(t, output, `<retrieval type="knowledge" mode="graph">`)
	require.Contains(t, output, `<chunk id="c1" index="4" view="full">`)
	require.NotContains(t, output, "graph-chunk-real")
	require.Equal(t,
		`<kb doc="Graph Source" chunk_id="graph-chunk-real" kb_id="graph-kb-real" />`,
		registry.ExpandText(`<ref id="c1"/>`),
	)
}

func TestModelOutputDoesNotRegisterInternalSchemesAsWebSources(t *testing.T) {
	registry := newSourceRegistry()
	output := registry.ModelOutput(&types.ToolResult{
		Success: true,
		Output:  `{"url":"res://0001","knowledge_id":"doc-real-id"}`,
	})

	// An internal handle in a url-labeled field must never enter the web
	// handle space, where CompactKnownText would rewrite it a second time.
	require.Contains(t, output, "res://0001")
	require.NotContains(t, output, "w1")
	require.Contains(t, output, "d1")
	require.NotContains(t, output, "doc-real-id")

	registry.ModelOutput(&types.ToolResult{
		Success: true,
		Output:  `{"url":"https://example.com/page"}`,
	})
	require.Equal(t,
		`<web url="https://example.com/page" title="" />`,
		registry.ExpandText(`<ref id="w1"/>`),
	)
}
