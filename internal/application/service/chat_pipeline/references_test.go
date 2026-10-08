package chatpipeline

import (
	"context"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestPrepareMessagesWithModelContextUsesChunkCentricContext(t *testing.T) {
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

	messages, refs := prepareMessagesWithModelContext(context.Background(), manage)
	require.Len(t, messages, 2)
	require.Contains(t, messages[0].Content, "Source handling protocol")
	require.Contains(t, messages[1].Content, `<document id="d1" kb="b1" title="Doc">`)
	require.Contains(t, messages[1].Content, `<chunk id="c1" index="1" view="full">`)
	require.Contains(t, messages[1].Content, `<chunk id="c2" index="2" view="full">`)
	require.False(t, strings.Contains(messages[1].Content, "chunk-1"))
	require.Equal(t,
		`<kb doc="Doc" chunk_id="chunk-1" kb_id="kb-1" />`,
		refs.DecodeOutputText(`<ref id="c1"/>`),
	)
}

func TestPrepareMessagesWithModelContextReplacesSystemPromptContextAndHistoryCitations(t *testing.T) {
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

	messages, refs := prepareMessagesWithModelContext(context.Background(), manage)
	messages = refs.EncodeMessages(messages)
	require.NotContains(t, messages[0].Content, rendered)
	require.Contains(t, messages[0].Content, `<chunk id="c1"`)
	require.Contains(t, messages[2].Content, `<ref id="c2"/>`)
	require.NotContains(t, messages[2].Content, "old-chunk")
}

func TestPrepareMessagesWithModelContextKeepsWebSeparateFromChunks(t *testing.T) {
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

	messages, refs := prepareMessagesWithModelContext(context.Background(), manage)
	require.Contains(t, messages[1].Content, `<retrieval type="web" mode="search" trust="untrusted">`)
	require.Contains(t, messages[1].Content, `<page id="w1" title="Example">`)
	require.NotContains(t, messages[1].Content, `<chunk id="c1"`)
	require.Equal(t,
		`<web url="https://example.com/page" title="Example" />`,
		refs.DecodeOutputText(`<ref id="w1"/>`),
	)
}

func TestPrepareMessagesWithModelContextCompactsHistoryWithoutCurrentRetrieval(t *testing.T) {
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

	messages, refs := prepareMessagesWithModelContext(context.Background(), manage)
	messages = refs.EncodeMessages(messages)
	require.Contains(t, messages[0].Content, "Source handling protocol")
	require.Contains(t, messages[2].Content, `<ref id="w1"/>`)
	require.NotContains(t, messages[2].Content, "https://example.com/old")
}

func TestPrepareMessagesWithModelContextSuppressesCitationsWhenDisabled(t *testing.T) {
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

	messages, refs := prepareMessagesWithModelContext(context.Background(), manage)
	require.Contains(t, messages[0].Content, "Source citations are disabled")
	require.Contains(t, messages[1].Content, `<chunk id="c1"`)
	require.NotContains(t, messages[1].Content, "chunk-1")
	require.Equal(t, "answer ", refs.DecodeOutputText(`answer <ref id="c1"/>`))
}

// A truncated ranked list must reach the model context view: this is the text
// the model actually reads, and RenderedContexts is replaced wholesale. N is
// the number of passages this view renders, M the candidate pool the first
// cutting stage received.
func TestPrepareMessagesWithModelContextReportsTruncatedRetrieval(t *testing.T) {
	manage := &types.ChatManage{
		PipelineRequest: types.PipelineRequest{
			Query:         "who holds the CCSK certificate",
			SummaryConfig: types.SummaryConfig{Prompt: "system"},
		},
		PipelineState: types.PipelineState{
			UserContent: "question",
			MergeResult: []*types.SearchResult{{
				ID: "chunk-1", KnowledgeID: "doc-1", KnowledgeBaseID: "kb-1",
				KnowledgeTitle: "Doc", Content: "evidence",
			}},
			Truncation: &types.RetrievalTruncation{
				Stage:      types.RetrievalStageRerank,
				Candidates: 146,
			},
		},
	}

	messages, _ := prepareMessagesWithModelContext(context.Background(), manage)
	require.Contains(t, messages[1].Content, `<subset shown="1" candidates="146">`)
	require.Contains(t, messages[1].Content, "state that the provided context contains only the top 1 of 146")
	require.Contains(t, messages[1].Content, "otherwise answer normally without adding a disclaimer")

	manage.Truncation = nil
	messages, _ = prepareMessagesWithModelContext(context.Background(), manage)
	require.NotContains(t, messages[1].Content, "<subset")
}

// End to end: FILTER_TOP_K drops candidates, INTO_CHAT_MESSAGE renders the
// passages, and the caveat must land in the messages the model reads — not in
// RenderedContexts, which this stage replaces wholesale.
func TestTruncatedRetrievalReachesModelMessages(t *testing.T) {
	manage := &types.ChatManage{
		PipelineRequest: types.PipelineRequest{
			Query:         "who holds the CCSK certificate",
			RerankTopK:    2,
			SummaryConfig: types.SummaryConfig{Prompt: "system", ContextTemplate: "{{query}}\n{{contexts}}"},
		},
		PipelineState: types.PipelineState{
			MergeResult: []*types.SearchResult{
				{ID: "c1", KnowledgeID: "doc-1", KnowledgeBaseID: "kb-1", Content: "first", Score: 0.9},
				{ID: "c2", KnowledgeID: "doc-1", KnowledgeBaseID: "kb-1", Content: "second", Score: 0.8},
				{ID: "c3", KnowledgeID: "doc-1", KnowledgeBaseID: "kb-1", Content: "third", Score: 0.7},
			},
		},
	}
	next := func() *PluginError { return nil }
	filter := &PluginFilterTopK{}
	if err := filter.OnEvent(context.Background(), types.FILTER_TOP_K, manage, next); err != nil {
		t.Fatalf("filter_top_k: %v", err)
	}
	intoChat := &PluginIntoChatMessage{}
	if err := intoChat.OnEvent(context.Background(), types.INTO_CHAT_MESSAGE, manage, next); err != nil {
		t.Fatalf("into_chat_message: %v", err)
	}
	require.Len(t, manage.MergeResult, 2)
	require.NotContains(t, manage.RenderedContexts, "<subset")

	messages, _ := prepareMessagesWithModelContext(context.Background(), manage)
	require.Contains(t, messages[1].Content, `<subset shown="2" candidates="3">`)
	require.Contains(t, messages[1].Content, "second")
	require.NotContains(t, messages[1].Content, "third")
}

// End to end on the rerank path: with a rerank model the stage that cuts is
// rerank, not FILTER_TOP_K, which finds the already cut list and does nothing.
// Without the rerank record this turn reaches the prompt with no caveat at
// all, and a count question answers from the two shown passages as if they
// were every match.
func TestRerankTruncationReachesModelMessages(t *testing.T) {
	manage := &types.ChatManage{
		PipelineRequest: types.PipelineRequest{
			Query:           "how many certificates do we hold",
			RerankModelID:   "rr-1",
			RerankTopK:      2,
			RerankThreshold: 0.3,
			SummaryConfig:   types.SummaryConfig{Prompt: "system", ContextTemplate: "{{query}}\n{{contexts}}"},
		},
		PipelineState: types.PipelineState{
			RewriteQuery: "how many certificates do we hold",
			SearchResult: []*types.SearchResult{
				{ID: "c1", KnowledgeID: "doc-1", KnowledgeBaseID: "kb-1", Content: "first", Score: 0.5},
				{ID: "c2", KnowledgeID: "doc-2", KnowledgeBaseID: "kb-1", Content: "second", Score: 0.4},
				{ID: "c3", KnowledgeID: "doc-3", KnowledgeBaseID: "kb-1", Content: "third", Score: 0.3},
				{ID: "c4", KnowledgeID: "doc-4", KnowledgeBaseID: "kb-1", Content: "fourth", Score: 0.2},
			},
		},
	}
	next := func() *PluginError { return nil }
	rerank := &PluginRerank{modelService: &rerankOnlyModelService{
		reranker: &fixedReranker{scores: []float64{0.9, 0.8, 0.7, 0.6}},
	}}
	if err := rerank.OnEvent(context.Background(), types.CHUNK_RERANK, manage, next); err != nil {
		t.Fatalf("chunk_rerank: %v", err)
	}
	merge := &PluginMerge{}
	if err := merge.OnEvent(context.Background(), types.CHUNK_MERGE, manage, next); err != nil {
		t.Fatalf("chunk_merge: %v", err)
	}
	filter := &PluginFilterTopK{}
	if err := filter.OnEvent(context.Background(), types.FILTER_TOP_K, manage, next); err != nil {
		t.Fatalf("filter_top_k: %v", err)
	}
	require.Len(t, manage.MergeResult, 2)
	require.NotNil(t, manage.Truncation, "rerank dropped 2 of 4 candidates and must record it")
	require.Equal(t, types.RetrievalTruncation{
		Stage:      types.RetrievalStageRerank,
		Candidates: 4,
	}, *manage.Truncation)

	messages, _ := prepareMessagesWithModelContext(context.Background(), manage)
	require.Contains(t, messages[1].Content, `<subset shown="2" candidates="4">`)
}

// End to end on the merge fallback path: rerank produced nothing, merge cut
// the retrieval list to RerankTopK, and FILTER_TOP_K has nothing left to cut.
// The prompt must still carry the record of the earlier cut.
func TestMergeFallbackTruncationReachesModelMessages(t *testing.T) {
	manage := &types.ChatManage{
		PipelineRequest: types.PipelineRequest{
			Query:         "how many certificates do we hold",
			RerankTopK:    2,
			SummaryConfig: types.SummaryConfig{Prompt: "system", ContextTemplate: "{{query}}\n{{contexts}}"},
		},
		PipelineState: types.PipelineState{
			SearchResult: []*types.SearchResult{
				{ID: "c1", KnowledgeID: "doc-1", KnowledgeBaseID: "kb-1", Content: "first", Score: 0.9},
				{ID: "c2", KnowledgeID: "doc-2", KnowledgeBaseID: "kb-1", Content: "second", Score: 0.8},
				{ID: "c3", KnowledgeID: "doc-3", KnowledgeBaseID: "kb-1", Content: "third", Score: 0.7},
			},
		},
	}
	next := func() *PluginError { return nil }
	merge := &PluginMerge{}
	if err := merge.OnEvent(context.Background(), types.CHUNK_MERGE, manage, next); err != nil {
		t.Fatalf("chunk_merge: %v", err)
	}
	filter := &PluginFilterTopK{}
	if err := filter.OnEvent(context.Background(), types.FILTER_TOP_K, manage, next); err != nil {
		t.Fatalf("filter_top_k: %v", err)
	}
	require.Len(t, manage.MergeResult, 2)
	require.NotNil(t, manage.Truncation, "merge dropped 1 of 3 candidates and must record it")
	require.Equal(t, types.RetrievalTruncation{
		Stage:      types.RetrievalStageMerge,
		Candidates: 3,
	}, *manage.Truncation)

	messages, _ := prepareMessagesWithModelContext(context.Background(), manage)
	require.Contains(t, messages[1].Content, `<subset shown="2" candidates="3">`)
}
