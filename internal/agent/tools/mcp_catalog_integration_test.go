package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/Tencent/WeKnora/internal/agent/approval"
	"github.com/Tencent/WeKnora/internal/event"
	internalmcp "github.com/Tencent/WeKnora/internal/mcp"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/utils"
	sdkmcp "github.com/mark3labs/mcp-go/mcp"
	sdkserver "github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/require"
)

type proxyApprovalGate struct {
	request  approval.PendingRequest
	reject   bool
	disabled bool
}

func (g *proxyApprovalGate) IsEnabled(context.Context, uint64, string, string) (bool, error) {
	return !g.disabled, nil
}
func (*proxyApprovalGate) NeedsApproval(context.Context, uint64, string, string) bool { return true }

func (g *proxyApprovalGate) RequestAndWait(
	_ context.Context,
	request approval.PendingRequest,
) (approval.Decision, error) {
	g.request = request
	return approval.Decision{Approved: !g.reject, Reason: "rejected by test user"}, nil
}

func TestMCPProxyHTTPApprovalArgumentsAndImages(t *testing.T) {
	utils.SetSSRFWhitelistFromRaw("127.0.0.1")
	t.Cleanup(utils.ResetSSRFWhitelistForTest)
	server := sdkserver.NewMCPServer("catalog-test", "1", sdkserver.WithToolCapabilities(false))
	var calls, requests atomic.Int32
	server.AddTool(
		sdkmcp.NewTool("get_order", sdkmcp.WithNumber("count", sdkmcp.Required())),
		func(_ context.Context, request sdkmcp.CallToolRequest) (*sdkmcp.CallToolResult, error) {
			calls.Add(1)
			count := request.GetArguments()["count"]
			if count == float64(-1) {
				return sdkmcp.NewToolResultError("order not found"), nil
			}
			if count != float64(2) {
				return sdkmcp.NewToolResultError("wrong target arguments"), nil
			}
			return &sdkmcp.CallToolResult{
				Content: []sdkmcp.Content{
					sdkmcp.NewTextContent("order found"),
					sdkmcp.NewImageContent(testBase64PNG, "image/png"),
				},
			}, nil
		},
	)
	transport := sdkserver.NewStreamableHTTPServer(server, sdkserver.WithStateLess(true))
	httpServer := httptest.NewServer(
		http.HandlerFunc(
			func(w http.ResponseWriter, request *http.Request) { requests.Add(1); transport.ServeHTTP(w, request) },
		),
	)
	defer httpServer.Close()
	manager := internalmcp.NewMCPManager(nil)
	defer manager.Shutdown()
	service := &types.MCPService{
		ID:            "orders",
		TenantID:      7,
		Enabled:       true,
		Name:          "Orders",
		URL:           &httpServer.URL,
		TransportType: types.MCPTransportHTTPStreamable,
	}
	ctx := catalogTestContext()
	registry := NewToolRegistry()
	gate := &proxyApprovalGate{}
	_, err := RegisterMCPTools(ctx, registry, []*types.MCPService{service}, manager, gate, 0, nil, nil)
	require.NoError(t, err)
	require.Zero(t, requests.Load())
	discoverPage(ctx, t, registry, map[string]any{"mode": "list_servers"})
	require.Zero(t, requests.Load())
	page := discoverPage(ctx, t, registry, map[string]any{"mode": "list_tools", "server_id": service.ID})
	require.Len(t, page.Tools, 1)
	require.Positive(t, requests.Load())
	require.Zero(t, calls.Load())
	// Listing loads the target, but must not allow execution or approval yet.
	discovery, _ := registry.GetTool(ToolDiscoverMCPTools)
	snapshot, _, err := discovery.(*MCPDiscoverTool).catalog.snapshot(ctx, service.ID, false)
	require.NoError(t, err)
	premature, _ := json.Marshal(map[string]any{
		"tool_ref": mcpToolRef(snapshot[0]), "arguments": map[string]any{"count": 2},
	})
	blocked, err := registry.ExecuteTool(ctx, ToolCallMCPTool, premature)
	require.NoError(t, err)
	require.False(t, blocked.Success)
	require.Contains(t, blocked.Error, "schema has not been described")
	require.Nil(t, registry.MCPCallTarget(ctx, ToolCallMCPTool, premature))
	require.Zero(t, calls.Load())
	require.Empty(t, gate.request.ToolCallID)
	described := describeTool(ctx, t, registry, service.ID, page.Tools[0].Name)
	describedRaw, _ := json.Marshal(map[string]any{
		"tool_ref": described.ToolRef, "arguments": map[string]any{"count": 2},
	})
	require.NotNil(t, registry.MCPCallTarget(ctx, ToolCallMCPTool, describedRaw))
	ctx = WithToolExecContext(
		ctx,
		&ToolExecContext{
			EventBus:           event.NewEventBus(),
			ToolCallID:         "original-proxy-call",
			SessionID:          "session",
			AssistantMessageID: "message",
			ApprovalCtx:        ctx,
		},
	)
	invoke := func(arguments string) *types.ToolResult {
		raw, _ := json.Marshal(
			map[string]any{"tool_ref": described.ToolRef, "arguments": json.RawMessage(arguments)},
		)
		result, err := registry.ExecuteTool(ctx, ToolCallMCPTool, raw)
		require.NoError(t, err)
		return result
	}
	invalid := invoke(`{}`)
	require.False(t, invalid.Success)
	require.Contains(t, invalid.Error, "count")
	require.Zero(t, calls.Load())
	require.Empty(t, gate.request.ToolCallID)
	result := invoke(`{"count":"2"}`)
	require.True(t, result.Success, result.Error)
	require.Contains(t, result.Output, "order found")
	require.Len(t, result.Images, 1)
	require.Contains(t, result.Images[0], testBase64PNG)
	require.NotEmpty(t, result.Data["content_items"])
	require.NotContains(t, result.Output, testBase64PNG)
	require.Equal(t, "original-proxy-call", gate.request.ToolCallID)
	require.Equal(t, "get_order", gate.request.MCPToolName)
	require.Equal(t, "Orders", gate.request.ServiceName)
	require.JSONEq(t, `{"count":2}`, string(gate.request.Args))
	require.Equal(t, int32(1), calls.Load())
	result = invoke(`{"count":-1}`)
	require.False(t, result.Success)
	require.Contains(t, result.Error, "order not found")
	gate.reject = true
	result = invoke(`{"count":2}`)
	require.False(t, result.Success)
	require.Contains(t, result.Error, "rejected by test user")
	require.Equal(t, int32(2), calls.Load())
	gate.disabled = true
	result = invoke(`{"count":2}`)
	require.False(t, result.Success)
	require.Equal(t, int32(2), calls.Load())
}
