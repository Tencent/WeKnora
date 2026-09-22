package searchutil

import "math"

// ranking.go is the single implementation of the post-rerank ranking core
// shared by the chat pipeline and the agent knowledge-search tool. Both sides
// used to carry their own compositeScore/applyMMR pair; the formulas are kept
// bit-identical to those originals, with the one behavioral difference between
// them — the agent's position prior — exposed here instead of buried in a
// private copy. Callers keep their own tokenization strategy and logging.

// CompositeScoreRaw is the unclamped weighted core both pipelines agree on:
// 0.6*modelScore + 0.3*baseScore + 0.1*sourceWeight, where web_search results
// carry a 0.95 source weight. The chat pipeline clamps this value directly;
// the agent pipeline multiplies in PositionPrior first and clamps after —
// which is why the clamp deliberately lives with the callers.
func CompositeScoreRaw(modelScore, baseScore float64, knowledgeSource string) float64 {
	sourceWeight := 1.0
	if isWebSearchSource(knowledgeSource) {
		sourceWeight = 0.95
	}
	return 0.6*modelScore + 0.3*baseScore + 0.1*sourceWeight
}

func isWebSearchSource(knowledgeSource string) bool {
	return lowerASCII(knowledgeSource) == "web_search"
}

// lowerASCII avoids importing strings for one comparison. KnowledgeSource is
// produced by WeKnora itself, so non-ASCII case folding is not a concern.
func lowerASCII(value string) string {
	changed := false
	bytes := []byte(value)
	for i, b := range bytes {
		if b >= 'A' && b <= 'Z' {
			bytes[i] = b + ('a' - 'A')
			changed = true
		}
	}
	if !changed {
		return value
	}
	return string(bytes)
}

// PositionPrior returns the agent pipeline's document-position multiplier:
// chunks earlier in the document get up to +0.05, later ones down to -0.05.
// An invalid or empty range yields the neutral 1.0. Whether the chat pipeline
// should adopt this factor is a maintainer decision; until then it stays an
// explicit, centrally-documented agent-side input.
func PositionPrior(startAt, endAt int) float64 {
	if startAt < 0 || endAt <= startAt {
		return 1.0
	}
	positionRatio := 1.0 - float64(startAt)/float64(endAt+1)
	return 1.0 + ClampFloat(positionRatio, -0.05, 0.05)
}

// ApplyMMR selects up to k items by Maximal Marginal Relevance using
// pre-computed token sets, returning the selection and the average pairwise
// redundancy among it (the chat pipeline logs that value; the agent tool
// currently ignores it). The incremental maxRedundancy cache makes each round
// cost one comparison per remaining candidate while producing exactly the
// same selection — including tie-breaking — as the naive per-round rescan.
//
// scoreOf is consulted once per surviving candidate per round, so it must be
// cheap or cached by the caller.
func ApplyMMR[T any](
	items []T,
	tokenSets []map[string]struct{},
	k int,
	lambda float64,
	scoreOf func(T) float64,
) (selected []T, avgRedundancy float64) {
	if k <= 0 || len(items) == 0 || len(tokenSets) < len(items) {
		return nil, 0
	}

	selected = make([]T, 0, k)
	selectedIdx := make([]int, 0, k)
	candidates := make([]int, len(items))
	for i := range candidates {
		candidates[i] = i
	}
	maxRedundancy := make([]float64, len(items))

	for len(selected) < k && len(candidates) > 0 {
		bestIdx := 0
		bestScore := -1.0
		for pos, i := range candidates {
			mmr := lambda*scoreOf(items[i]) - (1.0-lambda)*maxRedundancy[i]
			if mmr > bestScore {
				bestScore = mmr
				bestIdx = pos
			}
		}
		chosen := candidates[bestIdx]
		selected = append(selected, items[chosen])
		selectedIdx = append(selectedIdx, chosen)
		candidates = append(candidates[:bestIdx], candidates[bestIdx+1:]...)
		for _, i := range candidates {
			maxRedundancy[i] = math.Max(maxRedundancy[i], Jaccard(tokenSets[i], tokenSets[chosen]))
		}
	}

	if count := len(selectedIdx); count > 1 {
		pairs := 0
		for i := 0; i < count; i++ {
			for j := i + 1; j < count; j++ {
				avgRedundancy += Jaccard(tokenSets[selectedIdx[i]], tokenSets[selectedIdx[j]])
				pairs++
			}
		}
		if pairs > 0 {
			avgRedundancy /= float64(pairs)
		}
	}
	return selected, avgRedundancy
}
