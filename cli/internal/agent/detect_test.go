package agent

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// clearAgentEnv unsets every env var DetectAIAgent inspects, so a developer
// running these tests from inside Claude Code / Cursor doesn't see false
// positives.
func clearAgentEnv(t *testing.T) {
	t.Helper()
	for _, a := range aiAgentEnvs {
		t.Setenv(a.env, "")
	}
	t.Setenv("WEKNORA_NO_AGENT_AUTODETECT", "")
	t.Setenv("WEKNORA_AGENT", "")
}

func TestDetectAIAgent(t *testing.T) {
	cases := []struct {
		name string
		set  map[string]string
		want AIAgentName
	}{
		{name: "empty env", set: nil, want: ""},
		{name: "unrelated env", set: map[string]string{"PATH": "/usr/bin"}, want: ""},
		{name: "claude code", set: map[string]string{"CLAUDECODE": "1"}, want: "claude-code"},
		{name: "cursor", set: map[string]string{"CURSOR_AGENT": "yes"}, want: "cursor"},
		// Other entries (codex / aider / continue / opencode / gemini-coder)
		// were dropped in v0.2 ADR-3 — env names had no official agent docs.
		// New entries should arrive with a documented source URL.
		{
			name: "first-match precedence",
			set:  map[string]string{"CLAUDECODE": "1", "CURSOR_AGENT": "1"},
			want: "claude-code",
		},
		{name: "empty value treated as unset", set: map[string]string{"CLAUDECODE": ""}, want: ""},
		{
			name: "WEKNORA_NO_AGENT_AUTODETECT=1 suppresses detection",
			set:  map[string]string{"CLAUDECODE": "1", "WEKNORA_NO_AGENT_AUTODETECT": "1"},
			want: "",
		},
		{
			name: "WEKNORA_NO_AGENT_AUTODETECT=0 still allows detection",
			set:  map[string]string{"CLAUDECODE": "1", "WEKNORA_NO_AGENT_AUTODETECT": "0"},
			want: "claude-code",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			clearAgentEnv(t)
			for k, v := range tc.set {
				t.Setenv(k, v)
			}
			assert.Equal(t, tc.want, DetectAIAgent())
		})
	}
}

