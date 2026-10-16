package chatpipeline

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestExpandedContextHasSeparateCitationBodies(t *testing.T) {
	a := &types.Chunk{
		ID:              "a",
		KnowledgeID:     "doc",
		KnowledgeBaseID: "kb",
		Content:         "Opening greeting.",
		ParentChunkID:   "p",
		ChunkType:       types.ChunkTypeText,
		IsEnabled:       true,
	}
	b := &types.Chunk{
		ID:              "b",
		KnowledgeID:     "doc",
		KnowledgeBaseID: "kb",
		Content:         "Exact later quotation.",
		ParentChunkID:   "p",
		ChunkType:       types.ChunkTypeText,
		IsEnabled:       true,
		ChunkIndex:      1,
	}
	disabled := &types.Chunk{
		ID:              "disabled",
		KnowledgeID:     "doc",
		KnowledgeBaseID: "kb",
		Content:         "Exact later quotation.",
		ParentChunkID:   "p",
		ChunkType:       types.ChunkTypeText,
	}
	foreign := &types.Chunk{
		ID:              "foreign",
		KnowledgeID:     "other",
		KnowledgeBaseID: "kb",
		Content:         "Opening greeting.",
		IsEnabled:       true,
	}
	repo := &expandChunkRepo{
		chunks: map[string]*types.Chunk{
			"a": a,
			"p": {
				ID:        "p",
				ChunkType: types.ChunkTypeParentText,
			},
		},
		children: map[string][]*types.Chunk{"p": {
			a,
			b,
			disabled,
			foreign,
		}},
	}
	p := &PluginMerge{chunkRepo: repo}
	merged := &types.SearchResult{
		ID:               "a",
		KnowledgeID:      "doc",
		KnowledgeBaseID:  "kb",
		Content:          "Opening greeting.\n\nExact later quotation.",
		ContentRewritten: true,
	}
	p.attachCitationSources(context.Background(), []*types.SearchResult{merged})
	got := expandCitationSources([]*types.SearchResult{merged, merged})
	require.Len(t, got, 2)
	require.Equal(t, "a", got[0].ID)
	require.Equal(t, a.Content, got[0].Content)
	require.Equal(t, "b", got[1].ID)
	require.Equal(t, b.Content, got[1].Content)
	require.Empty(t, got[0].SubChunkID)
}

func TestExpandedEvidenceFailsClosedAndPreservesPlainSources(t *testing.T) {
	got := expandCitationSources([]*types.SearchResult{
		{
			ID:               "expanded",
			Content:          "parent text under child ID",
			ContentRewritten: true,
		},
		{
			ID:      "plain",
			Content: "actual source",
		},
	})
	require.Len(t, got, 1)
	require.Equal(t, "plain", got[0].ID)
}

func TestModelHandlesReferenceTheActualExpandedChild(t *testing.T) {
	a := &types.SearchResult{
		ID:              "opening",
		KnowledgeID:     "doc",
		KnowledgeBaseID: "kb",
		KnowledgeTitle:  "Report",
		Content:         "Opening greeting.",
	}
	b := &types.SearchResult{
		ID:              "later",
		KnowledgeID:     "doc",
		KnowledgeBaseID: "kb",
		KnowledgeTitle:  "Report",
		ChunkIndex:      1,
		Content:         "Exact later quotation.",
	}
	rendered := "old expanded context"
	manage := &types.ChatManage{
		PipelineRequest: types.PipelineRequest{
			Query:         "question",
			SummaryConfig: types.SummaryConfig{Prompt: "system"},
		},
		PipelineState: types.PipelineState{
			RenderedContexts: rendered,
			UserContent:      rendered,
			MergeResult: []*types.SearchResult{{
				ID:               "opening",
				ContentRewritten: true,
				CitationSources: []*types.SearchResult{
					a,
					b,
				},
			}},
		},
	}
	messages, registry := prepareMessagesWithModelContext(context.Background(), manage)
	require.Contains(t, messages[1].Content, `<chunk id="c2"`)
	require.Contains(t, registry.DecodeOutputText(`<ref id="c2"/>`), `chunk_id="later"`)
	require.Contains(t, registry.DecodeOutputText(`<ref id="c1"/>`), `chunk_id="opening"`)
	manage.MergeResult[0].CitationSources = nil
	messages, _ = prepareMessagesWithModelContext(context.Background(), manage)
	require.NotContains(t, messages[1].Content, rendered)
	require.Contains(t, messages[1].Content, "could not be verified")
}
