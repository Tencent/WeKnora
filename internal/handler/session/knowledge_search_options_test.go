package session

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/types"
)

// knowledge-search runs one search per scoped target and reranks every
// candidate, so result counts are bounded.
func TestKnowledgeSearchOptionsBoundsCounts(t *testing.T) {
	t.Parallel()
	_, err := knowledgeSearchOptions(&SearchKnowledgeRequest{MatchCount: types.MaxRequestedResults})
	require.NoError(t, err)

	_, err = knowledgeSearchOptions(&SearchKnowledgeRequest{MatchCount: types.MaxRequestedResults + 1})
	require.ErrorContains(t, err, "match_count must not exceed")

	_, err = knowledgeSearchOptions(&SearchKnowledgeRequest{
		Rerank: &types.RerankOptions{TopK: types.MaxRequestedResults + 1},
	})
	require.ErrorContains(t, err, "rerank.top_k must not exceed")
}
