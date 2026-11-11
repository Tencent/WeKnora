package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/agent/tools"
	"github.com/Tencent/WeKnora/internal/types"
)

func TestRegisterJavaScriptToolFollowsAllowlist(t *testing.T) {
	svc := &agentService{}

	registry := tools.NewToolRegistry()
	config := &types.AgentConfig{AllowedTools: []string{tools.ToolRunJavaScript}}
	svc.registerJavaScriptTool(context.Background(), registry, config)
	require.Equal(t, []string{tools.ToolRunJavaScript}, registry.ListTools())

	// A second registration attempt is a no-op, not a duplicate.
	svc.registerJavaScriptTool(context.Background(), registry, config)
	require.Equal(t, []string{tools.ToolRunJavaScript}, registry.ListTools())

	other := tools.NewToolRegistry()
	svc.registerJavaScriptTool(context.Background(), other, &types.AgentConfig{AllowedTools: []string{}})
	_, err := other.GetTool(tools.ToolRunJavaScript)
	require.Error(t, err)

	svc.registerJavaScriptTool(context.Background(), other, nil)
	_, err = other.GetTool(tools.ToolRunJavaScript)
	require.Error(t, err)
}
