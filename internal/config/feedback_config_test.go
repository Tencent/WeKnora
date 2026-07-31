package config

import (
	"math"
	"testing"
)

func validFeedbackConfig() *FeedbackConfig {
	return &FeedbackConfig{
		MinimumSampleCount:    5,
		OptimizationThreshold: 0.3,
		LowThreshold:          0.5,
		HighThreshold:         0.8,
		LowWeight:             0.8,
		NormalWeight:          1,
		HighWeight:            1.2,
	}
}

func TestValidateConfigAcceptsFeedbackPolicyBoundaries(t *testing.T) {
	feedback := validFeedbackConfig()
	feedback.OptimizationThreshold = 0
	feedback.LowThreshold = 0
	feedback.HighThreshold = 1
	feedback.LowWeight = 1
	feedback.NormalWeight = 1
	feedback.HighWeight = 1

	if err := ValidateConfig(&Config{Feedback: feedback}); err != nil {
		t.Fatalf("valid feedback policy rejected: %v", err)
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
			name: "optimization above low threshold",
			mutate: func(cfg *FeedbackConfig) {
				cfg.OptimizationThreshold = 0.6
			},
		},
		{
			name: "low threshold above high threshold",
			mutate: func(cfg *FeedbackConfig) {
				cfg.LowThreshold = 0.9
			},
		},
		{
			name: "high threshold above one",
			mutate: func(cfg *FeedbackConfig) {
				cfg.HighThreshold = 1.1
			},
		},
		{
			name: "non-positive low weight",
			mutate: func(cfg *FeedbackConfig) {
				cfg.LowWeight = 0
			},
		},
		{
			name: "low weight above normal weight",
			mutate: func(cfg *FeedbackConfig) {
				cfg.LowWeight = 1.1
			},
		},
		{
			name: "normal weight above high weight",
			mutate: func(cfg *FeedbackConfig) {
				cfg.NormalWeight = 1.3
			},
		},
		{
			name: "nan threshold",
			mutate: func(cfg *FeedbackConfig) {
				cfg.LowThreshold = math.NaN()
			},
		},
		{
			name: "infinite weight",
			mutate: func(cfg *FeedbackConfig) {
				cfg.HighWeight = math.Inf(1)
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
