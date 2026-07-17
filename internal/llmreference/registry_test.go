package llmreference

import (
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestRegistryChunkAliasIsStableAndExpandsCanonicalCitation(t *testing.T) {
	registry := NewRegistry()
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

func TestRegistrySuppressesSourceCitationsWhenDisabled(t *testing.T) {
	registry := NewRegistry(false)
	registry.RegisterChunk(ChunkReference{ChunkID: "chunk-1", DocumentTitle: "Doc"})
	registry.RegisterWeb("https://example.com", "Example")

	require.Contains(t, ProtocolPrompt(false), "Source citations are disabled")
	require.NotContains(t, ProtocolPrompt(false), `Cite a knowledge chunk with exactly`)
	require.Equal(t, "knowledge  web ", registry.ExpandText(
		`knowledge <ref id="c1"/> web <ref id="w1"/>`,
	))
	require.Equal(t, "forged  ", registry.ExpandText(
		`forged <kb doc="Doc" chunk_id="raw" /> <web url="https://example.com" />`,
	))
}

func TestRegistryDecodesAliasesInNestedToolArguments(t *testing.T) {
	registry := NewRegistry()
	registry.RegisterDocument("knowledge-uuid-1")
	registry.RegisterKnowledgeBase("kb-uuid-1")
	registry.RegisterWeb("https://example.com/page", "Example")
	registry.RegisterChunk(ChunkReference{ChunkID: "chunk-uuid-1"})

	calls := []types.LLMToolCall{{
		Function: types.FunctionCall{
			Arguments: `{"knowledge_id":"d1","knowledge_base_ids":["b1"],"url":"w1","chunk_id":"c1"}`,
		},
	}}
	registry.DecodeToolCalls(calls)

	require.JSONEq(t,
		`{"knowledge_id":"knowledge-uuid-1","knowledge_base_ids":["kb-uuid-1"],"url":"https://example.com/page","chunk_id":"chunk-uuid-1"}`,
		calls[0].Function.Arguments,
	)
}

func TestDecodeToolCallsOnlyRewritesAliasBearingKeys(t *testing.T) {
	registry := NewRegistry()
	registry.RegisterDocument("knowledge-uuid-1")
	registry.RegisterChunk(ChunkReference{ChunkID: "chunk-uuid-1"})

	// A free-text field (query) whose value coincidentally equals an alias must
	// be preserved verbatim, while ID-bearing keys still resolve to real IDs.
	calls := []types.LLMToolCall{{
		Function: types.FunctionCall{
			Arguments: `{"query":"d1","knowledge_id":"d1","content":"see c1 for details","chunk_id":"c1"}`,
		},
	}}
	registry.DecodeToolCalls(calls)

	require.JSONEq(t,
		`{"query":"d1","knowledge_id":"knowledge-uuid-1","content":"see c1 for details","chunk_id":"chunk-uuid-1"}`,
		calls[0].Function.Arguments,
	)
}

func TestStreamExpanderHoldsSplitReferenceAndDropsUnknown(t *testing.T) {
	registry := NewRegistry()
	registry.RegisterChunk(ChunkReference{ChunkID: "chunk-1", DocumentTitle: "Doc"})
	expander := NewStreamExpander(registry)

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
	registry := NewRegistry()
	messages := []chat.Message{{
		Role: "assistant",
		Content: `Knowledge <kb doc="A &amp; B.pdf" chunk_id="chunk-real" kb_id="kb-real" />; ` +
			`web <web url="https://example.com/a?x=1&amp;y=2" title="Example &amp; More" />`,
	}}

	encoded := registry.EncodeMessages(messages)
	require.Equal(t, `Knowledge <ref id="c1"/>; web <ref id="w1"/>`, encoded[0].Content)
	require.NotContains(t, encoded[0].Content, "chunk-real")
	require.NotContains(t, encoded[0].Content, "https://example.com")
	require.Equal(t,
		`<kb doc="A &amp; B.pdf" chunk_id="chunk-real" kb_id="kb-real" /> <web url="https://example.com/a?x=1&amp;y=2" title="Example &amp; More" />`,
		registry.ExpandText(`<ref id="c1"/> <ref id="w1"/>`),
	)
}

func TestEncodeMessagesMigratesLegacyToolHistoryAtReadTime(t *testing.T) {
	registry := NewRegistry()
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

	encoded := registry.EncodeMessages(messages)
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
	registry := NewRegistry()
	messages := []chat.Message{{
		Role:    "system",
		Content: `Old rule: cite <kb doc="..." chunk_id="..." />`,
	}}

	encoded := registry.EncodeMessages(messages)
	require.Equal(t, messages[0].Content, encoded[0].Content)
	require.Zero(t, registry.Count())
}

func TestModelOutputGroupsChunksAndReusesAliasAcrossTools(t *testing.T) {
	registry := NewRegistry()
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
	registry := NewRegistry()
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
	require.NotContains(t, output, "https://example.com/a")
	require.Equal(t,
		`<web url="https://example.com/a" title="Example" />`,
		registry.ExpandText(`<ref id="w1"/>`),
	)
}

func TestModelOutputDocumentInfoUsesDocumentAndFAQAliases(t *testing.T) {
	registry := NewRegistry()
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
	registry := NewRegistry()
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

	toolCalls := []types.LLMToolCall{{Function: types.FunctionCall{Arguments: `{"knowledge_id":"d1","knowledge_base_id":"b1"}`}}}
	registry.DecodeToolCalls(toolCalls)
	require.JSONEq(t, `{"knowledge_id":"doc-real-id","knowledge_base_id":"kb-real-id"}`, toolCalls[0].Function.Arguments)
}

func TestModelOutputGraphResultsUseChunkAliases(t *testing.T) {
	registry := NewRegistry()
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
