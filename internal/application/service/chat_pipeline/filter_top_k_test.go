package chatpipeline

import (
	"context"
	"errors"
	"math"
	"testing"

	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type filterFeedbackRepo struct {
	interfaces.FeedbackRepository
	calls int
	stats []types.ChunkFeedbackStat
	err   error
}

func (r *filterFeedbackRepo) ListChunkFeedbackStats(
	_ context.Context, _ []types.ChunkFeedbackScope,
) ([]types.ChunkFeedbackStat, error) {
	r.calls++
	return r.stats, r.err
}

func TestFilterTopK_FeedbackGloballyDisabledPreservesBaselineRanking(t *testing.T) {
	repo := &filterFeedbackRepo{}
	chatManage := &types.ChatManage{
		PipelineRequest: types.PipelineRequest{RerankTopK: 3},
		PipelineState: types.PipelineState{
			MergeResult: []*types.SearchResult{
				{ID: "low", KnowledgeID: "doc-c", Score: 0.2},
				{ID: "high", KnowledgeID: "doc-a", Score: 0.9},
				{ID: "medium", KnowledgeID: "doc-b", Score: 0.5},
				{ID: "second", KnowledgeID: "doc-d", Score: 0.8},
			},
		},
	}

	plugin := &PluginFilterTopK{
		feedbackConfig: config.DefaultFeedbackConfig(),
		feedbackRepo:   repo,
	}
	err := plugin.OnEvent(
		context.Background(),
		types.FILTER_TOP_K,
		chatManage,
		func() *PluginError { return nil },
	)

	require.Nil(t, err)
	require.Len(t, chatManage.MergeResult, 3)
	assert.Equal(t, []string{"high", "second", "medium"}, searchResultIDs(chatManage.MergeResult))
	assert.Equal(t, []uint64{
		math.Float64bits(0.9),
		math.Float64bits(0.8),
		math.Float64bits(0.5),
	}, searchResultScoreBits(chatManage.MergeResult))
	assert.Zero(t, repo.calls)
}

func TestSortSearchResultsDeterministicallyUsesStableTieBreakers(t *testing.T) {
	results := []*types.SearchResult{
		{ID: "chunk-b", KnowledgeID: "doc-b", ChunkType: "text", StartAt: 10, EndAt: 20, Score: 0.8},
		{ID: "chunk-c", KnowledgeID: "doc-a", ChunkType: "summary", StartAt: 0, EndAt: 10, Score: 0.8},
		{ID: "chunk-a", KnowledgeID: "doc-a", ChunkType: "text", StartAt: 0, EndAt: 10, Score: 0.8},
	}
	sortSearchResultsDeterministically(results)
	assert.Equal(t, []string{"chunk-c", "chunk-a", "chunk-b"}, searchResultIDs(results))
}

func TestPluginFilterTopKDisabledDoesNotQueryFeedback(t *testing.T) {
	repo := &filterFeedbackRepo{}
	chatManage := &types.ChatManage{
		PipelineRequest: types.PipelineRequest{RerankTopK: 2},
		PipelineState: types.PipelineState{
			MergeResult: []*types.SearchResult{
				{ID: "neutral", KnowledgeBaseID: "kb", Score: 0.90},
				{ID: "promoted", KnowledgeBaseID: "kb", Score: 0.80},
				{ID: "demoted", KnowledgeBaseID: "kb", Score: 0.95},
			},
		},
	}

	plugin := &PluginFilterTopK{
		feedbackConfig: config.DefaultFeedbackConfig(),
		feedbackRepo:   repo,
	}
	err := plugin.OnEvent(
		context.Background(),
		types.FILTER_TOP_K,
		chatManage,
		func() *PluginError { return nil },
	)

	require.Nil(t, err)
	assert.Equal(t, []string{"demoted", "neutral"}, searchResultIDs(chatManage.MergeResult))
	assert.Zero(t, repo.calls)
}

func TestPluginFilterTopKWorkspaceDisabledDoesNotQueryFeedback(t *testing.T) {
	cfg := config.DefaultFeedbackConfig()
	cfg.RetrievalWeightEnabled = true
	repo := &filterFeedbackRepo{}
	chatManage := &types.ChatManage{
		PipelineRequest: types.PipelineRequest{
			RerankTopK: 2,
			SearchTargets: types.SearchTargets{&types.SearchTarget{
				TenantID: 1, KnowledgeBaseID: "kb", FeedbackRetrievalWeightEnabled: false,
			}},
		},
		PipelineState: types.PipelineState{
			MergeResult: []*types.SearchResult{
				{ID: "neutral", KnowledgeBaseID: "kb", Score: 0.90},
				{ID: "promoted", KnowledgeBaseID: "kb", Score: 0.80},
				{ID: "demoted", KnowledgeBaseID: "kb", Score: 0.95},
			},
		},
	}

	err := (&PluginFilterTopK{feedbackConfig: cfg, feedbackRepo: repo}).OnEvent(
		context.Background(), types.FILTER_TOP_K, chatManage,
		func() *PluginError { return nil },
	)

	require.Nil(t, err)
	assert.Equal(t, []string{"demoted", "neutral"}, searchResultIDs(chatManage.MergeResult))
	assert.Zero(t, repo.calls)
}

func TestPluginFilterTopKFailOpenPreservesBaselineRanking(t *testing.T) {
	tests := []struct {
		name         string
		configure    func(*config.FeedbackConfig)
		repo         *filterFeedbackRepo
		wantRepoCall int
	}{
		{
			name: "nil config",
			repo: &filterFeedbackRepo{},
		},
		{
			name: "repository error",
			repo: &filterFeedbackRepo{err: errors.New("feedback unavailable")},
			configure: func(cfg *config.FeedbackConfig) {
				cfg.RetrievalWeightEnabled = true
			},
			wantRepoCall: 1,
		},
		{
			name: "no feedback stats",
			repo: &filterFeedbackRepo{},
			configure: func(cfg *config.FeedbackConfig) {
				cfg.RetrievalWeightEnabled = true
			},
			wantRepoCall: 1,
		},
		{
			name: "invalid runtime weight",
			repo: &filterFeedbackRepo{},
			configure: func(cfg *config.FeedbackConfig) {
				cfg.RetrievalWeightEnabled = true
				cfg.HighRecallWeight = math.Inf(1)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var cfg *config.FeedbackConfig
			if tt.configure != nil {
				cfg = config.DefaultFeedbackConfig()
				tt.configure(cfg)
			}
			results := []*types.SearchResult{
				{ID: "low", KnowledgeBaseID: "kb", Score: 0.2},
				{ID: "high", KnowledgeBaseID: "kb", Score: 0.9},
				{ID: "medium", KnowledgeBaseID: "kb", Score: 0.5},
				{ID: "second", KnowledgeBaseID: "kb", Score: 0.8},
			}
			chatManage := &types.ChatManage{
				PipelineRequest: types.PipelineRequest{
					RerankTopK: 3,
					SearchTargets: types.SearchTargets{&types.SearchTarget{
						TenantID: 1, KnowledgeBaseID: "kb", FeedbackRetrievalWeightEnabled: true,
					}},
				},
				PipelineState: types.PipelineState{MergeResult: results},
			}

			err := (&PluginFilterTopK{feedbackConfig: cfg, feedbackRepo: tt.repo}).OnEvent(
				context.Background(), types.FILTER_TOP_K, chatManage,
				func() *PluginError { return nil },
			)

			require.Nil(t, err)
			assert.Equal(t, []string{"high", "second", "medium"}, searchResultIDs(chatManage.MergeResult))
			assert.Equal(t, []uint64{
				math.Float64bits(0.9),
				math.Float64bits(0.8),
				math.Float64bits(0.5),
			}, searchResultScoreBits(chatManage.MergeResult))
			assert.Equal(t, tt.wantRepoCall, tt.repo.calls)
		})
	}
}

func TestPluginFilterTopKEnabledAppliesEffectiveWeightAtFinalCutoff(t *testing.T) {
	cfg := config.DefaultFeedbackConfig()
	cfg.RetrievalWeightEnabled = true
	repo := &filterFeedbackRepo{stats: []types.ChunkFeedbackStat{
		{
			ChunkFeedbackScope: types.ChunkFeedbackScope{
				TenantID: 1, KnowledgeBaseID: "kb", ChunkID: "neutral",
			},
			LikeCount: 3, DislikeCount: 2, StoredRecallWeight: 1,
		},
		{
			ChunkFeedbackScope: types.ChunkFeedbackScope{
				TenantID: 1, KnowledgeBaseID: "kb", ChunkID: "promoted",
			},
			LikeCount: 5, StoredRecallWeight: 0.8,
		},
		{
			ChunkFeedbackScope: types.ChunkFeedbackScope{
				TenantID: 1, KnowledgeBaseID: "kb", ChunkID: "demoted",
			},
			DislikeCount: 5, StoredRecallWeight: 1.2,
		},
	}}
	chatManage := &types.ChatManage{
		PipelineRequest: types.PipelineRequest{
			RerankTopK: 2,
			SearchTargets: types.SearchTargets{&types.SearchTarget{
				TenantID: 1, KnowledgeBaseID: "kb", FeedbackRetrievalWeightEnabled: true,
			}},
		},
		PipelineState: types.PipelineState{
			MergeResult: []*types.SearchResult{
				{ID: "neutral", KnowledgeBaseID: "kb", Score: 0.90},
				{ID: "promoted", KnowledgeBaseID: "kb", Score: 0.80},
				{ID: "demoted", KnowledgeBaseID: "kb", Score: 0.95},
			},
		},
	}

	err := (&PluginFilterTopK{feedbackConfig: cfg, feedbackRepo: repo}).OnEvent(
		context.Background(), types.FILTER_TOP_K, chatManage,
		func() *PluginError { return nil },
	)

	require.Nil(t, err)
	assert.Equal(t, []string{"promoted", "neutral"}, searchResultIDs(chatManage.MergeResult))
	assert.Equal(t, 0.80, chatManage.MergeResult[0].Score, "raw score must remain observable")
	assert.Equal(t, 0.8, chatManage.MergeResult[0].StoredRecallWeight)
	assert.Equal(t, 1.2, chatManage.MergeResult[0].EffectiveRecallWeight)
	assert.True(t, chatManage.MergeResult[0].FeedbackWeightApplied)
	assert.Equal(t, 1, repo.calls)
}

func TestPluginFilterTopKEqualEffectiveScoresKeepBaselineRelevanceOrder(t *testing.T) {
	cfg := config.DefaultFeedbackConfig()
	cfg.RetrievalWeightEnabled = true
	repo := &filterFeedbackRepo{stats: []types.ChunkFeedbackStat{
		{
			ChunkFeedbackScope: types.ChunkFeedbackScope{
				TenantID: 1, KnowledgeBaseID: "kb", ChunkID: "higher",
			},
			LikeCount: 3, DislikeCount: 2, StoredRecallWeight: 1,
		},
		{
			ChunkFeedbackScope: types.ChunkFeedbackScope{
				TenantID: 1, KnowledgeBaseID: "kb", ChunkID: "lower",
			},
			LikeCount: 3, DislikeCount: 2, StoredRecallWeight: 1,
		},
	}}
	chatManage := &types.ChatManage{
		PipelineRequest: types.PipelineRequest{
			RerankTopK: 2,
			SearchTargets: types.SearchTargets{&types.SearchTarget{
				TenantID: 1, KnowledgeBaseID: "kb", FeedbackRetrievalWeightEnabled: true,
			}},
		},
		PipelineState: types.PipelineState{MergeResult: []*types.SearchResult{
			{ID: "lower", KnowledgeBaseID: "kb", Score: 0.8},
			{ID: "higher", KnowledgeBaseID: "kb", Score: 0.8},
		}},
	}

	err := (&PluginFilterTopK{feedbackConfig: cfg, feedbackRepo: repo}).OnEvent(
		context.Background(), types.FILTER_TOP_K, chatManage,
		func() *PluginError { return nil },
	)

	require.Nil(t, err)
	assert.Equal(t, []string{"higher", "lower"}, searchResultIDs(chatManage.MergeResult))
	assert.Equal(t, 1, repo.calls)
}

func TestPluginFilterTopKNonPositiveAndEmptyInputs(t *testing.T) {
	cfg := config.DefaultFeedbackConfig()
	repo := &filterFeedbackRepo{}
	chatManage := &types.ChatManage{
		PipelineRequest: types.PipelineRequest{RerankTopK: 0},
		PipelineState: types.PipelineState{MergeResult: []*types.SearchResult{
			{ID: "low", Score: 0.2},
			{ID: "high", Score: 0.9},
		}},
	}

	err := (&PluginFilterTopK{feedbackConfig: cfg, feedbackRepo: repo}).OnEvent(
		context.Background(), types.FILTER_TOP_K, chatManage,
		func() *PluginError { return nil },
	)
	require.Nil(t, err)
	assert.Equal(t, []string{"high", "low"}, searchResultIDs(chatManage.MergeResult))
	assert.Zero(t, repo.calls)

	empty := &types.ChatManage{
		PipelineRequest: types.PipelineRequest{RerankTopK: 3},
	}
	err = (&PluginFilterTopK{feedbackConfig: cfg, feedbackRepo: repo}).OnEvent(
		context.Background(), types.FILTER_TOP_K, empty,
		func() *PluginError { return nil },
	)
	require.Nil(t, err)
	assert.Empty(t, empty.MergeResult)
	assert.Zero(t, repo.calls)
}

func searchResultIDs(results []*types.SearchResult) []string {
	ids := make([]string, 0, len(results))
	for _, result := range results {
		ids = append(ids, result.ID)
	}
	return ids
}

func searchResultScoreBits(results []*types.SearchResult) []uint64 {
	bits := make([]uint64, 0, len(results))
	for _, result := range results {
		bits = append(bits, math.Float64bits(result.Score))
	}
	return bits
}
