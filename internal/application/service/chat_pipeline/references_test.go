package chatpipeline

import (
	"context"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestPrepareMessagesWithReferencesUsesChunkCentricContext(t *testing.T) {
	rendered := `<context id="1">first content</context><context id="2">second content</context>`
	manage := &types.ChatManage{
		PipelineRequest: types.PipelineRequest{
			Query: "question",
			SummaryConfig: types.SummaryConfig{
				Prompt: "system",
			},
		},
		PipelineState: types.PipelineState{
			RenderedContexts: rendered,
			UserContent:      "References:\n" + rendered + "\nQuestion: question",
			MergeResult: []*types.SearchResult{
				{ID: "chunk-1", KnowledgeID: "doc-1", KnowledgeBaseID: "kb-1", KnowledgeTitle: "Doc", ChunkIndex: 1, Content: "first content"},
				{ID: "chunk-2", KnowledgeID: "doc-1", KnowledgeBaseID: "kb-1", KnowledgeTitle: "Doc", ChunkIndex: 2, Content: "second content"},
			},
		},
	}

	messages, refs := prepareMessagesWithReferences(context.Background(), manage)
	require.Len(t, messages, 2)
	require.Contains(t, messages[0].Content, "Source handling protocol")
	require.Contains(t, messages[1].Content, `<document id="d1" kb="b1" title="Doc">`)
	require.Contains(t, messages[1].Content, `<chunk id="c1" index="1" view="full">`)
	require.Contains(t, messages[1].Content, `<chunk id="c2" index="2" view="full">`)
	require.False(t, strings.Contains(messages[1].Content, "chunk-1"))
	require.Equal(t,
		`<kb doc="Doc" chunk_id="chunk-1" kb_id="kb-1" />`,
		refs.ExpandText(`<ref id="c1"/>`),
	)
}

func TestPrepareMessagesWithReferencesReplacesSystemPromptContextAndHistoryCitations(t *testing.T) {
	rendered := `<context id="1">first content</context>`
	manage := &types.ChatManage{
		PipelineRequest: types.PipelineRequest{
			Query: "question",
			SummaryConfig: types.SummaryConfig{
				Prompt: `System references: {{contexts}}`,
			},
		},
		PipelineState: types.PipelineState{
			RenderedContexts: rendered,
			UserContent:      "Question: question",
			History: []*types.History{{
				Query:  "previous",
				Answer: `Previous <kb doc="Old" chunk_id="old-chunk" kb_id="old-kb" />`,
			}},
			MergeResult: []*types.SearchResult{{
				ID: "current-chunk", KnowledgeID: "current-doc", KnowledgeBaseID: "current-kb", KnowledgeTitle: "Current", Content: "first content",
			}},
		},
	}

	messages, refs := prepareMessagesWithReferences(context.Background(), manage)
	messages = refs.EncodeMessages(messages)
	require.NotContains(t, messages[0].Content, rendered)
	require.Contains(t, messages[0].Content, `<chunk id="c1"`)
	require.Contains(t, messages[2].Content, `<ref id="c2"/>`)
	require.NotContains(t, messages[2].Content, "old-chunk")
}

func TestPrepareMessagesWithReferencesKeepsWebSeparateFromChunks(t *testing.T) {
	manage := &types.ChatManage{
		PipelineRequest: types.PipelineRequest{Query: "question", SummaryConfig: types.SummaryConfig{Prompt: "system"}},
		PipelineState: types.PipelineState{
			UserContent: "question",
			MergeResult: []*types.SearchResult{{
				ID:              "https://example.com/page",
				KnowledgeTitle:  "Example",
				Content:         "web content",
				ChunkType:       string(types.ChunkTypeWebSearch),
				KnowledgeSource: "web_search",
			}},
		},
	}

	messages, refs := prepareMessagesWithReferences(context.Background(), manage)
	require.Contains(t, messages[1].Content, `<retrieval type="web" mode="search">`)
	require.Contains(t, messages[1].Content, `<page id="w1" title="Example">`)
	require.NotContains(t, messages[1].Content, `<chunk id="c1"`)
	require.Equal(t,
		`<web url="https://example.com/page" title="Example" />`,
		refs.ExpandText(`<ref id="w1"/>`),
	)
}

func TestPrepareMessagesWithReferencesCompactsHistoryWithoutCurrentRetrieval(t *testing.T) {
	manage := &types.ChatManage{
		PipelineRequest: types.PipelineRequest{Query: "follow-up", SummaryConfig: types.SummaryConfig{Prompt: "system"}},
		PipelineState: types.PipelineState{
			UserContent: "follow-up",
			History: []*types.History{{
				Query:  "previous",
				Answer: `Previous <web url="https://example.com/old" title="Old" />`,
			}},
		},
	}

	messages, refs := prepareMessagesWithReferences(context.Background(), manage)
	messages = refs.EncodeMessages(messages)
	require.Contains(t, messages[0].Content, "Source handling protocol")
	require.Contains(t, messages[2].Content, `<ref id="w1"/>`)
	require.NotContains(t, messages[2].Content, "https://example.com/old")
}

func TestPrepareMessagesWithReferencesSuppressesCitationsWhenDisabled(t *testing.T) {
	disabled := false
	manage := &types.ChatManage{
		PipelineRequest: types.PipelineRequest{
			Query:           "question",
			CitationEnabled: &disabled,
			SummaryConfig:   types.SummaryConfig{Prompt: "custom system prompt"},
		},
		PipelineState: types.PipelineState{
			UserContent: "question",
			MergeResult: []*types.SearchResult{{
				ID: "chunk-1", KnowledgeID: "doc-1", KnowledgeBaseID: "kb-1", KnowledgeTitle: "Doc", Content: "evidence",
			}},
		},
	}

	messages, refs := prepareMessagesWithReferences(context.Background(), manage)
	require.Contains(t, messages[0].Content, "Source citations are disabled")
	require.Contains(t, messages[1].Content, `<chunk id="c1"`)
	require.NotContains(t, messages[1].Content, "chunk-1")
	require.Equal(t, "answer ", refs.ExpandText(`answer <ref id="c1"/>`))
}
