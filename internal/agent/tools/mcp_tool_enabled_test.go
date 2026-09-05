package tools

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/Tencent/WeKnora/internal/agent/approval"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

type staticPolicyGate struct {
	enabled bool
	err     error
}

func (g staticPolicyGate) NeedsApproval(context.Context, uint64, string, string) bool {
	return false
}

func (g staticPolicyGate) IsEnabled(context.Context, uint64, string, string) (bool, error) {
	return g.enabled, g.err
}

func (g staticPolicyGate) RequestAndWait(context.Context, approval.PendingRequest) (approval.Decision, error) {
	return approval.Decision{Approved: true}, nil
}

var _ approval.MCPApproval = staticPolicyGate{}

func TestMCPToolExecuteDisabledByPolicy(t *testing.T) {
	tool := &MCPTool{
		service: &types.MCPService{ID: "svc-1", Name: "weather"},
		mcpTool: &types.MCPTool{Name: "get_forecast"},
		gate:    staticPolicyGate{enabled: false},
	}
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(7))
	result, err := tool.Execute(ctx, json.RawMessage(`{}`))
	require.NoError(t, err)
	require.False(t, result.Success)
	require.Equal(t, "MCP tool is disabled", result.Error)
}

func TestMCPToolExecutePolicyError(t *testing.T) {
	tool := &MCPTool{
		service: &types.MCPService{ID: "svc-1", Name: "weather"},
		mcpTool: &types.MCPTool{Name: "get_forecast"},
		gate:    staticPolicyGate{err: errors.New("db down")},
	}
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(7))
	result, err := tool.Execute(ctx, json.RawMessage(`{}`))
	require.NoError(t, err)
	require.False(t, result.Success)
	require.Contains(t, result.Error, "db down")
}

func TestMCPToolExecuteMissingTenantFailClosed(t *testing.T) {
	tool := &MCPTool{
		service: &types.MCPService{ID: "svc-1", Name: "weather"},
		mcpTool: &types.MCPTool{Name: "get_forecast"},
		gate:    staticPolicyGate{enabled: true},
	}
	result, err := tool.Execute(context.Background(), json.RawMessage(`{}`))
	require.NoError(t, err)
	require.False(t, result.Success)
	require.Equal(t, "MCP tool is disabled", result.Error)
}
