package handler

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

func TestValidateFeedbackRankingConfigDefaultsPass(t *testing.T) {
	if err := validateFeedbackRankingConfig(&types.RetrievalConfig{}); err != nil {
		t.Fatalf("all-defaults config must validate, got: %v", err)
	}
}

func TestValidateFeedbackRankingConfigOrdering(t *testing.T) {
	// penalty > boost violates 0 <= needsOpt <= penalty <= boost <= 1.
	cfg := &types.RetrievalConfig{
		FeedbackBoostThreshold:   0.6,
		FeedbackPenaltyThreshold: 0.7,
	}
	if err := validateFeedbackRankingConfig(cfg); err == nil {
		t.Error("expected error when penalty threshold exceeds boost threshold")
	}

	// needsOpt > penalty (effective penalty default is 0.5).
	cfg = &types.RetrievalConfig{FeedbackNeedsOptimizationThreshold: 0.6}
	if err := validateFeedbackRankingConfig(cfg); err == nil {
		t.Error("expected error when needs-optimization threshold exceeds penalty threshold")
	}

	// boost > 1.
	cfg = &types.RetrievalConfig{FeedbackBoostThreshold: 1.2}
	if err := validateFeedbackRankingConfig(cfg); err == nil {
		t.Error("expected error when boost threshold exceeds 1")
	}
}

func TestValidateFeedbackRankingConfigFactors(t *testing.T) {
	cfg := &types.RetrievalConfig{FeedbackBoostFactor: 0.9}
	if err := validateFeedbackRankingConfig(cfg); err == nil {
		t.Error("expected error for boost factor below 1")
	}

	cfg = &types.RetrievalConfig{FeedbackPenaltyFactor: 1.5}
	if err := validateFeedbackRankingConfig(cfg); err == nil {
		t.Error("expected error for penalty factor above 1")
	}

	cfg = &types.RetrievalConfig{FeedbackMinSamples: -1}
	if err := validateFeedbackRankingConfig(cfg); err == nil {
		t.Error("expected error for negative min samples")
	}

	// Valid custom values pass.
	cfg = &types.RetrievalConfig{
		FeedbackBoostThreshold:             0.9,
		FeedbackPenaltyThreshold:           0.4,
		FeedbackNeedsOptimizationThreshold: 0.1,
		FeedbackBoostFactor:                1.3,
		FeedbackPenaltyFactor:              0.7,
		FeedbackMinSamples:                 5,
	}
	if err := validateFeedbackRankingConfig(cfg); err != nil {
		t.Errorf("valid custom config must pass, got: %v", err)
	}
}
