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
		// Every rung of api.ReasoningLadder, plus the two switches.
		{"minimal", true},
		{"low", true},
		{"medium", true},
		{"high", true},
		{"xhigh", true},
		{"max", true},
		{"auto", true},
		{"off", false},
		// Aliases the API layer accepts before canonicalizing.
		{"none", false},
		{"disabled", false},
		{"false", false},
		{"true", true},
		{"enabled", true},
		{"default", true},
		{"on", true},
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

// TestEnsureDefaults_UnknownReasoningEffortDoesNotEnableThinking closes the
// fail-safe the write path and the runtime already implement.
//
// A level api.ParseReasoningEffort rejects is dropped at call time
// (api.SanitizeReasoningEffort returns "", so api.Options.Reasoning falls back
// to the boolean). If EnsureDefaults derived `Thinking = true` from that same
// typo, the fallback would enable thinking at the provider default — the exact
// outcome the 400 on the write path and the sanitiser exist to prevent. Builtin
// YAML agents and rows written before the 400 existed reach EnsureDefaults
// without ever passing the handler's normalisation, so this is the last guard.
func TestEnsureDefaults_UnknownReasoningEffortDoesNotEnableThinking(t *testing.T) {
	// Values api.ParseReasoningEffort rejects: a typo, a level that does not
	// exist, and canonical spellings in the wrong case (it neither trims nor
	// lowercases, so neither may this).
	for _, level := range []string{"hgih", "ultra", "HIGH", " high ", "Off"} {
		agent := &CustomAgent{Config: CustomAgentConfig{ReasoningEffort: level}}
		agent.EnsureDefaults()
		if agent.Config.Thinking == nil {
			t.Errorf("reasoning_effort=%q left Thinking nil; it must still be pinned", level)
			continue
		}
		if *agent.Config.Thinking {
			t.Errorf("reasoning_effort=%q enabled thinking; an unparseable level is not a preference", level)
		}
	}

	// An unknown level must not overwrite an explicit boolean either way.
	enabled := true
	kept := &CustomAgent{Config: CustomAgentConfig{ReasoningEffort: "hgih", Thinking: &enabled}}
	kept.EnsureDefaults()
	if kept.Config.Thinking == nil || !*kept.Config.Thinking {
		t.Error("an unparseable level must leave an explicit thinking:true alone")
	}
}
