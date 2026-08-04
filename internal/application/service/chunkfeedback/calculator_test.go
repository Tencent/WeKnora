package chunkfeedback

import "testing"

func TestApplyVote(t *testing.T) {
	config := testConfig()
	tests := []struct {
		name string
		in   State
		vote VoteChange
		want State
	}{
		{
			name: "new like increments likes without consuming existing dislikes",
			in:   State{LikeCount: 2, DislikeCount: 3, RecallWeight: 0.5, QualityStatus: StatusNormal},
			vote: VoteChange{WasCreated: true, IsChanged: true, IsPositive: true},
			want: State{LikeCount: 3, DislikeCount: 3, PositiveRate: 0.5, RecallWeight: 1.0, QualityStatus: StatusNormal},
		},
		{
			name: "switch dislike to like moves one vote",
			in:   State{LikeCount: 2, DislikeCount: 3, RecallWeight: 0.5, QualityStatus: StatusNormal},
			vote: VoteChange{IsChanged: true, IsPositive: true},
			want: State{LikeCount: 3, DislikeCount: 2, PositiveRate: 0.6, RecallWeight: 1.0, QualityStatus: StatusNormal},
		},
		{
			name: "switch like to dislike moves one vote",
			in:   State{LikeCount: 2, DislikeCount: 2, RecallWeight: 1.0, QualityStatus: StatusNormal},
			vote: VoteChange{IsChanged: true, IsPositive: false},
			want: State{LikeCount: 1, DislikeCount: 3, PositiveRate: 0.25, RecallWeight: 0.5, QualityStatus: StatusNormal},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ApplyVote(tt.in, tt.vote, config)
			assertState(t, got, tt.want)
		})
	}
}

func TestCancelVote(t *testing.T) {
	config := testConfig()
	got := CancelVote(State{
		LikeCount:     1,
		DislikeCount:  4,
		PositiveRate:  0.2,
		RecallWeight:  0.5,
		QualityStatus: StatusPending,
	}, true, config)

	assertState(t, got, State{
		LikeCount:     0,
		DislikeCount:  4,
		PositiveRate:  0,
		RecallWeight:  0.5,
		QualityStatus: StatusNormal,
	})
}

func TestCancelLastVoteResetsWeightToNeutral(t *testing.T) {
	got := CancelVote(State{
		LikeCount:     1,
		PositiveRate:  1,
		RecallWeight:  1.5,
		QualityStatus: StatusNormal,
	}, true, testConfig())

	assertState(t, got, State{
		LikeCount:     0,
		DislikeCount:  0,
		PositiveRate:  0,
		RecallWeight:  1.0,
		QualityStatus: StatusNormal,
	})
}

func TestApplyVoteNoopDoesNotChangeCounters(t *testing.T) {
	got := ApplyVote(State{
		LikeCount:     2,
		DislikeCount:  3,
		PositiveRate:  0.4,
		RecallWeight:  0.5,
		QualityStatus: StatusNormal,
	}, VoteChange{WasCreated: false, IsChanged: false, IsPositive: false}, testConfig())

	assertState(t, got, State{
		LikeCount:     2,
		DislikeCount:  3,
		PositiveRate:  0.4,
		RecallWeight:  0.5,
		QualityStatus: StatusNormal,
	})
}

func TestQualityStatusMarksPendingAfterEnoughLowQualityVotes(t *testing.T) {
	got := ApplyVote(State{
		LikeCount:     1,
		DislikeCount:  3,
		QualityStatus: StatusNormal,
	}, VoteChange{WasCreated: true, IsChanged: true, IsPositive: false}, testConfig())

	if got.QualityStatus != StatusPending {
		t.Fatalf("QualityStatus = %q, want %q", got.QualityStatus, StatusPending)
	}
}

func TestQualityStatusUsesConfiguredMinimumFeedbackCount(t *testing.T) {
	config := testConfig()
	config.AutoMarkMinFeedbacks = 1

	got := ApplyVote(State{QualityStatus: StatusNormal},
		VoteChange{WasCreated: true, IsChanged: true, IsPositive: false}, config)

	if got.QualityStatus != StatusPending {
		t.Fatalf("QualityStatus = %q, want %q", got.QualityStatus, StatusPending)
	}
}

func TestRecallWeightClampsToConfiguredBounds(t *testing.T) {
	config := testConfig()
	config.WeightBoostFactor = 5
	config.WeightPenaltyFactor = 0.01
	config.MinWeight = 0.25
	config.MaxWeight = 2

	boosted := RecallWeight(0.9, 10, config)
	if boosted != 2 {
		t.Fatalf("boosted RecallWeight = %v, want 2", boosted)
	}

	penalized := RecallWeight(0.2, 10, config)
	if penalized != 0.25 {
		t.Fatalf("penalized RecallWeight = %v, want 0.25", penalized)
	}
}

func TestRecalculateUsesExactPositiveRateForThresholds(t *testing.T) {
	config := testConfig()

	almostHigh := Recalculate(State{LikeCount: 159, DislikeCount: 41}, config)
	if almostHigh.PositiveRate != 0.795 {
		t.Fatalf("PositiveRate = %v, want 0.795", almostHigh.PositiveRate)
	}
	if almostHigh.RecallWeight != 1.0 {
		t.Fatalf("RecallWeight at 79.5%% = %v, want neutral 1.0", almostHigh.RecallWeight)
	}

	atHigh := Recalculate(State{LikeCount: 160, DislikeCount: 40}, config)
	if atHigh.RecallWeight != config.WeightBoostFactor {
		t.Fatalf("RecallWeight at 80%% = %v, want %v", atHigh.RecallWeight, config.WeightBoostFactor)
	}

	justBelowLow := Recalculate(State{LikeCount: 99, DislikeCount: 101}, config)
	if justBelowLow.RecallWeight != config.WeightPenaltyFactor {
		t.Fatalf("RecallWeight below 50%% = %v, want %v", justBelowLow.RecallWeight, config.WeightPenaltyFactor)
	}

	atLow := Recalculate(State{LikeCount: 100, DislikeCount: 100}, config)
	if atLow.RecallWeight != 1.0 {
		t.Fatalf("RecallWeight at 50%% = %v, want neutral 1.0", atLow.RecallWeight)
	}
}

func testConfig() Config {
	return Config{
		HighQualityThreshold: 0.8,
		LowQualityThreshold:  0.5,
		WeightBoostFactor:    1.5,
		WeightPenaltyFactor:  0.5,
		AutoMarkThreshold:    0.3,
		AutoMarkMinFeedbacks: 5,
		MinWeight:            0.1,
		MaxWeight:            2,
	}
}

func assertState(t *testing.T, got, want State) {
	t.Helper()
	if got.LikeCount != want.LikeCount {
		t.Fatalf("LikeCount = %d, want %d", got.LikeCount, want.LikeCount)
	}
	if got.DislikeCount != want.DislikeCount {
		t.Fatalf("DislikeCount = %d, want %d", got.DislikeCount, want.DislikeCount)
	}
	if got.PositiveRate != want.PositiveRate {
		t.Fatalf("PositiveRate = %v, want %v", got.PositiveRate, want.PositiveRate)
	}
	if got.RecallWeight != want.RecallWeight {
		t.Fatalf("RecallWeight = %v, want %v", got.RecallWeight, want.RecallWeight)
	}
	if got.QualityStatus != want.QualityStatus {
		t.Fatalf("QualityStatus = %q, want %q", got.QualityStatus, want.QualityStatus)
	}
}
