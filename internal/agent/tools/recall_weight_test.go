package tools

import (
	"sort"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/types"
)

func TestApplyRecallWeightToAgentResultChangesFinalOrderingExactlyOnce(t *testing.T) {
	results := []*types.SearchResult{
		{ID: "raw-top", Score: 0.9, RecallWeight: 0.5},
		{ID: "boosted", Score: 0.5, RecallWeight: 2},
	}

	for _, result := range results {
		require.True(t, applyRecallWeightToAgentResult(result))
		require.False(t, applyRecallWeightToAgentResult(result), "weight must not be applied twice")
	}
	sort.SliceStable(results, func(i, j int) bool {
		return results[i].Score > results[j].Score
	})

	require.Equal(t, []string{"boosted", "raw-top"}, []string{results[0].ID, results[1].ID})
	require.Equal(t, 1.0, results[0].Score)
	require.Equal(t, "0.5000", results[0].Metadata["recall_weight_original_score"])
	require.Equal(t, "1.0000", results[0].Metadata["recall_weighted_score"])
}
