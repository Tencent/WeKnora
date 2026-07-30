package feedbackweight

import (
	"fmt"
	"math"

	"github.com/Tencent/WeKnora/internal/config"
)

// EffectiveEnabled is the single enablement contract shared by every
// retrieval adapter. A nil global policy is always retrieval-disabled.
func EffectiveEnabled(global *config.FeedbackConfig, workspaceOptIn bool) bool {
	return global != nil &&
		global.Enabled &&
		global.RetrievalWeightEnabled &&
		workspaceOptIn
}

// EffectiveWeight derives the retrieval-time multiplier from current counts.
// Stored recall_weight is deliberately not an input.
func EffectiveWeight(global *config.FeedbackConfig, likes, dislikes int64) (float64, string, error) {
	if global == nil {
		return 1, "disabled", nil
	}
	if err := global.Validate(); err != nil {
		return 1, "invalid_data", err
	}
	if likes < 0 || dislikes < 0 {
		return 1, "invalid_data", fmt.Errorf("feedback counts must be non-negative")
	}
	total := likes + dislikes
	if total < likes || total < dislikes {
		return 1, "invalid_data", fmt.Errorf("feedback count overflow")
	}
	if total == 0 {
		return 1, "no_feedback", nil
	}
	if total < global.MinimumSampleCount {
		return 1, "minimum_sample_count", nil
	}
	rate := float64(likes) / float64(total)
	var (
		weight float64
		tier   string
	)
	switch {
	case rate >= global.HighRateThreshold:
		weight, tier = global.HighRecallWeight, "high"
	case rate < global.LowRateThreshold:
		weight, tier = global.LowRecallWeight, "low"
	default:
		weight, tier = global.NormalRecallWeight, "normal"
	}
	if math.IsNaN(weight) || math.IsInf(weight, 0) || weight <= 0 {
		return 1, "invalid_data", fmt.Errorf("effective recall weight must be finite and positive")
	}
	return weight, tier, nil
}
