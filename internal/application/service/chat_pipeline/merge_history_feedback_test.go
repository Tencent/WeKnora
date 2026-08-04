package chatpipeline

import (
	"context"
	"errors"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

type historyFeedbackChunkRepo struct {
	interfaces.ChunkRepository
	chunks       []*types.Chunk
	requestedIDs []string
	err          error
}

func (r *historyFeedbackChunkRepo) ListChunksByIDOnly(_ context.Context, ids []string) ([]*types.Chunk, error) {
	r.requestedIDs = append([]string(nil), ids...)
	return r.chunks, r.err
}

func TestHistoryInjectionTracksKBChunksAndRefreshesRecallWeights(t *testing.T) {
	storedA := &types.SearchResult{
		ID:              "chunk-a",
		Content:         "alpha beta first",
		KnowledgeID:     "document-1",
		KnowledgeBaseID: "kb-1",
		Score:           1.2,
		RecallWeight:    1.5,
		MatchType:       types.MatchTypeEmbedding,
		Metadata: map[string]string{
			"recall_weight_original_score": "0.8000",
		},
	}
	storedB := &types.SearchResult{
		ID:              "chunk-b",
		Content:         "alpha beta second",
		KnowledgeID:     "document-1",
		KnowledgeBaseID: "kb-1",
		Score:           0.3,
		RecallWeight:    0.5,
		MatchType:       types.MatchTypeEmbedding,
		Metadata: map[string]string{
			"recall_weight_original_score": "0.6000",
		},
	}
	repo := &historyFeedbackChunkRepo{chunks: []*types.Chunk{
		{ID: "chunk-a", RecallWeight: 0.5},
		{ID: "chunk-b", RecallWeight: 1.5},
	}}
	plugin := &PluginMerge{chunkRepo: repo}
	chatManage := &types.ChatManage{
		PipelineRequest: types.PipelineRequest{
			Query: "alpha beta",
			SearchTargets: types.SearchTargets{{
				Type:            types.SearchTargetTypeKnowledgeBase,
				KnowledgeBaseID: "kb-1",
				TenantID:        7,
			}},
		},
		PipelineState: types.PipelineState{History: []*types.History{{
			KnowledgeReferences: types.References{storedA, storedB},
		}}},
	}

	got := plugin.injectHistoryResults(context.Background(), chatManage, nil)

	require.Equal(t, []string{"chunk-a", "chunk-b"}, repo.requestedIDs)
	require.Len(t, got, 2)
	require.Equal(t, "chunk-b", got[0].ID)
	require.Equal(t, 1.5, got[0].RecallWeight)
	require.InDelta(t, 0.54, got[0].Score, 0.0001)
	require.Equal(t, "chunk-a", got[1].ID)
	require.Equal(t, 0.5, got[1].RecallWeight)
	require.InDelta(t, 0.24, got[1].Score, 0.0001)
	require.Equal(t, []string{"chunk-b", "chunk-a"}, types.CollectSearchResultChunkIDs(got))

	// History injection must not mutate the references loaded from the message.
	require.Equal(t, 1.2, storedA.Score)
	require.Equal(t, 1.5, storedA.RecallWeight)
	require.Equal(t, types.MatchTypeEmbedding, storedA.MatchType)
	require.NotContains(t, storedA.Metadata, "history_reference")
}

func TestHistoryInjectionDropsDeletedKBChunksBeforeFeedbackAttribution(t *testing.T) {
	repo := &historyFeedbackChunkRepo{chunks: []*types.Chunk{
		{ID: "chunk-existing", RecallWeight: 1},
	}}
	plugin := &PluginMerge{chunkRepo: repo}
	chatManage := &types.ChatManage{
		PipelineRequest: types.PipelineRequest{
			Query: "alpha beta",
			SearchTargets: types.SearchTargets{{
				Type:            types.SearchTargetTypeKnowledgeBase,
				KnowledgeBaseID: "kb-1",
				TenantID:        7,
			}},
		},
		PipelineState: types.PipelineState{History: []*types.History{{
			KnowledgeReferences: types.References{
				{
					ID:              "chunk-deleted",
					Content:         "alpha beta",
					KnowledgeBaseID: "kb-1",
					Score:           0.9,
					MatchType:       types.MatchTypeEmbedding,
				},
				{
					ID:              "chunk-existing",
					Content:         "alpha beta",
					KnowledgeBaseID: "kb-1",
					Score:           0.8,
					MatchType:       types.MatchTypeEmbedding,
				},
			},
		}}},
	}

	got := plugin.injectHistoryResults(context.Background(), chatManage, nil)

	require.Len(t, got, 1)
	require.Equal(t, "chunk-existing", got[0].ID)
	require.Equal(t, []string{"chunk-existing"}, types.CollectSearchResultChunkIDs(got))
}

func TestHistoryInjectionRejectsChunkFromRemovedKnowledgeBaseScope(t *testing.T) {
	repo := &historyFeedbackChunkRepo{chunks: []*types.Chunk{
		{ID: "chunk-revoked", RecallWeight: 1},
	}}
	plugin := &PluginMerge{chunkRepo: repo}
	chatManage := &types.ChatManage{
		PipelineRequest: types.PipelineRequest{
			Query: "alpha beta",
			SearchTargets: types.SearchTargets{{
				Type:            types.SearchTargetTypeKnowledgeBase,
				KnowledgeBaseID: "kb-current",
				TenantID:        7,
			}},
		},
		PipelineState: types.PipelineState{History: []*types.History{{
			KnowledgeReferences: types.References{{
				ID:              "chunk-revoked",
				Content:         "alpha beta",
				KnowledgeID:     "document-revoked",
				KnowledgeBaseID: "kb-revoked",
				Score:           0.9,
				MatchType:       types.MatchTypeEmbedding,
			}},
		}}},
	}

	got := plugin.injectHistoryResults(context.Background(), chatManage, nil)

	require.Empty(t, got)
	require.Empty(t, repo.requestedIDs)
}

func TestHistoryInjectionDoesNotWidenExplicitDocumentScope(t *testing.T) {
	repo := &historyFeedbackChunkRepo{chunks: []*types.Chunk{
		{ID: "chunk-other-document", RecallWeight: 1},
	}}
	plugin := &PluginMerge{chunkRepo: repo}
	chatManage := &types.ChatManage{
		PipelineRequest: types.PipelineRequest{
			Query:            "alpha beta",
			KnowledgeBaseIDs: []string{"kb-1"},
			SearchTargets: types.SearchTargets{{
				Type:            types.SearchTargetTypeKnowledge,
				KnowledgeBaseID: "kb-1",
				TenantID:        7,
				KnowledgeIDs:    []string{"document-current"},
			}},
		},
		PipelineState: types.PipelineState{History: []*types.History{{
			KnowledgeReferences: types.References{{
				ID:              "chunk-other-document",
				Content:         "alpha beta",
				KnowledgeID:     "document-other",
				KnowledgeBaseID: "kb-1",
				Score:           0.9,
				MatchType:       types.MatchTypeEmbedding,
			}},
		}}},
	}

	got := plugin.injectHistoryResults(context.Background(), chatManage, nil)

	require.Empty(t, got)
	require.Empty(t, repo.requestedIDs)
}

func TestHistoryInjectionFailsClosedWhenCurrentChunkLookupFails(t *testing.T) {
	repo := &historyFeedbackChunkRepo{err: errors.New("temporary database failure")}
	plugin := &PluginMerge{chunkRepo: repo}
	chatManage := &types.ChatManage{
		PipelineRequest: types.PipelineRequest{
			Query: "alpha beta",
			SearchTargets: types.SearchTargets{{
				Type:            types.SearchTargetTypeKnowledgeBase,
				KnowledgeBaseID: "kb-1",
				TenantID:        7,
			}},
		},
		PipelineState: types.PipelineState{History: []*types.History{{
			KnowledgeReferences: types.References{{
				ID:              "chunk-unverified",
				Content:         "alpha beta",
				KnowledgeID:     "document-1",
				KnowledgeBaseID: "kb-1",
				Score:           0.9,
				MatchType:       types.MatchTypeEmbedding,
			}},
		}}},
	}

	got := plugin.injectHistoryResults(context.Background(), chatManage, nil)

	require.Empty(t, got)
	require.Equal(t, []string{"chunk-unverified"}, repo.requestedIDs)
}
