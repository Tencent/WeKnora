package agent

import (
	"strings"
	"testing"
)

func TestWikiGranularityGuidance_RoutesByKey(t *testing.T) {
	cases := map[string]string{
		"focused":    WikiGranularityGuidanceFocused,
		"standard":   WikiGranularityGuidanceStandard,
		"exhaustive": WikiGranularityGuidanceExhaustive,
	}
	for key, want := range cases {
		if got := WikiGranularityGuidance(key); got != want {
			t.Errorf("WikiGranularityGuidance(%q) returned unexpected block", key)
			_ = got
		}
	}
}

func TestWikiGranularityGuidance_UnknownDefaultsToStandard(t *testing.T) {
	unknowns := []string{"", "FOCUSED", "detailed", "minimal", "full", "unknown"}
	for _, k := range unknowns {
		if WikiGranularityGuidance(k) != WikiGranularityGuidanceStandard {
			t.Errorf("WikiGranularityGuidance(%q) should fall back to STANDARD block", k)
		}
	}
}

// Sanity check that the three guidance blocks are meaningfully different.
// A regression (e.g. two constants accidentally pointing at the same string)
// would silently disable the user-facing level control.
func TestWikiGranularityGuidance_BlocksAreDistinct(t *testing.T) {
	blocks := []string{
		WikiGranularityGuidanceFocused,
		WikiGranularityGuidanceStandard,
		WikiGranularityGuidanceExhaustive,
	}
	seen := make(map[string]bool, len(blocks))
	for _, b := range blocks {
		if b == "" {
			t.Error("granularity guidance block must not be empty")
			continue
		}
		if seen[b] {
			t.Error("granularity guidance blocks must be distinct")
		}
		seen[b] = true
	}

	// Each block should name its mode, so the LLM can't silently get the
	// wrong guidance without us noticing in review.
	if !strings.Contains(WikiGranularityGuidanceFocused, "FOCUSED") {
		t.Error("focused block should self-identify")
	}
	if !strings.Contains(WikiGranularityGuidanceStandard, "STANDARD") {
		t.Error("standard block should self-identify")
	}
	if !strings.Contains(WikiGranularityGuidanceExhaustive, "EXHAUSTIVE") {
		t.Error("exhaustive block should self-identify")
	}
}
