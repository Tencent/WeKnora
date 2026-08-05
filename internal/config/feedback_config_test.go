package config

import (
	"math"
	"testing"
)

func validFeedbackConfig() *FeedbackConfig {
	return &FeedbackConfig{
		MinimumSampleCount:    5,
		OptimizationThreshold: 0.3,
	}
}

func TestValidateConfigAcceptsFeedbackPolicyBoundaries(t *testing.T) {
	feedback := validFeedbackConfig()
	feedback.OptimizationThreshold = 0

	if err := ValidateConfig(&Config{Feedback: feedback}); err != nil {
		t.Fatalf("valid feedback policy rejected: %v", err)
	}
	feedback.OptimizationThreshold = 1
	if err := ValidateConfig(&Config{Feedback: feedback}); err != nil {
		t.Fatalf("valid upper feedback threshold rejected: %v", err)
	}
}

func TestValidateConfigRejectsInvalidFeedbackPolicy(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*FeedbackConfig)
	}{
		{
			name: "minimum sample count",
			mutate: func(cfg *FeedbackConfig) {
				cfg.MinimumSampleCount = 0
			},
		},
		{
			name: "negative optimization threshold",
			mutate: func(cfg *FeedbackConfig) {
				cfg.OptimizationThreshold = -0.1
			},
		},
		{
			name: "optimization above one",
			mutate: func(cfg *FeedbackConfig) {
				cfg.OptimizationThreshold = 1.1
			},
		},
		{
			name: "nan threshold",
			mutate: func(cfg *FeedbackConfig) {
				cfg.OptimizationThreshold = math.NaN()
			},
		},
		{
			name: "infinite threshold",
			mutate: func(cfg *FeedbackConfig) {
				cfg.OptimizationThreshold = math.Inf(1)
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			feedback := validFeedbackConfig()
			test.mutate(feedback)
			if err := ValidateConfig(&Config{Feedback: feedback}); err == nil {
				t.Fatal("ValidateConfig unexpectedly accepted an invalid feedback policy")
			}
		})
	}
}
