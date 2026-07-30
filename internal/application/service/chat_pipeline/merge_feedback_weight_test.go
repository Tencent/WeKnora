package chatpipeline

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/types"
)

func feedbackWeightMergeInputs(reverse bool) []*types.SearchResult {
	first := &types.SearchResult{
		ID: "chunk-a", KnowledgeID: "knowledge-a", KnowledgeBaseID: "kb", ChunkType: string(types.ChunkTypeText),
		ChunkIndex: 0, Content: "alpha", Score: 0.70, StoredRecallWeight: 0.8,
	}
	second := &types.SearchResult{
		ID: "chunk-b", KnowledgeID: "knowledge-a", KnowledgeBaseID: "kb", ChunkType: string(types.ChunkTypeText),
		ChunkIndex: 1, Content: "beta", Score: 0.90, StoredRecallWeight: 1.2,
	}
	if reverse {
		return []*types.SearchResult{second, first}
	}
	return []*types.SearchResult{first, second}
}

func TestMergeKeepsScoreAndRecallWeightFromSameSource(t *testing.T) {
	for _, reverse := range []bool{false, true} {
		t.Run(map[bool]string{false: "forward", true: "reverse"}[reverse], func(t *testing.T) {
			merged := (&PluginMerge{}).groupAndMergeCurrentContent(
				context.Background(), feedbackWeightMergeInputs(reverse),
			)

			require.Len(t, merged, 1)
			assert.Equal(t, 0.90, merged[0].Score)
			assert.Equal(t, 1.2, merged[0].StoredRecallWeight)
			assert.Contains(t, merged[0].Content, "alpha")
			assert.Contains(t, merged[0].Content, "beta")
		})
	}
}

func TestContainedMergeKeepsScoreAndRecallWeightFromSameSource(t *testing.T) {
	merged := (&PluginMerge{}).mergeSequentialChunks(
		context.Background(),
		"knowledge-a",
		[]*types.SearchResult{
			{
				ID: "chunk-a", ChunkIndex: 0, Content: "alpha beta gamma delta epsilon",
				Score: 0.70, StoredRecallWeight: 0.8,
			},
			{
				ID: "chunk-b", ChunkIndex: 9, Content: "alpha beta gamma delta epsilon zeta",
				Score: 0.90, StoredRecallWeight: 1.2,
			},
		},
	)

	require.Len(t, merged, 1)
	assert.Equal(t, 0.90, merged[0].Score)
	assert.Equal(t, 1.2, merged[0].StoredRecallWeight)
}

func TestEqualScoreMergeKeepsDeterministicOriginalOwner(t *testing.T) {
	for _, reverse := range []bool{false, true} {
		inputs := feedbackWeightMergeInputs(reverse)
		for _, input := range inputs {
			input.Score = 0.80
		}

		merged := (&PluginMerge{}).groupAndMergeCurrentContent(context.Background(), inputs)

		require.Len(t, merged, 1)
		assert.Equal(t, "chunk-a", merged[0].ID)
		assert.Equal(t, 0.8, merged[0].StoredRecallWeight)
	}
}

func TestMergedScoreOwnerControlsFeedbackWeightedTopK(t *testing.T) {
	merged := (&PluginMerge{}).groupAndMergeCurrentContent(
		context.Background(), feedbackWeightMergeInputs(false),
	)
	require.Len(t, merged, 1)
	competitor := &types.SearchResult{
		ID: "competitor", KnowledgeID: "knowledge-b", KnowledgeBaseID: "kb", Score: 1.0, StoredRecallWeight: 1.0,
	}
	cfg := config.DefaultFeedbackConfig()
	cfg.RetrievalWeightEnabled = true
	repo := &filterFeedbackRepo{stats: []types.ChunkFeedbackStat{
		{ChunkFeedbackScope: types.ChunkFeedbackScope{
			TenantID: 1, KnowledgeBaseID: "kb", ChunkID: "chunk-a",
		}, LikeCount: 5, StoredRecallWeight: 1.2},
		{ChunkFeedbackScope: types.ChunkFeedbackScope{
			TenantID: 1, KnowledgeBaseID: "kb", ChunkID: "competitor",
		}, LikeCount: 3, DislikeCount: 2, StoredRecallWeight: 1},
	}}
	chatManage := &types.ChatManage{
		PipelineRequest: types.PipelineRequest{
			RerankTopK: 1,
			SearchTargets: types.SearchTargets{&types.SearchTarget{
				TenantID: 1, KnowledgeBaseID: "kb", FeedbackRetrievalWeightEnabled: true,
			}},
		},
		PipelineState: types.PipelineState{
			MergeResult: append(merged, competitor),
		},
	}

	err := (&PluginFilterTopK{feedbackConfig: cfg, feedbackRepo: repo}).OnEvent(
		context.Background(), types.FILTER_TOP_K, chatManage,
		func() *PluginError { return nil },
	)

	require.Nil(t, err)
	require.Len(t, chatManage.MergeResult, 1)
	assert.Equal(t, "chunk-a", chatManage.MergeResult[0].ID)
	assert.Equal(t, 0.90, chatManage.MergeResult[0].Score)
	assert.Equal(t, 1.2, chatManage.MergeResult[0].StoredRecallWeight)
}
