package reranking

import (
	"context"
	"math"

	"github.com/Tencent/WeKnora/internal/searchutil"
	"github.com/Tencent/WeKnora/internal/types"
)

// DefaultMMRLambda weighs relevance against redundancy in SelectMMR.
const DefaultMMRLambda = 0.7

// SelectMMR picks up to k results by Maximal Marginal Relevance over their
// Score and the token overlap of their enriched passages, and returns the
// indices of the picks in selection order. Ties go to the earlier result.
func SelectMMR(ctx context.Context, results []*types.SearchResult, k int, lambda float64) []int {
	if k <= 0 || len(results) == 0 {
		return nil
	}

	remaining := make([]int, len(results))
	tokenSets := make([]map[string]struct{}, len(results))
	for i, r := range results {
		remaining[i] = i
		tokenSets[i] = searchutil.TokenizeSimple(EnrichedPassage(ctx, r))
	}

	// Incremental form: maxRedundancy[i] caches result i's maximum jaccard
	// against everything selected so far, so each round only needs one
	// comparison per remaining result.
	maxRedundancy := make([]float64, len(results))
	selected := make([]int, 0, min(k, len(results)))
	for len(selected) < k && len(remaining) > 0 {
		bestPos := 0
		bestScore := math.Inf(-1)
		for pos, i := range remaining {
			mmr := lambda*results[i].Score - (1.0-lambda)*maxRedundancy[i]
			if mmr > bestScore {
				bestScore = mmr
				bestPos = pos
			}
		}
		chosen := remaining[bestPos]
		selected = append(selected, chosen)
		remaining = append(remaining[:bestPos], remaining[bestPos+1:]...)
		for _, i := range remaining {
			maxRedundancy[i] = math.Max(maxRedundancy[i], searchutil.Jaccard(tokenSets[i], tokenSets[chosen]))
		}
	}
	return selected
}
