package agent

import (
	"testing"
	"time"

	agenttools "github.com/Tencent/WeKnora/internal/agent/tools"
	"github.com/Tencent/WeKnora/internal/types"
)

func TestGetToolExecTimeoutKeepsDefaultForOrdinaryTools(t *testing.T) {
	engine := &AgentEngine{config: &types.AgentConfig{}}

	got := engine.getToolExecTimeout(agenttools.ToolKnowledgeSearch)

	if got != defaultToolExecTimeout {
		t.Fatalf("ordinary tool timeout = %s, want %s", got, defaultToolExecTimeout)
	}
}

func TestGetToolExecTimeoutUsesLongerDefaultForWebFetch(t *testing.T) {
	engine := &AgentEngine{config: &types.AgentConfig{}}

	got := engine.getToolExecTimeout(agenttools.ToolWebFetch)

	if got != defaultWebFetchToolExecTimeout {
		t.Fatalf("web_fetch timeout = %s, want %s", got, defaultWebFetchToolExecTimeout)
	}
}

func TestGetToolExecTimeoutUsesConfiguredWebFetchTimeout(t *testing.T) {
	engine := &AgentEngine{config: &types.AgentConfig{WebFetchToolTimeout: 240}}

	got := engine.getToolExecTimeout(agenttools.ToolWebFetch)

	if got != 240*time.Second {
		t.Fatalf("configured web_fetch timeout = %s, want 240s", got)
	}
}
