package tools

import (
	"fmt"

	"github.com/Tencent/WeKnora/internal/types"
)

// applyRecallWeightToAgentResult applies persisted feedback weight to an
// agent-visible final score exactly once. HybridSearch already used the same
// weight for candidate selection without mutating Score.
func applyRecallWeightToAgentResult(result *types.SearchResult) bool {
	if result == nil || result.RecallWeight == 0 || result.RecallWeight == 1 {
		return false
	}
	if result.Metadata == nil {
		result.Metadata = make(map[string]string)
	}
	if _, applied := result.Metadata["recall_weight_original_score"]; applied {
		return false
	}

	originalScore := result.Score
	result.Score *= result.RecallWeight
	result.Metadata["recall_weight"] = fmt.Sprintf("%.2f", result.RecallWeight)
	result.Metadata["recall_weight_original_score"] = fmt.Sprintf("%.4f", originalScore)
	result.Metadata["recall_weighted_score"] = fmt.Sprintf("%.4f", result.Score)
	return true
}
