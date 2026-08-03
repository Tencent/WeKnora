package types

import "testing"

// This file exists to lock in the API contract for the feedback policy
// accessors. They are covered more thoroughly in message_feedback_test.go
// (types_test), but a bare-bones smoke test in the internal package keeps
// the file structure consistent with neighbouring tests in case the
// external test runner picks up this file directly.

func TestPolicyAccessorDefaults(t *testing.T) {
	cfg := &RetrievalConfig{}
	if cfg.GetEffectiveFeedbackRankingEnabled() {
		t.Fatal("default ranking should be disabled")
	}
	if cfg.GetEffectiveFeedbackMinSamples() != 3 {
		t.Fatalf("min-samples default = %d, want 3", cfg.GetEffectiveFeedbackMinSamples())
	}
}