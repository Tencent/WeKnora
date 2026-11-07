package tools

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/Tencent/WeKnora/internal/event"
	internalmcp "github.com/Tencent/WeKnora/internal/mcp"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/utils"
	sdkmcp "github.com/mark3labs/mcp-go/mcp"
	sdkserver "github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/require"
)

func TestMCPCallGuardCoversDirectAndProxyBeforeApproval(t *testing.T) {
	utils.SetSSRFWhitelistFromRaw("127.0.0.1")
	t.Cleanup(utils.ResetSSRFWhitelistForTest)
	for _, direct := range []bool{true, false} {
		t.Run(map[bool]string{true: "direct", false: "proxy"}[direct], func(t *testing.T) {
			var executions atomic.Int32
			server := sdkserver.NewMCPServer("guard", "1", sdkserver.WithToolCapabilities(false))
			server.AddTool(
				sdkmcp.NewTool("get_order", sdkmcp.WithNumber("count", sdkmcp.Required())),
				func(context.Context, sdkmcp.CallToolRequest) (*sdkmcp.CallToolResult, error) {
					executions.Add(1)
					return sdkmcp.NewToolResultText("executed"), nil
				},
			)
			transport := httptest.NewServer(sdkserver.NewStreamableHTTPServer(server, sdkserver.WithStateLess(true)))
			defer transport.Close()
			manager := internalmcp.NewMCPManager(nil)
			defer manager.Shutdown()
			service := &types.MCPService{
				ID:            "service-id",
				Name:          "Orders",
				Enabled:       true,
				URL:           &transport.URL,
				TransportType: types.MCPTransportHTTPStreamable,
			}
			ctx := catalogTestContext()
			registry := NewToolRegistry()
			guardCalls := 0
			registry.SetMCPCallGuard(
				func(_ context.Context, serviceID, toolName string, args json.RawMessage) (*types.ToolResult, error) {
					guardCalls++
					require.Equal(t, service.ID, serviceID)
					require.Equal(t, "get_order", toolName)
					require.JSONEq(t, `{"count":2}`, string(args), "guard sees normalized native arguments")
					return &types.ToolResult{
						Success: false,
						Error:   "delegated",
						Data:    map[string]interface{}{"code": "delegated_call_in_progress"},
					}, nil
				},
			)
			gate := &proxyApprovalGate{reject: true}
			_, err := RegisterMCPTools(ctx, registry, []*types.MCPService{service}, manager, gate, 0, nil, nil)
			require.NoError(t, err)
			name, raw := "", json.RawMessage(`{"count":"2"}`)
			if direct {
				registry.PrepareMCPToolsDirect(ctx)
				name = MCPToolNamesByServiceID(registry)[service.ID][0]
			} else {
				described := describeTool(ctx, t, registry, service.ID, "get_order")
				name = ToolCallMCPTool
				raw, err = json.Marshal(map[string]any{"tool_ref": described.ToolRef, "arguments": raw})
				require.NoError(t, err)
			}
			ctx = WithToolExecContext(
				ctx,
				&ToolExecContext{
					EventBus:           event.NewEventBus(),
					ToolCallID:         "parent-call",
					SessionID:          "parent",
					AssistantMessageID: "message",
					ApprovalCtx:        ctx,
				},
			)
			result, err := registry.ExecuteTool(ctx, name, raw)
			require.NoError(t, err)
			require.False(t, result.Success)
			require.Nil(t, result.Suspend)
			require.Equal(t, "delegated_call_in_progress", result.Data["code"])
			require.Equal(t, 1, guardCalls)
			require.Empty(t, gate.request.ToolCallID, "no second approval is opened")
			require.Zero(t, executions.Load())
			registry.RegisterTool(&mockTool{name: "independent", parameters: json.RawMessage(`{"type":"object"}`)})
			_, err = registry.ExecuteTool(ctx, "independent", json.RawMessage(`{}`))
			require.NoError(t, err)
			require.Equal(t, 1, guardCalls, "ordinary tools do not invoke this fence")
			registry.SetMCPCallGuard(nil)
			result, err = registry.ExecuteTool(ctx, name, raw)
			require.NoError(t, err)
			require.False(t, result.Success)
			require.Equal(t, "parent-call", gate.request.ToolCallID, "unfenced calls still require their own approval")
			require.Zero(t, executions.Load(), "rejection is not replaced with child authorization")
		})
	}
}

func TestMCPCallGuardLeavesExistingParentApprovalLifecycleIntact(t *testing.T) {
	ctx := withMCPCallGuard(
		context.Background(),
		func(context.Context, string, string, json.RawMessage) (*types.ToolResult, error) {
			t.Fatal("already-issued approval must resume through its original lifecycle")
			return nil, nil
		},
	)
	ctx = WithToolExecContext(ctx, &ToolExecContext{Answered: &types.AgentPrompt{ID: "parent-prompt"}})
	result, err := checkMCPCallGuard(ctx, "service", "tool", json.RawMessage(`{}`))
	require.NoError(t, err)
	require.Nil(t, result)
}
