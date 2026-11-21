// Package feedbackweight applies chunk approval-rate recall weights to a
// ranked result list. Both the standard QA rerank stage and the agent
// knowledge search tool use it.
package feedbackweight

import (
	"fmt"
	"sort"

	"github.com/Tencent/WeKnora/internal/types"
)

// Apply multiplies each attributable result's score by its chunk recall
// weight and re-sorts descending. Missing and non-positive weights stay at
// 1. Web and history hits are left alone. The returned count is the number
// of scores that changed.
func Apply(results []*types.SearchResult, weights map[string]float64) int {
	if len(results) == 0 || len(weights) == 0 {
		return 0
	}
	adjusted := 0
	for _, result := range results {
		if !types.AttributableKnowledgeRef(result) {
			continue
		}
		weight, ok := weights[result.ID]
		if !ok {
			continue
		}
		weight = types.EffectiveRecallWeight(weight)
		if weight == 1 {
			continue
		}
		if result.Metadata == nil {
			result.Metadata = map[string]string{}
		}
		result.Metadata["feedback_recall_weight"] = fmt.Sprintf("%.4f", weight)
		result.Score *= weight
		adjusted++
	}
	if adjusted == 0 {
		return 0
	}
	sort.SliceStable(results, func(i, j int) bool {
		if results[i] == nil || results[j] == nil {
			return results[i] != nil
		}
		return results[i].Score > results[j].Score
	})
	return adjusted
}

// WeightsByID collects recall weights for the chunks that were found.
func WeightsByID(chunks []*types.Chunk) map[string]float64 {
	weights := make(map[string]float64, len(chunks))
	for _, chunk := range chunks {
		if chunk == nil || chunk.ID == "" {
			continue
		}
		weights[chunk.ID] = types.EffectiveRecallWeight(chunk.RecallWeight)
	}
	return weights
}
