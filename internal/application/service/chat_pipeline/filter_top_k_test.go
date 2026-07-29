package chatpipeline

import (
	"context"
	"math"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPluginFilterTopKSortsMergeResultsBeforeTruncation(t *testing.T) {
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

	plugin := &PluginFilterTopK{}
	err := plugin.OnEvent(
		context.Background(),
		types.FILTER_TOP_K,
		chatManage,
		func() *PluginError { return nil },
	)

	require.Nil(t, err)
	require.Len(t, chatManage.MergeResult, 3)
	assert.Equal(t, []string{"high", "second", "medium"}, searchResultIDs(chatManage.MergeResult))
}

func TestPluginFilterTopKUsesDeterministicTieBreakers(t *testing.T) {
	chatManage := &types.ChatManage{
		PipelineRequest: types.PipelineRequest{RerankTopK: 10},
		PipelineState: types.PipelineState{
			MergeResult: []*types.SearchResult{
				{ID: "chunk-b", KnowledgeID: "doc-b", ChunkType: "text", StartAt: 10, EndAt: 20, Score: 0.8},
				{ID: "chunk-c", KnowledgeID: "doc-a", ChunkType: "summary", StartAt: 0, EndAt: 10, Score: 0.8},
				{ID: "chunk-a", KnowledgeID: "doc-a", ChunkType: "text", StartAt: 0, EndAt: 10, Score: 0.8},
			},
		},
	}

	plugin := &PluginFilterTopK{}
	err := plugin.OnEvent(
		context.Background(),
		types.FILTER_TOP_K,
		chatManage,
		func() *PluginError { return nil },
	)

	require.Nil(t, err)
	assert.Equal(t, []string{"chunk-c", "chunk-a", "chunk-b"}, searchResultIDs(chatManage.MergeResult))
}

func TestPluginFilterTopKDisabledPreservesLegacyOrder(t *testing.T) {
	chatManage := &types.ChatManage{
		PipelineRequest: types.PipelineRequest{RerankTopK: 2},
		PipelineState: types.PipelineState{
			MergeResult: []*types.SearchResult{
				{ID: "neutral", KnowledgeID: "doc-a", Score: 0.90, RecallWeight: 1, FeedbackWeightEnabled: true},
				{ID: "promoted", KnowledgeID: "doc-b", Score: 0.80, RecallWeight: 1.2, FeedbackWeightEnabled: true},
				{ID: "demoted", KnowledgeID: "doc-c", Score: 0.95, RecallWeight: 0.8, FeedbackWeightEnabled: true},
			},
		},
	}

	err := (&PluginFilterTopK{}).OnEvent(
		context.Background(), types.FILTER_TOP_K, chatManage,
		func() *PluginError { return nil },
	)

	require.Nil(t, err)
	assert.Equal(t, []string{"demoted", "neutral"}, searchResultIDs(chatManage.MergeResult))
	assert.Equal(t, 0.95, chatManage.MergeResult[0].Score)
}

func TestPluginFilterTopKEnabledAppliesRecallWeightAtFinalCutoff(t *testing.T) {
	chatManage := &types.ChatManage{
		PipelineRequest: types.PipelineRequest{RerankTopK: 2},
		PipelineState: types.PipelineState{
			MergeResult: []*types.SearchResult{
				{ID: "neutral", KnowledgeID: "doc-a", Score: 0.90, RecallWeight: 1, FeedbackWeightEnabled: true},
				{ID: "promoted", KnowledgeID: "doc-b", Score: 0.80, RecallWeight: 1.2, FeedbackWeightEnabled: true},
				{ID: "demoted", KnowledgeID: "doc-c", Score: 0.95, RecallWeight: 0.8, FeedbackWeightEnabled: true},
			},
		},
	}

	err := (&PluginFilterTopK{retrievalWeightEnabled: true}).OnEvent(
		context.Background(), types.FILTER_TOP_K, chatManage,
		func() *PluginError { return nil },
	)

	require.Nil(t, err)
	assert.Equal(t, []string{"promoted", "neutral"}, searchResultIDs(chatManage.MergeResult))
	assert.Equal(t, 0.80, chatManage.MergeResult[0].Score, "raw score must remain observable")
}

func TestPluginFilterTopKRequiresKnowledgeBaseOptIn(t *testing.T) {
	chatManage := &types.ChatManage{
		PipelineRequest: types.PipelineRequest{RerankTopK: 2},
		PipelineState: types.PipelineState{
			MergeResult: []*types.SearchResult{
				{
					ID: "opted-out-promoted", KnowledgeID: "doc-a",
					Score: 0.80, RecallWeight: 1.2, FeedbackWeightEnabled: false,
				},
				{
					ID: "opted-in-neutral", KnowledgeID: "doc-b",
					Score: 0.90, RecallWeight: 1, FeedbackWeightEnabled: true,
				},
				{
					ID: "opted-in-demoted", KnowledgeID: "doc-c",
					Score: 0.95, RecallWeight: 0.8, FeedbackWeightEnabled: true,
				},
			},
		},
	}

	err := (&PluginFilterTopK{retrievalWeightEnabled: true}).OnEvent(
		context.Background(), types.FILTER_TOP_K, chatManage,
		func() *PluginError { return nil },
	)

	require.Nil(t, err)
	assert.Equal(
		t,
		[]string{"opted-in-neutral", "opted-out-promoted"},
		searchResultIDs(chatManage.MergeResult),
	)
}

func TestNormalizedRecallWeightTreatsMissingAndInvalidValuesAsNeutral(t *testing.T) {
	for _, weight := range []float64{0, -1, 0.79, 1.21, math.NaN(), math.Inf(1)} {
		assert.Equal(t, 1.0, normalizedRecallWeight(weight))
	}
	assert.Equal(t, 0.8, normalizedRecallWeight(0.8))
	assert.Equal(t, 1.2, normalizedRecallWeight(1.2))
}

func TestEntitySearchResultCarriesCanonicalScopeAndFeedbackOptIn(t *testing.T) {
	result := chunk2SearchResult(
		&types.Chunk{
			ID: "chunk-graph", TenantID: 42, KnowledgeID: "knowledge-graph",
			KnowledgeBaseID: "kb-graph", RecallWeight: 1.2,
		},
		&types.Knowledge{
			ID: "knowledge-graph", KnowledgeBaseID: "kb-graph",
		},
		true,
	)

	if result.TenantID != 42 || result.KnowledgeBaseID != "kb-graph" {
		t.Fatalf(
			"canonical scope = (%d, %q), want (42, kb-graph)",
			result.TenantID, result.KnowledgeBaseID,
		)
	}
	if !result.FeedbackWeightEnabled || result.RecallWeight != 1.2 {
		t.Fatalf(
			"feedback projection = (enabled=%v, weight=%v), want (true, 1.2)",
			result.FeedbackWeightEnabled, result.RecallWeight,
		)
	}
}

func searchResultIDs(results []*types.SearchResult) []string {
	ids := make([]string, 0, len(results))
	for _, result := range results {
		ids = append(ids, result.ID)
	}
	return ids
}
