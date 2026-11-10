package text_test

import (
	"testing"
	"time"

	"github.com/Tencent/WeKnora/cli/internal/text"
)

func TestFuzzyAgo(t *testing.T) {
	now := time.Date(2026, 5, 8, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name string
		t    time.Time
		want string
	}{
		{"5 minutes ago", now.Add(-5 * time.Minute), "about 5 minutes ago"},
		{"1 hour ago", now.Add(-1 * time.Hour), "about 1 hour ago"},
		{"3 hours ago", now.Add(-3 * time.Hour), "about 3 hours ago"},
		{"2 days ago", now.Add(-2 * 24 * time.Hour), "about 2 days ago"},
		{"30 days ago", now.Add(-30 * 24 * time.Hour), "about 1 month ago"},
		{"5 minutes from now", now.Add(5 * time.Minute), "about 5 minutes from now"},
		{"less than 1m ago", now.Add(-30 * time.Second), "less than a minute ago"},
	}
	for _, tt := range tests {
		got := text.FuzzyAgo(now, tt.t)
		if got != tt.want {
			t.Errorf("FuzzyAgo(%s) = %q, want %q", tt.name, got, tt.want)
		}
	}
}
