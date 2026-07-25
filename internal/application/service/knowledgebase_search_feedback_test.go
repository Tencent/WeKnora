package service

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestRankPrimaryChunksByFeedbackAppliesWeightBeforeTopK(t *testing.T) {
	candidates := []*types.IndexWithScore{
		{ChunkID: "high-raw", Score: 0.9},
		{ChunkID: "boosted", Score: 0.7},
	}
	chunks := map[string]*types.Chunk{
		"high-raw": {ID: "high-raw", RecallWeight: 0.8},
		"boosted":  {ID: "boosted", RecallWeight: 1.2},
	}

	ranked := rankPrimaryChunksByFeedback(candidates, chunks, 1)

	require.Len(t, ranked, 1)
	require.Equal(t, "boosted", ranked[0].ChunkID)
	require.Equal(t, []string{"high-raw", "boosted"}, []string{candidates[0].ChunkID, candidates[1].ChunkID},
		"ranking must not mutate shared retrieval slices")
}

func TestBuildSearchResultCarriesFeedbackWeight(t *testing.T) {
	chunk := &types.Chunk{
		ID:              "chunk-1",
		KnowledgeID:     "knowledge-1",
		KnowledgeBaseID: "kb-1",
		Content:         "content",
		RecallWeight:    1.2,
	}
	knowledge := &types.Knowledge{ID: "knowledge-1", KnowledgeBaseID: "kb-1"}

	result := (&knowledgeBaseService{}).buildSearchResult(
		chunk,
		knowledge,
		0.5,
		types.MatchTypeEmbedding,
		"",
	)

	require.InDelta(t, 0.6, result.Score, 1e-9)
	require.Equal(t, 1.2, result.RecallWeight)
}
