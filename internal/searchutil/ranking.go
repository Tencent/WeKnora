package searchutil

import (
	"math"
	"strings"
)

// Composite ranking weights shared by every retrieval surface that combines a
// rerank model score with the retrieval base score (chat pipeline and agent
// knowledge_search today).
const (
	CompositeModelWeight  = 0.6
	CompositeBaseWeight   = 0.3
	CompositeSourceWeight = 0.1
	// webSearchSourceWeight slightly down-weights web_search passages so
	// knowledge-base content wins ties against fetched web content.
	webSearchSourceWeight = 0.95
)

// CompositeRawScore returns the unclamped weighted combination
// CompositeModelWeight*modelScore + CompositeBaseWeight*baseScore +
// CompositeSourceWeight*sourceWeight, where web_search sources use
// webSearchSourceWeight and everything else weighs 1.0.
// Callers that multiply in an additional prior (the agent position prior)
// should clamp the product themselves so the clamp stays the final step.
func CompositeRawScore(knowledgeSource string, modelScore, baseScore float64) float64 {
	sourceWeight := 1.0
	if strings.ToLower(knowledgeSource) == "web_search" {
		sourceWeight = webSearchSourceWeight
	}
	return CompositeModelWeight*modelScore + CompositeBaseWeight*baseScore + CompositeSourceWeight*sourceWeight
}

// CompositeScore is CompositeRawScore clamped to [0, 1].
func CompositeScore(knowledgeSource string, modelScore, baseScore float64) float64 {
	return ClampFloat(CompositeRawScore(knowledgeSource, modelScore, baseScore), 0, 1)
}

// PositionPrior returns the multiplier that slightly favors chunks earlier in
// their document: 1 + clamp(1 - startAt/(endAt+1), -0.05, 0.05) for a valid
// span, and 1.0 otherwise. Applied by the agent knowledge_search tool only;
// whether it should spread to the chat pipeline is a maintainer decision.
func PositionPrior(startAt, endAt int) float64 {
	prior := 1.0
	if startAt >= 0 && endAt > startAt {
		positionRatio := 1.0 - float64(startAt)/float64(endAt+1)
		prior += ClampFloat(positionRatio, -0.05, 0.05)
	}
	return prior
}

// ApplyMMR selects up to k items by maximal marginal relevance:
//
//	mmr = lambda*score(item) - (1-lambda)*max Jaccard(item tokens, selected tokens)
//
// over pre-computed token sets, preserving candidate order on ties (the first
// candidate with the best MMR score wins each round). It returns the selected
// items in selection order plus the average pairwise Jaccard redundancy among
// them, so callers can log the same diversity summary they produced before
// this helper existed. tokenSets must be the same length as items.
//
// The selection uses the incremental maxRedundancy cache: each round folds the
// freshly selected item into every remaining candidate's cached maximum, which
// yields the same picks as rescanning all selected pairs each round.
func ApplyMMR[T any](items []T, score func(T) float64, tokenSets []map[string]struct{}, k int, lambda float64) (selected []T, avgRedundancy float64) {
	if k <= 0 || len(items) == 0 {
		return nil, 0
	}

	candidates := make([]T, len(items))
	copy(candidates, items)
	candidateTokens := make([]map[string]struct{}, len(items))
	copy(candidateTokens, tokenSets)
	maxRedundancy := make([]float64, len(items))

	selected = make([]T, 0, k)
	selectedTokens := make([]map[string]struct{}, 0, k)

	for len(selected) < k && len(candidates) > 0 {
		bestIdx := 0
		bestScore := -1.0

		for i, item := range candidates {
			mmr := lambda*score(item) - (1.0-lambda)*maxRedundancy[i]
			if mmr > bestScore {
				bestScore = mmr
				bestIdx = i
			}
		}

		selected = append(selected, candidates[bestIdx])
		chosenTokens := candidateTokens[bestIdx]
		selectedTokens = append(selectedTokens, chosenTokens)
		candidates = append(candidates[:bestIdx], candidates[bestIdx+1:]...)
		candidateTokens = append(candidateTokens[:bestIdx], candidateTokens[bestIdx+1:]...)
		maxRedundancy = append(maxRedundancy[:bestIdx], maxRedundancy[bestIdx+1:]...)

		// Fold the freshly selected item into every remaining candidate's cache.
		for i := range candidates {
			maxRedundancy[i] = math.Max(maxRedundancy[i], Jaccard(candidateTokens[i], chosenTokens))
		}
	}

	avgRedundancy = 0.0
	if len(selectedTokens) > 1 {
		pairs := 0
		for i := 0; i < len(selectedTokens); i++ {
			for j := i + 1; j < len(selectedTokens); j++ {
				avgRedundancy += Jaccard(selectedTokens[i], selectedTokens[j])
				pairs++
			}
		}
		if pairs > 0 {
			avgRedundancy /= float64(pairs)
		}
	}
	return selected, avgRedundancy
}
