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
		{name: "codex", set: map[string]string{"CODEX_PROMPT_HOSTNAME": "host"}, want: "codex"},
		{name: "aider", set: map[string]string{"AIDER_PROMPT": "y"}, want: "aider"},
		{name: "continue", set: map[string]string{"CONTINUE_GLOBAL_DIR": "/tmp"}, want: "continue"},
		{name: "opencode", set: map[string]string{"OPENCODE_RUNNING": "1"}, want: "opencode"},
		{name: "gemini-coder", set: map[string]string{"GEMINICODER_PROFILE": "default"}, want: "gemini-coder"},
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

func TestShouldUseAgentMode_Triggers(t *testing.T) {
	t.Run("WEKNORA_AGENT=1 triggers", func(t *testing.T) {
		clearAgentEnv(t)
		t.Setenv("WEKNORA_AGENT", "1")
		assert.True(t, ShouldUseAgentMode(nil))
	})
	t.Run("WEKNORA_AGENT=true triggers", func(t *testing.T) {
		clearAgentEnv(t)
		t.Setenv("WEKNORA_AGENT", "true")
		assert.True(t, ShouldUseAgentMode(nil))
	})
	t.Run("WEKNORA_AGENT=0 does not trigger", func(t *testing.T) {
		clearAgentEnv(t)
		t.Setenv("WEKNORA_AGENT", "0")
		assert.False(t, ShouldUseAgentMode(nil))
	})
	t.Run("AI agent env triggers", func(t *testing.T) {
		clearAgentEnv(t)
		t.Setenv("CLAUDECODE", "1")
		assert.True(t, ShouldUseAgentMode(nil))
	})
	t.Run("no triggers", func(t *testing.T) {
		clearAgentEnv(t)
		assert.False(t, ShouldUseAgentMode(nil))
	})
}
