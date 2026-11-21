package tools

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Tencent/WeKnora/internal/agent/approval"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

type catalogSessionPolicies struct {
	rules []types.MCPToolSessionRule
}

func (p *catalogSessionPolicies) ListByService(
	_ context.Context, tenantID uint64, serviceID string,
) ([]*types.MCPToolSessionPolicy, error) {
	return []*types.MCPToolSessionPolicy{{
		TenantID: tenantID, ServiceID: serviceID, ToolName: "get_order", AllowedSessions: p.rules,
	}}, nil
}

func TestMCPCatalogIMSessionPolicyAndRevocation(t *testing.T) {
	ctx := types.WithIMConversation(catalogTestContext(), types.IMConversation{
		Platform: "feishu", ChatType: "group", ChatID: "ops",
	})
	policies := &catalogSessionPolicies{rules: []types.MCPToolSessionRule{{
		Platform: "feishu", ChatType: "group", ChatID: "ops",
	}}}
	gate := approval.NewGate(nil, nil, nil).WithSessionPolicies(policies)
	service := &types.MCPService{ID: "orders", Name: "Orders", TenantID: 7, Enabled: true}
	catalog := newMCPCatalog(ctx, []*types.MCPService{service}, gate,
		func(_ context.Context, s *types.MCPService, _ bool) ([]*MCPTool, error) {
			return []*MCPTool{catalogTestTool(s, "get_order", "lookup", gate)}, nil
		}, nil)
	registry := NewToolRegistry()
	installMCPCatalog(registry, catalog)
	page := discoverPage(ctx, t, registry, map[string]any{"mode": "list_tools", "server_id": service.ID})
	require.Len(t, page.Tools, 1)
	described := describeTool(ctx, t, registry, service.ID, "get_order")

	// Revoke an already described tool, without recreating the catalog or gate.
	policies.rules = []types.MCPToolSessionRule{}
	page = discoverPage(ctx, t, registry, map[string]any{"mode": "list_tools", "server_id": service.ID})
	require.Empty(t, page.Tools)
	raw, err := json.Marshal(map[string]any{"tool_ref": described.ToolRef, "arguments": map[string]any{"count": 2}})
	require.NoError(t, err)
	result, err := registry.ExecuteTool(ctx, ToolCallMCPTool, raw)
	require.NoError(t, err)
	require.False(t, result.Success)

	// Check the direct execution boundary too. A nil manager would panic if
	// execution reached the MCP client, so this proves the upstream is untouched.
	tool := catalogTestTool(service, "get_order", "lookup", gate)
	result, err = tool.Execute(ctx, json.RawMessage(`{"count":2}`))
	require.NoError(t, err)
	require.False(t, result.Success)
	require.Equal(t, "MCP tool is disabled", result.Error)
}
