package types

import (
	"testing"
)

func TestResolveSystemPrompt(t *testing.T) {
	tests := []struct {
		name             string
		config           *AgentConfig
		webSearchEnabled bool
		want             string
	}{
		{
			name:             "nil config returns empty",
			config:           nil,
			webSearchEnabled: false,
			want:             "",
		},
		{
			name:             "unified SystemPrompt returned",
			config:           &AgentConfig{SystemPrompt: "my prompt"},
			webSearchEnabled: false,
			want:             "my prompt",
		},
		{
			name: "deprecated web-disabled fallback",
			config: &AgentConfig{
				SystemPromptWebDisabled: "no-web",
				SystemPromptWebEnabled:  "with-web",
			},
			webSearchEnabled: false,
			want:             "no-web",
		},
		{
			name: "deprecated web-enabled fallback",
			config: &AgentConfig{
				SystemPromptWebDisabled: "no-web",
				SystemPromptWebEnabled:  "with-web",
			},
			webSearchEnabled: true,
			want:             "with-web",
		},
		{
			name:             "empty config returns empty",
			config:           &AgentConfig{},
			webSearchEnabled: false,
			want:             "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.config.ResolveSystemPrompt(tt.webSearchEnabled)
			if got != tt.want {
				t.Errorf("ResolveSystemPrompt() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestUseCustomSystemPromptFlag verifies that UseCustomSystemPrompt=false
// does not block a non-empty SystemPrompt from being resolved by agent_service.
// The fix in agent_service.go checks config.SystemPrompt != "" as a fallback,
// so ResolveSystemPrompt must return the prompt even when the flag is false.
func TestUseCustomSystemPromptFlag(t *testing.T) {
	cfg := &AgentConfig{
		SystemPrompt:          "custom prompt",
		UseCustomSystemPrompt: false, // flag NOT set
	}

	// ResolveSystemPrompt should still return the prompt
	got := cfg.ResolveSystemPrompt(false)
	if got != "custom prompt" {
		t.Errorf("expected 'custom prompt', got %q", got)
	}
}

// The window the agent plans against must come from the model when the model
// knows it. Assuming a window wider than the real one means compaction never
// runs and the provider rejects the request instead.
func TestAgentMaxContextTokens(t *testing.T) {
	if got := AgentMaxContextTokens(0, 0); got != DefaultMaxContextTokens {
		t.Fatalf("nothing known = %d, want %d", got, DefaultMaxContextTokens)
	}
	if got := AgentMaxContextTokens(0, 32768); got != 32768 {
		t.Fatalf("model window unused: got %d, want 32768", got)
	}
	if got := AgentMaxContextTokens(64000, 32768); got != 64000 {
		t.Fatalf("explicit agent setting must win, got %d", got)
	}
	if got := AgentMaxContextTokens(64000, 0); got != 64000 {
		t.Fatalf("explicit agent setting with unknown model = %d, want 64000", got)
	}
}

func TestAgentRoundMaxCompletionTokens(t *testing.T) {
	if got := AgentRoundMaxCompletionTokens(0); got != DefaultSmartReasoningMaxCompletionTokens {
		t.Fatalf("zero configured = %d, want %d", got, DefaultSmartReasoningMaxCompletionTokens)
	}
	if got := AgentRoundMaxCompletionTokensFor(0, "cfg-a"); got != DefaultAgentMaxCompletionTokens {
		t.Fatalf("unset with sandbox = %d, want %d", got, DefaultAgentMaxCompletionTokens)
	}
	if got := AgentRoundMaxCompletionTokens(2048); got != 2048 {
		t.Fatalf("explicit 2048 = %d, want 2048", got)
	}
	if got := AgentRoundMaxCompletionTokens(4096); got != 4096 {
		t.Fatalf("explicit 4096 = %d, want 4096", got)
	}
	// With a sandbox bound, a cap this small cannot hold a file body inside the
	// tool-call JSON, so every write truncates mid-string and the agent can
	// never finish. The floor makes the setting survivable rather than a
	// deadlock.
	if got := AgentRoundMaxCompletionTokensFor(4096, "cfg-a"); got != MinSandboxWriteCompletionTokens {
		t.Fatalf("explicit 4096 with sandbox = %d, want floor %d", got, MinSandboxWriteCompletionTokens)
	}
	if got := AgentRoundMaxCompletionTokensFor(32768, "cfg-a"); got != 32768 {
		t.Fatalf("explicit value above the floor must be honored, got %d", got)
	}
	if got := AgentRoundMaxCompletionTokens(4096); got != 4096 {
		t.Fatalf("no sandbox means no floor, got %d", got)
	}
	if got := AgentRoundMaxCompletionTokens(64000); got != 64000 {
		t.Fatalf("explicit high value = %d, want 64000", got)
	}
}
