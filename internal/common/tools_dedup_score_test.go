package common

import (
	"fmt"
	"testing"
)

type dedupScoreItem struct {
	key   string
	score float64
}

func (s dedupScoreItem) GetScore() float64 { return s.score }

func dedupScoreKeys(items []dedupScoreItem) []string {
	keys := make([]string, len(items))
	for i, item := range items {
		keys[i] = item.key
	}
	return keys
}

func TestDeduplicateWithScoreKeepsHighestScore(t *testing.T) {
	items := []dedupScoreItem{
		{key: "a", score: 1},
		{key: "b", score: 5},
		{key: "a", score: 3},
		{key: "b", score: 2},
		{key: "c", score: 4},
	}
	got := DeduplicateWithScore(func(s dedupScoreItem) string { return s.key }, items...)
	want := map[string]float64{"a": 3, "b": 5, "c": 4}
	if len(got) != 3 {
		t.Fatalf("got %d items, want 3: %v", len(got), got)
	}
	for _, item := range got {
		if want[item.key] != item.score {
			t.Errorf("key %q kept score %v, want %v", item.key, item.score, want[item.key])
		}
	}
}

func TestDeduplicateWithScoreSortsByScoreDescending(t *testing.T) {
	items := []dedupScoreItem{
		{key: "low", score: 1},
		{key: "high", score: 9},
		{key: "mid", score: 5},
	}
	got := DeduplicateWithScore(func(s dedupScoreItem) string { return s.key }, items...)
	want := []string{"high", "mid", "low"}
	if keys := dedupScoreKeys(got); fmt.Sprint(keys) != fmt.Sprint(want) {
		t.Fatalf("got order %v, want %v", keys, want)
	}
}

func TestDeduplicateWithScoreTiesKeepFirstAppearanceOrder(t *testing.T) {
	// All scores tie, so the result must follow first-appearance order.
	// Before the fix the items went through a map first, and the order
	// came out random run to run.
	items := make([]dedupScoreItem, 30)
	want := make([]string, 30)
	for i := range items {
		items[i] = dedupScoreItem{key: fmt.Sprintf("k%02d", i), score: 1.0}
		want[i] = items[i].key
	}
	for trial := 0; trial < 50; trial++ {
		got := DeduplicateWithScore(func(s dedupScoreItem) string { return s.key }, items...)
		if keys := dedupScoreKeys(got); fmt.Sprint(keys) != fmt.Sprint(want) {
			t.Fatalf("trial %d: got order %v, want first-appearance order %v", trial, keys, want)
		}
	}
}

func TestDeduplicateWithScoreEmptyAndSingle(t *testing.T) {
	if got := DeduplicateWithScore(func(s dedupScoreItem) string { return s.key }); len(got) != 0 {
		t.Fatalf("empty input returned %v, want empty", got)
	}
	got := DeduplicateWithScore(func(s dedupScoreItem) string { return s.key }, dedupScoreItem{key: "only", score: 2})
	if len(got) != 1 || got[0].key != "only" {
		t.Fatalf("single input returned %v", got)
	}
}
