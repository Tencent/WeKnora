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
	// The first pick has no redundancy term. Avoid passage cleaning and
	// tokenization when the caller only wants that pick.
	if k == 1 {
		best, bestScore := 0, math.Inf(-1)
		for i, r := range results {
			if score := lambda*r.Score - (1.0-lambda)*0; score > bestScore {
				best, bestScore = i, score
			}
		}
		return []int{best}
	}

	remaining := make([]int, len(results))
	for i := range results {
		remaining[i] = i
	}
	tokens, offsets, postings := buildMMRPostings(ctx, results)

	// Cache each candidate's maximum Jaccard against the selected passages.
	// Postings count intersections only for candidates sharing a token with
	// the new pick; disjoint passages have zero redundancy and need no update.
	maxRedundancy := make([]float64, len(results))
	intersections := make([]int, len(results))
	taken := make([]bool, len(results))
	touched := make([]int, 0, len(results))
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
		taken[chosen] = true
		remaining = append(remaining[:bestPos], remaining[bestPos+1:]...)
		if len(selected) == k || len(remaining) == 0 {
			break
		}
		touched = touched[:0]
		for _, token := range tokens[chosen] {
			for _, i := range postings[offsets[token]:offsets[token+1]] {
				if taken[i] {
					continue
				}
				if intersections[i] == 0 {
					touched = append(touched, i)
				}
				intersections[i]++
			}
		}
		for _, i := range touched {
			intersection := intersections[i]
			union := len(tokens[i]) + len(tokens[chosen]) - intersection
			maxRedundancy[i] = math.Max(maxRedundancy[i], float64(intersection)/float64(union))
			intersections[i] = 0
		}
	}
	return selected
}

// buildMMRPostings interns each unique token and stores its candidate IDs in
// one flat array. offsets[t]:offsets[t+1] is token t's posting list. Each row
// comes from a token set, so one shared token contributes exactly once to a
// pair's intersection, regardless of its frequency in either passage.
func buildMMRPostings(
	ctx context.Context, results []*types.SearchResult,
) (tokens [][]int, offsets, postings []int) {
	termIDs := make(map[string]int)
	var counts []int
	tokens = make([][]int, len(results))
	for i, r := range results {
		set := searchutil.TokenizeSimple(EnrichedPassage(ctx, r))
		tokens[i] = make([]int, 0, len(set))
		for term := range set {
			id, exists := termIDs[term]
			if !exists {
				id = len(counts)
				termIDs[term] = id
				counts = append(counts, 0)
			}
			counts[id]++
			tokens[i] = append(tokens[i], id)
		}
	}
	offsets = make([]int, len(counts)+1)
	for id, count := range counts {
		offsets[id+1] = offsets[id] + count
	}
	postings = make([]int, offsets[len(counts)])
	// Reuse counts as insertion cursors; offsets stay unchanged for lookups.
	copy(counts, offsets)
	for i, row := range tokens {
		for _, id := range row {
			postings[counts[id]] = i
			counts[id]++
		}
	}
	return tokens, offsets, postings
}
