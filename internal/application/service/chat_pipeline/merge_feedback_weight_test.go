package chatpipeline

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/types"
)

func feedbackWeightMergeInputs(reverse bool) []*types.SearchResult {
	first := &types.SearchResult{
		ID: "chunk-a", KnowledgeID: "knowledge-a", ChunkType: string(types.ChunkTypeText),
		ChunkIndex: 0, Content: "alpha", Score: 0.70, RecallWeight: 0.8, FeedbackWeightEnabled: true,
	}
	second := &types.SearchResult{
		ID: "chunk-b", KnowledgeID: "knowledge-a", ChunkType: string(types.ChunkTypeText),
		ChunkIndex: 1, Content: "beta", Score: 0.90, RecallWeight: 1.2, FeedbackWeightEnabled: true,
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
			assert.Equal(t, 1.2, merged[0].RecallWeight)
			assert.True(t, merged[0].FeedbackWeightEnabled)
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
				Score: 0.70, RecallWeight: 0.8, FeedbackWeightEnabled: true,
			},
			{
				ID: "chunk-b", ChunkIndex: 9, Content: "alpha beta gamma delta epsilon zeta",
				Score: 0.90, RecallWeight: 1.2, FeedbackWeightEnabled: true,
			},
		},
	)

	require.Len(t, merged, 1)
	assert.Equal(t, 0.90, merged[0].Score)
	assert.Equal(t, 1.2, merged[0].RecallWeight)
	assert.True(t, merged[0].FeedbackWeightEnabled)
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
		assert.Equal(t, 0.8, merged[0].RecallWeight)
	}
}

func TestMergedScoreOwnerControlsFeedbackWeightedTopK(t *testing.T) {
	merged := (&PluginMerge{}).groupAndMergeCurrentContent(
		context.Background(), feedbackWeightMergeInputs(false),
	)
	require.Len(t, merged, 1)
	competitor := &types.SearchResult{
		ID: "competitor", KnowledgeID: "knowledge-b", Score: 1.0, RecallWeight: 1.0,
	}
	chatManage := &types.ChatManage{
		PipelineRequest: types.PipelineRequest{RerankTopK: 1},
		PipelineState: types.PipelineState{
			MergeResult: append(merged, competitor),
		},
	}

	err := (&PluginFilterTopK{retrievalWeightEnabled: true}).OnEvent(
		context.Background(), types.FILTER_TOP_K, chatManage,
		func() *PluginError { return nil },
	)

	require.Nil(t, err)
	require.Len(t, chatManage.MergeResult, 1)
	assert.Equal(t, "chunk-a", chatManage.MergeResult[0].ID)
	assert.Equal(t, 0.90, chatManage.MergeResult[0].Score)
	assert.Equal(t, 1.2, chatManage.MergeResult[0].RecallWeight)
}
