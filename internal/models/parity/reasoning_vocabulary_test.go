package parity

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/models/api"
	"github.com/Tencent/WeKnora/internal/types"
)

// internal/types cannot import internal/models/api (it would cycle), so
// types.CustomAgent.EnsureDefaults restates the reasoning-effort
// vocabulary by hand to derive the legacy Thinking bool. Two copies of a
// vocabulary drift, and the last time they did the result was a fail-open:
// an unparseable level meant "unknown, therefore thinking on", which turned
// a typo into thinking enabled at the provider default with an undefined
// level sent upstream.
//
// This package imports both sides, so it is the one place the two can be
// compared. It walks every value api.ParseReasoningEffort accepts and
// asserts EnsureDefaults agrees about whether thinking is on, then asserts
// that a value that function rejects never enables it.
func TestReasoningEffortVocabulariesAgree(t *testing.T) {
	accepted := make([]string, 0, len(api.AllReasoningEfforts)+8)
	for _, level := range api.AllReasoningEfforts {
		accepted = append(accepted, string(level))
	}
	// The aliases ParseReasoningEffort documents for the legacy boolean UI
	// and for provider wording.
	accepted = append(accepted, "none", "false", "disabled", "true", "enabled", "default", "on")

	for _, raw := range accepted {
		t.Run("accepted/"+raw, func(t *testing.T) {
			level, ok := api.ParseReasoningEffort(raw)
			if !ok {
				t.Fatalf("ParseReasoningEffort rejected %q, which this test lists as accepted", raw)
			}
			agent := types.CustomAgent{Config: types.CustomAgentConfig{ReasoningEffort: raw}}
			agent.EnsureDefaults()
			cfg := agent.Config
			if cfg.Thinking == nil {
				t.Fatalf("EnsureDefaults left Thinking unset for the known level %q", raw)
			}
			if got, want := *cfg.Thinking, level.Enabled(); got != want {
				t.Errorf("EnsureDefaults(%q) derived Thinking=%v, the level means %v", raw, got, want)
			}
		})
	}

	for _, raw := range []string{"hgih", "ultra", "HIGH", " high ", "maximum", "0", "yes"} {
		t.Run("rejected/"+raw, func(t *testing.T) {
			if _, ok := api.ParseReasoningEffort(raw); ok {
				t.Fatalf("ParseReasoningEffort accepted %q; this table assumes it does not", raw)
			}
			agent := types.CustomAgent{Config: types.CustomAgentConfig{ReasoningEffort: raw}}
			agent.EnsureDefaults()
			if cfg := agent.Config; cfg.Thinking != nil && *cfg.Thinking {
				t.Errorf("an unparseable level must never enable thinking, %q did", raw)
			}
		})
	}
}
