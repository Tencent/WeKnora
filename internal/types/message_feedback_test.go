package types

import "testing"

func TestFeedbackCounterDeltas(t *testing.T) {
	cases := []struct {
		old, new        string
		dLike, dDislike int
	}{
		{"", "", 0, 0},
		{"", FeedbackRatingLike, 1, 0},
		{"", FeedbackRatingDislike, 0, 1},
		{FeedbackRatingLike, "", -1, 0},
		{FeedbackRatingLike, FeedbackRatingLike, 0, 0},
		{FeedbackRatingLike, FeedbackRatingDislike, -1, 1},
		{FeedbackRatingDislike, "", 0, -1},
		{FeedbackRatingDislike, FeedbackRatingLike, 1, -1},
		{FeedbackRatingDislike, FeedbackRatingDislike, 0, 0},
	}
	for _, tc := range cases {
		dLike, dDislike := FeedbackCounterDeltas(tc.old, tc.new)
		if dLike != tc.dLike || dDislike != tc.dDislike {
			t.Errorf("FeedbackCounterDeltas(%q, %q) = (%d, %d), want (%d, %d)",
				tc.old, tc.new, dLike, dDislike, tc.dLike, tc.dDislike)
		}
	}
}

func TestComputeRecallWeight(t *testing.T) {
	cfg := &RetrievalConfig{} // effective defaults: boost 0.8/1.1, penalty 0.5/0.9, min 3, needsOpt 0.2

	cases := []struct {
		name     string
		like     int
		dislike  int
		weight   float64
		needsOpt bool
	}{
		{"below min samples stays neutral", 0, 2, 1.0, false},
		{"all likes boosts", 3, 0, 1.1, false},
		{"exactly boost threshold boosts", 4, 1, 1.1, false}, // rate 0.8
		{"middle band neutral", 2, 1, 1.0, false},            // rate 0.667
		{"below penalty threshold penalizes", 1, 2, 0.9, false},
		{"very low rate flags needs optimization", 0, 5, 0.9, true},
		{"needs opt boundary not flagged at threshold", 1, 4, 0.9, false}, // rate 0.2 == threshold
	}
	for _, tc := range cases {
		weight, needsOpt := ComputeRecallWeight(tc.like, tc.dislike, cfg)
		if weight != tc.weight || needsOpt != tc.needsOpt {
			t.Errorf("%s: ComputeRecallWeight(%d, %d) = (%v, %v), want (%v, %v)",
				tc.name, tc.like, tc.dislike, weight, needsOpt, tc.weight, tc.needsOpt)
		}
	}
}

func TestComputeRecallWeightCustomConfig(t *testing.T) {
	cfg := &RetrievalConfig{
		FeedbackBoostThreshold:   0.9,
		FeedbackPenaltyThreshold: 0.3,
		FeedbackBoostFactor:      1.5,
		FeedbackPenaltyFactor:    0.5,
		FeedbackMinSamples:       1,
	}
	if w, _ := ComputeRecallWeight(1, 0, cfg); w != 1.5 {
		t.Errorf("boost with custom config = %v, want 1.5", w)
	}
	if w, _ := ComputeRecallWeight(0, 1, cfg); w != 0.5 {
		t.Errorf("penalty with custom config = %v, want 0.5", w)
	}
	if w, _ := ComputeRecallWeight(1, 1, cfg); w != 1.0 {
		t.Errorf("middle band with custom config = %v, want 1.0", w)
	}
}

func TestPositiveRateOf(t *testing.T) {
	if rate := PositiveRateOf(0, 0); rate != 0 {
		t.Errorf("PositiveRateOf(0, 0) = %v, want 0", rate)
	}
	if rate := PositiveRateOf(3, 1); rate != 0.75 {
		t.Errorf("PositiveRateOf(3, 1) = %v, want 0.75", rate)
	}
}
