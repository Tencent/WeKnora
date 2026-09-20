package types

import "testing"

// TestEnsureDefaultsSyncsThinkingWithReasoningEffort pins the alias contract
// documented on CustomAgentConfig.ReasoningEffort.
//
// EnsureDefaults used to pin Thinking to false whenever it was nil, without
// looking at the graded level. An agent saved as {reasoning_effort: "high"}
// therefore came back as {thinking: false, reasoning_effort: "high"}: the
// request was still sent at "high" (api.Options.Reasoning prefers the graded
// field) while the editor, the pipeline logs and the
// "thinking is unset after EnsureDefaults" warning all reported it as off.
func TestEnsureDefaultsSyncsThinkingWithReasoningEffort(t *testing.T) {
	cases := []struct {
		level string
		want  bool
	}{
		{"high", true},
		{"auto", true},
		{"minimal", true},
		{"off", false},
		// Aliases the API layer accepts before canonicalizing.
		{"none", false},
		{"disabled", false},
	}
	for _, tc := range cases {
		agent := &CustomAgent{Config: CustomAgentConfig{ReasoningEffort: tc.level}}
		agent.EnsureDefaults()
		if agent.Config.Thinking == nil {
			t.Errorf("reasoning_effort=%q left Thinking nil", tc.level)
			continue
		}
		if *agent.Config.Thinking != tc.want {
			t.Errorf("reasoning_effort=%q: Thinking = %v, want %v",
				tc.level, *agent.Config.Thinking, tc.want)
		}
	}
}

// TestEnsureDefaultsKeepsLegacyThinkingWhenNoLevel keeps the pre-existing
// behaviour for agents that only ever used the boolean.
func TestEnsureDefaultsKeepsLegacyThinkingWhenNoLevel(t *testing.T) {
	enabled := true
	agent := &CustomAgent{Config: CustomAgentConfig{Thinking: &enabled}}
	agent.EnsureDefaults()
	if agent.Config.Thinking == nil || !*agent.Config.Thinking {
		t.Fatal("an explicit thinking:true must survive EnsureDefaults")
	}
	if agent.Config.ReasoningEffort != "" {
		t.Errorf("the graded field must stay unset, got %q", agent.Config.ReasoningEffort)
	}

	unset := &CustomAgent{}
	unset.EnsureDefaults()
	if unset.Config.Thinking == nil || *unset.Config.Thinking {
		t.Fatal("an unset thinking must still be pinned to false")
	}
}
