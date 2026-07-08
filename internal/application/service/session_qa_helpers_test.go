package service

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

func TestShouldApplyCustomAgentSystemPrompt(t *testing.T) {
	t.Run("custom agent prompt still applies", func(t *testing.T) {
		agent := &types.CustomAgent{
			ID: "custom-agent",
			Config: types.CustomAgentConfig{
				SystemPrompt: "custom prompt",
			},
		}

		if !shouldApplyCustomAgentSystemPrompt(agent) {
			t.Fatal("expected custom agent prompt to apply")
		}
	})

	t.Run("builtin quick answer saved prompt applies", func(t *testing.T) {
		agent := &types.CustomAgent{
			ID:        types.BuiltinQuickAnswerID,
			IsBuiltin: true,
			Config: types.CustomAgentConfig{
				SystemPrompt:   "custom prompt saved from agent editor",
				SystemPromptID: "default_kb",
			},
		}

		if !shouldApplyCustomAgentSystemPrompt(agent) {
			t.Fatal("expected builtin quick-answer saved prompt to apply")
		}
	})
}
