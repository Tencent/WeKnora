package types

import (
	"encoding/json"
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestLearningBKTGoldenAndIndependentPrediction(t *testing.T) {
	p := LearningInitialMastery
	want := []float64{0.5526315789473684, 0.8439524838012959, 0.9584756076946482}
	for i := range 3 {
		p = LearningBKT(p, true)
		require.InDelta(t, want[i], p, 1e-6)
	}
	// Enumerate the hidden known/unknown distribution independently for all
	// 256 eight-answer sequences, weighting each observation then transition.
	for bits := range 256 {
		known, unknown, got := .2, .8, .2
		for step := range 8 {
			correct := bits&(1<<step) != 0
			if correct {
				known *= .9
				unknown *= .25
			} else {
				known *= .1
				unknown *= .75
			}
			total := known + unknown
			known, unknown = known/total, unknown/total
			known, unknown = known+.15*unknown, .85*unknown
			got = LearningBKT(got, correct)
			require.InDelta(t, known, got, 1e-12)
			require.InDelta(t, 1., known+unknown, 1e-12)
		}
	}
	for _, p := range []float64{math.NaN(), math.Inf(1), -1, 2} {
		require.False(t, math.IsNaN(LearningBKT(p, false)))
	}
}

func TestLearningAssessmentFreshnessReviewAndFamiliarity(t *testing.T) {
	now := time.Date(2026, 9, 9, 0, 0, 0, 0, time.UTC)
	m := &LearningMastery{ViewedAt: &now, PMastery: .2}
	require.Equal(t, "unseen", LearningMasteryState(m, "s1", now).State)
	for i, days := range []int{1, 3, 7, 14, 30, 30} {
		LearningAssess(m, "s1", true, now)
		require.Equal(t, now.Add(time.Duration(days)*24*time.Hour), *m.NextReviewAt)
		if i < 2 {
			require.NotEqual(t, "mastered", LearningMasteryState(m, "s1", now).State)
		}
	}
	require.Equal(t, "review_due", LearningMasteryState(m, "s1", now.Add(31*24*time.Hour)).State)
	stale := LearningMasteryState(m, "s2", now)
	require.True(t, stale.SourceStale)
	require.Equal(t, .2, stale.PMastery)
	require.Equal(t, 6, stale.Attempts)
	require.Equal(t, "review_due", stale.State)
	before, after := LearningAssess(m, "s2", false, now)
	require.Equal(t, .2, before)
	require.InDelta(t, .1774193548387097, after, 1e-12)
	require.Equal(t, 1, m.Attempts)
	require.Zero(t, m.ConsecutiveCorrect)
	require.NotNil(t, m.ViewedAt)
	require.Equal(t, LearningFingerprint("  WHAT is atomic?\n"), LearningFingerprint("what is atomic?"))
}

func TestLearningPrivateRecordsNeverMarshalKeys(t *testing.T) {
	for _, v := range []any{
		LearningQuestion{
			CorrectOption: "SECRET",
			Explanation:   "SECRET",
			Evidence:      []LearningEvidence{{Quote: "SECRET"}},
		},
		LearningAttempt{Result: LearningAnswerResult{CorrectOption: "SECRET"}},
		LearningQuiz{SourceStamp: "SECRET"},
	} {
		b, err := json.Marshal(v)
		require.NoError(t, err)
		require.Equal(t, "{}", string(b))
	}
}
