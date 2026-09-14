package service

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/agent/tools"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAgentWebToolsFollowRuntimeSwitch(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		registry := tools.NewToolRegistry()
		svc := &agentService{}
		config := &types.AgentConfig{
			AllowedTools: []string{tools.ToolWebSearch, tools.ToolWebFetch}, WebSearchEnabled: enabled,
		}
		require.NoError(t, svc.registerTools(t.Context(), registry, config, nil, nil, "web-session"))
		for _, name := range []string{tools.ToolWebSearch, tools.ToolWebFetch} {
			_, err := registry.GetTool(name)
			assert.Equal(t, enabled, err == nil, name)
		}
		assert.NotContains(t, registry.ListTools(), tools.ToolKnowledgeSearch)
		assert.NotContains(t, registry.ListTools(), tools.ToolGrepChunks)
	}
}
