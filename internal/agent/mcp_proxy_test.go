package agent

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"

	agenttools "github.com/Tencent/WeKnora/internal/agent/tools"
	"github.com/Tencent/WeKnora/internal/event"
	internalmcp "github.com/Tencent/WeKnora/internal/mcp"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/utils"
	sdkmcp "github.com/mark3labs/mcp-go/mcp"
	sdkserver "github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/require"
)

func TestAgentMCPProxyKeepsTargetEventsAndProtocolHistory(t *testing.T) {
	utils.SetSSRFWhitelistFromRaw("127.0.0.1")
	t.Cleanup(utils.ResetSSRFWhitelistForTest)
	server := sdkserver.NewMCPServer("agent-proxy-test", "1", sdkserver.WithToolCapabilities(false))
	server.AddTool(
		sdkmcp.NewTool("get_order", sdkmcp.WithString("id", sdkmcp.Required())),
		func(context.Context, sdkmcp.CallToolRequest) (*sdkmcp.CallToolResult, error) {
			return sdkmcp.NewToolResultText("order found"), nil
		},
	)
	httpServer := httptest.NewServer(sdkserver.NewStreamableHTTPServer(server, sdkserver.WithStateLess(true)))
	defer httpServer.Close()
	manager := internalmcp.NewMCPManager(nil)
	defer manager.Shutdown()
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(7))
	registry := agenttools.NewToolRegistry()
	_, err := agenttools.RegisterMCPTools(
		ctx,
		registry,
		[]*types.MCPService{
			{
				ID:            "orders",
				TenantID:      7,
				Name:          "Orders",
				Enabled:       true,
				URL:           &httpServer.URL,
				TransportType: types.MCPTransportHTTPStreamable,
			},
		},
		manager,
		nil,
		0,
		nil,
	)
	require.NoError(t, err)
	engine := newTestEngine(t, &mockChat{})
	engine.toolRegistry = registry
	var starts []event.AgentToolCallData
	var outcomes []event.AgentToolResultData
	engine.eventBus.On(event.EventAgentToolCall, func(_ context.Context, evt event.Event) error {
		starts = append(starts, evt.Data.(event.AgentToolCallData))
		return nil
	})
	engine.eventBus.On(event.EventAgentToolResult, func(_ context.Context, evt event.Event) error {
		outcomes = append(outcomes, evt.Data.(event.AgentToolResultData))
		return nil
	})
	run := func(id, name, args string) types.ToolCall {
		return engine.runToolCall(
			ctx,
			types.LLMToolCall{ID: id, Function: types.FunctionCall{Name: name, Arguments: args}},
			0,
			1,
			1,
			"session",
			"message",
		)
	}
	listing := run("list", agenttools.ToolDiscoverMCPTools, `{"mode":"list_tools","server_id":"orders"}`)
	require.True(t, listing.Result.Success, listing.Result.Error)
	var page struct {
		Tools []struct {
			ToolRef string `json:"tool_ref"`
		} `json:"tools"`
	}
	require.NoError(t, json.Unmarshal([]byte(listing.Result.Output), &page))
	require.Len(t, page.Tools, 1)
	definition := run("describe", agenttools.ToolDiscoverMCPTools, `{
  "mode": "describe",
  "server_id": "orders",
  "tool_name": "get_order"
}`)
	require.True(t, definition.Result.Success, definition.Result.Error)
	var described struct {
		ToolRef string `json:"tool_ref"`
	}
	require.NoError(t, json.Unmarshal([]byte(definition.Result.Output), &described))
	raw, _ := json.Marshal(map[string]any{"tool_ref": described.ToolRef, "arguments": map[string]any{"id": "42"}})
	call := run("model-proxy-id", agenttools.ToolCallMCPTool, string(raw))
	require.True(t, call.Result.Success, call.Result.Error)
	require.Equal(t, agenttools.ToolCallMCPTool, call.Name)
	require.NotNil(t, call.Target)
	require.Equal(t, "get_order", call.Target.ToolName)
	require.Equal(t, "mcp_orders_get_order", starts[len(starts)-1].ToolName)
	require.Equal(t, map[string]any{"id": "42"}, starts[len(starts)-1].Arguments)
	engine.emitToolOutcome(ctx, call, 1, "session")
	require.Equal(t, "mcp_orders_get_order", outcomes[0].ToolName)
	require.Equal(t, "model-proxy-id", outcomes[0].ToolCallID)
	messages := engine.appendToolResults(nil, types.AgentStep{ToolCalls: []types.ToolCall{call}})
	require.Equal(t, agenttools.ToolCallMCPTool, messages[0].ToolCalls[0].Function.Name)
	require.JSONEq(t, string(raw), messages[0].ToolCalls[0].Function.Arguments)
	require.Equal(t, "model-proxy-id", messages[1].ToolCallID)
	require.Len(t, engine.buildToolsForLLM(), 2, "discovery must not advertise individual MCP schemas")
}
