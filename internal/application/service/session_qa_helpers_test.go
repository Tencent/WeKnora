package service

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

func TestApplyAgentOverridesToChatManageMarksCustomSystemPrompt(t *testing.T) {
	s := &sessionService{}
	cm := &types.ChatManage{}
	agent := &types.CustomAgent{
		Config: types.CustomAgentConfig{SystemPrompt: "You are Xiaochi."},
	}

	s.applyAgentOverridesToChatManage(context.Background(), agent, cm)

	if cm.SummaryConfig.Prompt != "You are Xiaochi." {
		t.Errorf("prompt: got %q", cm.SummaryConfig.Prompt)
	}
	if !cm.AgentSystemPromptApplied {
		t.Fatal("expected custom agent system prompt to be marked as applied")
	}
}
