package api

import (
	"context"
	"testing"
)

// The stored level reaches Options through a plain string cast on several
// paths (legacy rows, builtin YAML agents, SummaryConfig), so a typo used to
// pass straight through: non-empty means Enabled(), which turned thinking ON
// and sent the vendor a level it never defined.
func TestOptions_ReasoningIgnoresUnparseableEffort(t *testing.T) {
	on, off := true, false
	cases := []struct {
		name      string
		effort    ReasoningEffort
		thinking  *bool
		want      ReasoningEffort
		requested bool
	}{
		{name: "typo alone is no preference", effort: "hgih", want: "", requested: false},
		{name: "typo falls back to thinking off", effort: "hgih", thinking: &off, want: ReasoningOff, requested: true},
		{name: "typo falls back to thinking on", effort: "hgih", thinking: &on, want: ReasoningAuto, requested: true},
		{name: "valid level wins", effort: ReasoningHigh, thinking: &off, want: ReasoningHigh, requested: true},
		{name: "alias is canonicalised", effort: "none", want: ReasoningOff, requested: true},
		{name: "empty defers to the model", want: "", requested: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			opts := &Options{ReasoningEffort: tc.effort, Thinking: tc.thinking}
			level, requested := opts.Reasoning()
			if level != tc.want || requested != tc.requested {
				t.Fatalf("Reasoning() = (%q, %v), want (%q, %v)", level, requested, tc.want, tc.requested)
			}
			// The important half: a typo must never read as "thinking on".
			if tc.effort == "hgih" && tc.thinking == nil && opts.ThinkingRequested() {
				t.Fatal("an unparseable level enabled thinking")
			}
		})
	}
}

func TestSanitizeReasoningEffort(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		raw  string
		want ReasoningEffort
	}{
		{raw: "", want: ""},
		{raw: "high", want: ReasoningHigh},
		{raw: "true", want: ReasoningAuto},
		{raw: "hgih", want: ""},
		{raw: "HIGH", want: ""}, // levels are lower-case; a near miss is still a miss
	}
	for _, tc := range cases {
		t.Run(tc.raw, func(t *testing.T) {
			if got := SanitizeReasoningEffort(ctx, tc.raw, "test"); got != tc.want {
				t.Errorf("SanitizeReasoningEffort(%q) = %q, want %q", tc.raw, got, tc.want)
			}
		})
	}
}
