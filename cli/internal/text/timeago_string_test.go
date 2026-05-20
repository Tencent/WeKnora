package text_test

import (
	"testing"
	"time"

	"github.com/Tencent/WeKnora/cli/internal/text"
)

func TestFuzzyAgoStr(t *testing.T) {
	now := time.Date(2026, 5, 15, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name string
		ts   string
		want string
	}{
		{"empty renders dash", "", "-"},
		{"valid RFC3339 5m ago", now.Add(-5 * time.Minute).Format(time.RFC3339), "about 5 minutes ago"},
		{"valid RFC3339 2d ago", now.Add(-2 * 24 * time.Hour).Format(time.RFC3339), "about 2 days ago"},
		{"unparseable returned verbatim", "not-a-date", "not-a-date"},
		{"wrong format returned verbatim", "2026-05-15 12:00:00", "2026-05-15 12:00:00"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := text.FuzzyAgoStr(now, tt.ts)
			if got != tt.want {
				t.Errorf("FuzzyAgoStr(%q) = %q, want %q", tt.ts, got, tt.want)
			}
		})
	}
}
