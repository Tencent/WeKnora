package searchutil

import (
	"math"
	"testing"
)

type keywordScoreItem struct {
	keyword bool
	score   float64
}

func keywordScores(items ...*keywordScoreItem) []*keywordScoreItem {
	return items
}

func normalizeKeywordScoresForTest(results []*keywordScoreItem, callbacks KeywordScoreCallbacks) {
	NormalizeKeywordScores(results,
		func(it *keywordScoreItem) bool { return it.keyword },
		func(it *keywordScoreItem) float64 { return it.score },
		func(it *keywordScoreItem, score float64) { it.score = score },
		callbacks,
	)
}

func TestNormalizeKeywordScores(t *testing.T) {
	t.Run("no keyword results leaves scores untouched", func(t *testing.T) {
		results := keywordScores(
			&keywordScoreItem{keyword: false, score: 5},
			&keywordScoreItem{keyword: false, score: 9},
		)
		normalizeKeywordScoresForTest(results, KeywordScoreCallbacks{})
		if results[0].score != 5 || results[1].score != 9 {
			t.Errorf("non-keyword scores must be untouched, got %v, %v", results[0].score, results[1].score)
		}
	})

	t.Run("single keyword result scores 1", func(t *testing.T) {
		results := keywordScores(
			&keywordScoreItem{keyword: false, score: 7},
			&keywordScoreItem{keyword: true, score: 3},
		)
		normalizeKeywordScoresForTest(results, KeywordScoreCallbacks{})
		if results[1].score != 1.0 {
			t.Errorf("single keyword score = %v, want 1", results[1].score)
		}
		if results[0].score != 7 {
			t.Errorf("non-keyword score must be untouched, got %v", results[0].score)
		}
	})

	t.Run("no variance scores all 1 and reports it", func(t *testing.T) {
		results := keywordScores(
			&keywordScoreItem{keyword: true, score: 4},
			&keywordScoreItem{keyword: true, score: 4},
		)
		called := 0
		normalizeKeywordScoresForTest(results, KeywordScoreCallbacks{
			OnNoVariance: func(count int, score float64) {
				called++
				if count != 2 || score != 4 {
					t.Errorf("OnNoVariance(%d, %v), want (2, 4)", count, score)
				}
			},
		})
		if results[0].score != 1.0 || results[1].score != 1.0 {
			t.Errorf("no-variance scores must all become 1, got %v, %v", results[0].score, results[1].score)
		}
		if called != 1 {
			t.Errorf("OnNoVariance called %d times, want 1", called)
		}
	})

	t.Run("range maps min to 0 and max to 1", func(t *testing.T) {
		results := keywordScores(
			&keywordScoreItem{keyword: true, score: 0},
			&keywordScoreItem{keyword: true, score: 10},
			&keywordScoreItem{keyword: true, score: 20},
			&keywordScoreItem{keyword: false, score: 99},
		)
		normalizeKeywordScoresForTest(results, KeywordScoreCallbacks{})
		want := []float64{0, 0.5, 1}
		for i, w := range want {
			if math.Abs(results[i].score-w) > 1e-9 {
				t.Errorf("score[%d] = %v, want %v", i, results[i].score, w)
			}
		}
		if results[3].score != 99 {
			t.Errorf("non-keyword score must be untouched, got %v", results[3].score)
		}
	})

	t.Run("ten or more results use percentile bounds", func(t *testing.T) {
		results := make([]*keywordScoreItem, 0, 10)
		for s := 0.0; s <= 90; s += 10 {
			results = append(results, &keywordScoreItem{keyword: true, score: s})
		}
		called := 0
		normalizeKeywordScoresForTest(results, KeywordScoreCallbacks{
			OnNormalized: func(count int, rawMin, rawMax, normalizeMin, normalizeMax float64) {
				called++
				if count != 10 || rawMin != 0 || rawMax != 90 || normalizeMin != 0 || normalizeMax != 90 {
					t.Errorf("OnNormalized(%d, %v, %v, %v, %v), want (10, 0, 90, 0, 90)",
						count, rawMin, rawMax, normalizeMin, normalizeMax)
				}
			},
		})
		if math.Abs(results[0].score-0) > 1e-9 || math.Abs(results[9].score-1) > 1e-9 {
			t.Errorf("percentile bounds must map 0 to 0 and 90 to 1, got %v and %v",
				results[0].score, results[9].score)
		}
		if called != 1 {
			t.Errorf("OnNormalized called %d times, want 1", called)
		}
	})
}
