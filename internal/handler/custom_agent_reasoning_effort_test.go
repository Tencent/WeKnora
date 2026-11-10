package handler

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

// TestNormalizeAgentReasoningEffortRejectsUnknownLevel pins the create/update
// guard. An unvalidated level does not degrade to "off": ReasoningEffort wins
// over the legacy boolean and every non-empty value other than "off" counts as
// thinking-on, so a typo used to enable thinking and hand the vendor a level
// it never documented.
func TestNormalizeAgentReasoningEffortRejectsUnknownLevel(t *testing.T) {
	cfg := types.CustomAgentConfig{ReasoningEffort: "hgih"}
	if err := normalizeAgentReasoningEffort(&cfg); err == nil {
		t.Fatal("expected an error for an unknown reasoning_effort")
	}
}

// TestNormalizeAgentReasoningEffortCanonicalizes checks the accepted aliases
// are rewritten, so EnsureDefaults and the editor only ever see canonical
// values.
func TestNormalizeAgentReasoningEffortCanonicalizes(t *testing.T) {
	cases := map[string]string{
		"":      "",
		"high":  "high",
		"off":   "off",
		"none":  "off",
		"false": "off",
		"true":  "auto",
		"on":    "auto",
	}
	for in, want := range cases {
		cfg := types.CustomAgentConfig{ReasoningEffort: in}
		if err := normalizeAgentReasoningEffort(&cfg); err != nil {
			t.Errorf("normalize(%q): unexpected error %v", in, err)
			continue
		}
		if cfg.ReasoningEffort != want {
			t.Errorf("normalize(%q) = %q, want %q", in, cfg.ReasoningEffort, want)
		}
	}
}

// TestNormalizeAgentReasoningEffortNilConfig keeps the helper safe for the
// zero-value paths.
func TestNormalizeAgentReasoningEffortNilConfig(t *testing.T) {
	if err := normalizeAgentReasoningEffort(nil); err != nil {
		t.Fatalf("nil config should be a no-op, got %v", err)
	}
}
