package types

import (
	"strings"
	"testing"
	"time"
)

func TestRenderPromptPlaceholdersCurrentTimeIsDateOnly(t *testing.T) {
	got := RenderPromptPlaceholders("now={{current_time}}", PlaceholderValues{})
	want := "now=" + time.Now().Format("2006-01-02")
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
	if strings.Contains(got, "T") {
		t.Fatalf("date-only current_time must not include a clock: %q", got)
	}
	if strings.Count(got, ":") != 0 {
		t.Fatalf("date-only current_time must not include hh:mm:ss: %q", got)
	}
}

func TestPromptCacheHitRate(t *testing.T) {
	empty := TokenUsage{}
	if empty.PromptCacheHitRate() != 0 {
		t.Fatalf("empty usage must report 0, got %v", empty.PromptCacheHitRate())
	}
	u := TokenUsage{PromptTokens: 1000}
	u.SetPromptCacheUsage(873, 0, 127, true)
	got := u.PromptCacheHitRate()
	if got < 87.2 || got > 87.4 {
		t.Fatalf("hit rate %v, want ~87.3", got)
	}
}
