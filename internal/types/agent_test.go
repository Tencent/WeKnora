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
