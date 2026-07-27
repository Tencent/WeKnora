package types_test

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

func TestFeedbackCounterDeltas(t *testing.T) {
	cases := []struct {
		name                    string
		oldEffective            string
		newRating               string
		wantLike, wantDislike   int
	}{
		{"none-to-like", "", "like", 1, 0},
		{"none-to-dislike", "", "dislike", 0, 1},
		{"like-to-dislike", "like", "dislike", -1, 1},
		{"dislike-to-like", "dislike", "like", 1, -1},
		{"like-to-none", "like", "", -1, 0},
		{"dislike-to-none", "dislike", "", 0, -1},
		{"same-like", "like", "like", 0, 0},
		{"same-dislike", "dislike", "dislike", 0, 0},
		{"none-to-none", "", "", 0, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotLike, gotDislike := types.FeedbackCounterDeltas(tc.oldEffective, tc.newRating)
			if gotLike != tc.wantLike || gotDislike != tc.wantDislike {
				t.Fatalf("got (%d, %d), want (%d, %d)", gotLike, gotDislike, tc.wantLike, tc.wantDislike)
			}
		})
	}
}

func TestComputeRecallWeight(t *testing.T) {
	cfg := &types.RetrievalConfig{}

	t.Run("below-min-samples-stays-neutral", func(t *testing.T) {
		w, needs := types.ComputeRecallWeight(2, 0, cfg)
		if w != 1.0 || needs {
			t.Fatalf("got (w=%v, needs=%v), want (1.0, false)", w, needs)
		}
	})

	t.Run("high-positive-boosts", func(t *testing.T) {
		w, needs := types.ComputeRecallWeight(10, 1, cfg)
		if w != cfg.GetEffectiveFeedbackBoostFactor() || needs {
			t.Fatalf("got (w=%v, needs=%v), want (%v, false)", w, needs, cfg.GetEffectiveFeedbackBoostFactor())
		}
	})

	t.Run("mid-positive-neutral", func(t *testing.T) {
		w, needs := types.ComputeRecallWeight(6, 4, cfg)
		if w != 1.0 || needs {
			t.Fatalf("got (w=%v, needs=%v), want (1.0, false)", w, needs)
		}
	})

	t.Run("low-positive-penalises-and-flags", func(t *testing.T) {
		w, needs := types.ComputeRecallWeight(1, 9, cfg)
		if w != cfg.GetEffectiveFeedbackPenaltyFactor() {
			t.Fatalf("got (w=%v, needs=%v), want penalty=%v", w, needs, cfg.GetEffectiveFeedbackPenaltyFactor())
		}
		if !needs {
			t.Fatal("needs-optimisation should be true for a 1/10 chunk")
		}
	})

	t.Run("zero-feedback-is-never-needs-optimisation", func(t *testing.T) {
		_, needs := types.ComputeRecallWeight(0, 0, cfg)
		if needs {
			t.Fatal("zero-feedback chunks must not be flagged")
		}
	})
}

func TestPositiveRateOf(t *testing.T) {
	if got := types.PositiveRateOf(0, 0); got != 0 {
		t.Fatalf("empty: got %v, want 0", got)
	}
	if got := types.PositiveRateOf(3, 1); got != 0.75 {
		t.Fatalf("3/4: got %v, want 0.75", got)
	}
}

func TestWeightApproximatelyEqual(t *testing.T) {
	if !types.WeightApproximatelyEqual(1.0, 1.0+1e-12) {
		t.Fatal("tiny epsilon should be considered equal")
	}
	if types.WeightApproximatelyEqual(1.0, 1.01) {
		t.Fatal("non-trivial difference should not be considered equal")
	}
}

func TestValidateFeedbackPolicy(t *testing.T) {
	good := &types.RetrievalConfig{}
	if err := good.ValidateFeedbackPolicy(); err != nil {
		t.Fatalf("default config should validate, got: %v", err)
	}

	bad := &types.RetrievalConfig{}
	bad.FeedbackBoostThreshold = 0.1
	bad.FeedbackPenaltyThreshold = 0.8
	if err := bad.ValidateFeedbackPolicy(); err == nil {
		t.Fatal("boost<penalty must error")
	}

	bad2 := &types.RetrievalConfig{}
	bad2.FeedbackBoostFactor = 0.9
	if err := bad2.ValidateFeedbackPolicy(); err == nil {
		t.Fatal("boost-factor<1 must error")
	}

	bad3 := &types.RetrievalConfig{}
	bad3.FeedbackMinSamples = 0
	if err := bad3.ValidateFeedbackPolicy(); err == nil {
		t.Fatal("min-samples<1 must error")
	}

	bad4 := &types.RetrievalConfig{}
	bad4.FeedbackPenaltyThreshold = 0.2
	bad4.FeedbackNeedsOptimizationThreshold = 0.5
	if err := bad4.ValidateFeedbackPolicy(); err == nil {
		t.Fatal("needs-optimisation>penalty must error")
	}
}

func TestGetEffectiveFeedbackPolicyDefaults(t *testing.T) {
	cfg := &types.RetrievalConfig{}
	if got := cfg.GetEffectiveFeedbackBoostThreshold(); got != 0.8 {
		t.Fatalf("boost threshold: got %v, want 0.8", got)
	}
	if got := cfg.GetEffectiveFeedbackPenaltyThreshold(); got != 0.5 {
		t.Fatalf("penalty threshold: got %v, want 0.5", got)
	}
	if got := cfg.GetEffectiveFeedbackBoostFactor(); got != 1.2 {
		t.Fatalf("boost factor: got %v, want 1.2", got)
	}
	if got := cfg.GetEffectiveFeedbackPenaltyFactor(); got != 0.8 {
		t.Fatalf("penalty factor: got %v, want 0.8", got)
	}
	if got := cfg.GetEffectiveFeedbackMinSamples(); got != 3 {
		t.Fatalf("min samples: got %v, want 3", got)
	}
	if got := cfg.GetEffectiveFeedbackNeedsOptimizationThreshold(); got != 0.2 {
		t.Fatalf("needs-optimisation: got %v, want 0.2", got)
	}
	if got := cfg.GetEffectiveFeedbackRankingEnabled(); got {
		t.Fatal("ranking should default to disabled")
	}
}

func TestFeedbackReasonsValidation(t *testing.T) {
	cases := []struct {
		name    string
		reasons []string
		wantErr bool
	}{
		{"empty", nil, false},
		{"valid-set", []string{"inaccurate", "outdated"}, false},
		{"invalid", []string{"lol"}, true},
		{"duplicates", []string{"inaccurate", "inaccurate"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			hasInvalid := false
			for _, r := range tc.reasons {
				if !types.FeedbackDislikeReasons[r] {
					hasInvalid = true
					break
				}
			}
			if tc.wantErr && !hasInvalid {
				t.Fatal("expected at least one invalid reason, found none")
			}
			if !tc.wantErr && hasInvalid {
				t.Fatal("expected no invalid reasons, found one")
			}
		})
	}
}