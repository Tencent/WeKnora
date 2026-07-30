package tools

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

type toolFeedbackRepo struct {
	interfaces.FeedbackRepository
	calls int
	stats []types.ChunkFeedbackStat
}

func (r *toolFeedbackRepo) ListChunkFeedbackStats(
	_ context.Context, _ []types.ChunkFeedbackScope,
) ([]types.ChunkFeedbackStat, error) {
	r.calls++
	return r.stats, nil
}

func toolFeedbackConfig(enabled bool) *config.Config {
	feedback := config.DefaultFeedbackConfig()
	feedback.RetrievalWeightEnabled = enabled
	return &config.Config{Feedback: feedback}
}

func toolFeedbackTargets(optIn bool) types.SearchTargets {
	return types.SearchTargets{&types.SearchTarget{
		TenantID: 1, KnowledgeBaseID: "kb", FeedbackRetrievalWeightEnabled: optIn,
	}}
}

func toolFeedbackStats() []types.ChunkFeedbackStat {
	return []types.ChunkFeedbackStat{
		{
			ChunkFeedbackScope: types.ChunkFeedbackScope{
				TenantID: 1, KnowledgeBaseID: "kb", ChunkID: "promoted",
			},
			LikeCount: 5, StoredRecallWeight: 0.8,
		},
		{
			ChunkFeedbackScope: types.ChunkFeedbackScope{
				TenantID: 1, KnowledgeBaseID: "kb", ChunkID: "neutral",
			},
			LikeCount: 3, DislikeCount: 2, StoredRecallWeight: 1,
		},
	}
}

func TestKnowledgeAndGrepUseSameFeedbackPolicy(t *testing.T) {
	repo := &toolFeedbackRepo{stats: toolFeedbackStats()}
	cfg := toolFeedbackConfig(true)
	targets := toolFeedbackTargets(true)

	knowledge := &KnowledgeSearchTool{
		config: cfg, feedbackRepo: repo, searchTargets: targets,
	}
	knowledgeResults := []*searchResultWithMeta{
		{SearchResult: &types.SearchResult{ID: "neutral", KnowledgeBaseID: "kb", Score: 0.9}},
		{SearchResult: &types.SearchResult{ID: "promoted", KnowledgeBaseID: "kb", Score: 0.8}},
	}
	weightedKnowledge := knowledge.applyFeedbackWeights(context.Background(), knowledgeResults, 1)
	require.Equal(t, "promoted", weightedKnowledge[0].ID)
	require.Equal(t, 1.2, weightedKnowledge[0].EffectiveRecallWeight)

	grep := &GrepChunksTool{
		feedbackConfig: cfg.Feedback, feedbackRepo: repo, searchTargets: targets,
	}
	grepResults := []chunkWithTitle{
		{Chunk: types.Chunk{ID: "neutral", KnowledgeBaseID: "kb"}, MatchScore: 0.9},
		{Chunk: types.Chunk{ID: "promoted", KnowledgeBaseID: "kb"}, MatchScore: 0.8},
	}
	weightedGrep := grep.applyFeedbackWeights(context.Background(), grepResults, 1)
	require.Equal(t, "promoted", weightedGrep[0].ID)
	require.Equal(t, 1.2, weightedGrep[0].EffectiveRecallWeight)
	require.Equal(t, 2, repo.calls)
}

func TestKnowledgeAndGrepDisabledNeverQueryFeedback(t *testing.T) {
	repo := &toolFeedbackRepo{}
	cfg := toolFeedbackConfig(false)
	targets := toolFeedbackTargets(true)

	knowledge := &KnowledgeSearchTool{
		config: cfg, feedbackRepo: repo, searchTargets: targets,
	}
	originalKnowledge := []*searchResultWithMeta{
		{SearchResult: &types.SearchResult{ID: "first", KnowledgeBaseID: "kb", Score: 0.1}},
		{SearchResult: &types.SearchResult{ID: "second", KnowledgeBaseID: "kb", Score: 0.9}},
	}
	require.Equal(
		t,
		"first",
		knowledge.applyFeedbackWeights(context.Background(), originalKnowledge, 1)[0].ID,
	)

	grep := &GrepChunksTool{
		feedbackConfig: cfg.Feedback, feedbackRepo: repo, searchTargets: targets,
	}
	originalGrep := []chunkWithTitle{
		{Chunk: types.Chunk{ID: "first", KnowledgeBaseID: "kb"}, MatchScore: 0.1},
		{Chunk: types.Chunk{ID: "second", KnowledgeBaseID: "kb"}, MatchScore: 0.9},
	}
	require.Equal(t, "first", grep.applyFeedbackWeights(context.Background(), originalGrep, 1)[0].ID)
	require.Zero(t, repo.calls)
}

func TestKnowledgeFeedbackWeightIsAppliedOnlyOnce(t *testing.T) {
	repo := &toolFeedbackRepo{stats: toolFeedbackStats()}
	cfg := toolFeedbackConfig(true)
	tool := &KnowledgeSearchTool{
		config: cfg, feedbackRepo: repo, searchTargets: toolFeedbackTargets(true),
	}
	results := []*searchResultWithMeta{
		{SearchResult: &types.SearchResult{ID: "neutral", KnowledgeBaseID: "kb", Score: 0.9}},
		{SearchResult: &types.SearchResult{ID: "promoted", KnowledgeBaseID: "kb", Score: 0.8}},
	}
	first := tool.applyFeedbackWeights(context.Background(), results, 1)
	second := tool.applyFeedbackWeights(context.Background(), first, 1)
	require.Equal(t, "promoted", second[0].ID)
	require.Equal(t, 1.2, second[0].EffectiveRecallWeight)
	require.Equal(t, 1, repo.calls)
}
