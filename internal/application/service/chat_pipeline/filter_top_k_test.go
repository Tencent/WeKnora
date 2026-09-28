package chatpipeline

import (
	"context"
	"fmt"
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

// Truncation is the evidence that the prompt context is a subset: it must be
// recorded exactly when results were dropped.
func TestPluginFilterTopKRecordsTruncation(t *testing.T) {
	chatManage := &types.ChatManage{
		PipelineRequest: types.PipelineRequest{RerankTopK: 2},
		PipelineState: types.PipelineState{
			MergeResult: []*types.SearchResult{
				{ID: "first", KnowledgeID: "doc-a", Score: 0.9},
				{ID: "second", KnowledgeID: "doc-b", Score: 0.8},
				{ID: "third", KnowledgeID: "doc-c", Score: 0.7},
				{ID: "fourth", KnowledgeID: "doc-d", Score: 0.6},
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
	require.Len(t, chatManage.MergeResult, 2)
	require.NotNil(t, chatManage.Truncation)
	assert.Equal(t, types.RetrievalTruncation{Shown: 2, Candidates: 4}, *chatManage.Truncation)
}

// A prompt that saw every candidate must not carry the subset caveat, so the
// field stays nil when the result count is at or below topK.
func TestPluginFilterTopKLeavesTruncationNilWhenNothingDropped(t *testing.T) {
	for _, topK := range []int{2, 5} {
		t.Run(fmt.Sprintf("topK=%d", topK), func(t *testing.T) {
			chatManage := &types.ChatManage{
				PipelineRequest: types.PipelineRequest{RerankTopK: topK},
				PipelineState: types.PipelineState{
					MergeResult: []*types.SearchResult{
						{ID: "first", KnowledgeID: "doc-a", Score: 0.9},
						{ID: "second", KnowledgeID: "doc-b", Score: 0.8},
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
			require.Len(t, chatManage.MergeResult, 2)
			assert.Nil(t, chatManage.Truncation)
		})
	}
}

func searchResultIDs(results []*types.SearchResult) []string {
	ids := make([]string, 0, len(results))
	for _, result := range results {
		ids = append(ids, result.ID)
	}
	return ids
}
