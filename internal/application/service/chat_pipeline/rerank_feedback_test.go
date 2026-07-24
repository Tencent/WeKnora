package chatpipeline

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

func TestFeedbackAdjustmentWithoutMetadata(t *testing.T) {
	sr := &types.SearchResult{Metadata: map[string]string{}}
	factor, rawBase := feedbackAdjustment(sr, 0.42)
	if factor != 1 || rawBase != 0.42 {
		t.Errorf("feedbackAdjustment without metadata = (%v, %v), want (1, 0.42)", factor, rawBase)
	}
}

func TestFeedbackAdjustmentUsesCarriedOriginalScore(t *testing.T) {
	sr := &types.SearchResult{Metadata: map[string]string{
		"feedback_factor":         "0.9000",
		"feedback_original_score": "0.500000",
	}}
	factor, rawBase := feedbackAdjustment(sr, 0.45)
	if factor != 0.9 || rawBase != 0.5 {
		t.Errorf("feedbackAdjustment = (%v, %v), want (0.9, 0.5)", factor, rawBase)
	}
}

func TestFeedbackAdjustmentFallsBackToDivision(t *testing.T) {
	sr := &types.SearchResult{Metadata: map[string]string{
		"feedback_factor": "0.9000",
	}}
	factor, rawBase := feedbackAdjustment(sr, 0.45)
	if factor != 0.9 {
		t.Errorf("factor = %v, want 0.9", factor)
	}
	if diff := rawBase - 0.5; diff > 1e-9 || diff < -1e-9 {
		t.Errorf("rawBase = %v, want ~0.5", rawBase)
	}
}

func TestFeedbackAdjustmentIgnoresInvalidFactor(t *testing.T) {
	for _, bad := range []string{"", "abc", "0", "-1", "1"} {
		sr := &types.SearchResult{Metadata: map[string]string{"feedback_factor": bad}}
		factor, rawBase := feedbackAdjustment(sr, 0.3)
		if factor != 1 || rawBase != 0.3 {
			t.Errorf("factor %q: feedbackAdjustment = (%v, %v), want (1, 0.3)", bad, factor, rawBase)
		}
	}
}

// The net effect contract: composite(rawBase) * factor equals a single
// application of the factor regardless of how the base was carried.
func TestFeedbackNetSingleApplication(t *testing.T) {
	sr := &types.SearchResult{Metadata: map[string]string{
		"feedback_factor":         "1.1000",
		"feedback_original_score": "0.400000",
	}}
	factor, rawBase := feedbackAdjustment(sr, 0.44)
	composite := 0.6*0.8 + 0.3*rawBase + 0.1*1.0 // mirrors compositeScore weights
	adjusted := composite * factor
	unweightedComposite := 0.6*0.8 + 0.3*0.4 + 0.1*1.0
	if diff := adjusted - unweightedComposite*1.1; diff > 1e-9 || diff < -1e-9 {
		t.Errorf("net application drifted: %v vs %v", adjusted, unweightedComposite*1.1)
	}
}
