package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/event"
	internalmcp "github.com/Tencent/WeKnora/internal/mcp"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/utils"
	sdkmcp "github.com/mark3labs/mcp-go/mcp"
	sdkserver "github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/require"
)

func TestMCPDirectExposurePreservesEveryDescriptionAndRawSchema(t *testing.T) {
	ctx := catalogTestContext()
	server := &types.MCPService{ID: "orders", Name: "订单", Enabled: true}
	gate := &catalogPolicy{disabled: map[string]bool{"orders/disabled": true}}
	description := strings.Repeat("Detailed capability. ", 40) + "END-OF-DESCRIPTION"
	schema := json.RawMessage(
		`{"type":"object","definitions":{"id":{"type":"string"}},` +
			`"properties":{"id":{"$ref":"#/definitions/id"}},` +
			`"oneOf":[{"required":["id"]}],"additionalProperties":false}`,
	)
	c := newMCPCatalog(
		ctx,
		[]*types.MCPService{server},
		gate,
		func(context.Context, *types.MCPService, bool) ([]*MCPTool, error) {
			var tools []*MCPTool
			for _, name := range []string{
				"查询", "查询!", "get-order", "get_order", "Get_Order",
				strings.Repeat("long", 30) + "a", strings.Repeat("long", 30) + "b", "disabled",
			} {
				tool := NewMCPTool(
					server,
					&types.MCPTool{Name: name, Description: description, InputSchema: schema},
					nil,
					gate,
					0,
				)
				tool.serverInstructions = "Use customer IDs, never order display titles."
				tools = append(tools, tool)
			}
			return tools, nil
		},
		nil,
	)
	r := NewToolRegistry()
	installMCPCatalog(r, c)
	r.prepareMCPTools(ctx, time.Second)
	defs := r.GetModelFunctionDefinitions()
	require.Len(t, defs, 9, "seven distinct authorized tools plus two recovery tools")
	names := map[string]bool{}
	for _, def := range defs {
		if def.Name == ToolCallMCPTool || def.Name == ToolDiscoverMCPTools {
			continue
		}
		require.False(t, names[def.Name], "Unicode/case/truncation must not alias tools")
		names[def.Name] = true
		require.Regexp(t, `^[a-zA-Z0-9_-]{1,64}$`, def.Name)
		require.Contains(t, def.Description, description)
		require.JSONEq(t, string(schema), string(def.Parameters))
	}
	discovery, _ := r.GetTool(ToolDiscoverMCPTools)
	require.Contains(t, discovery.Description(), "Use customer IDs")
	require.Contains(t, discovery.Description(), `"status":"ready"`)
	before, _ := json.Marshal(defs)
	r.RefreshMCPTools(ctx)
	after, _ := json.Marshal(r.GetModelFunctionDefinitions())
	require.Equal(t, string(before), string(after), "unchanged snapshots must produce stable model prefixes")
	for _, tool := range c.servers["orders"].tools {
		if tool.mcpTool.Name == "disabled" {
			require.NotContains(t, names, mcpRegisteredName(tool))
		}
	}
}

func TestMCPDirectExecutionPreservesValidationApprovalAndImages(t *testing.T) {
	utils.SetSSRFWhitelistFromRaw("127.0.0.1")
	t.Cleanup(utils.ResetSSRFWhitelistForTest)
	var calls atomic.Int32
	server := sdkserver.NewMCPServer("Orders", "1", sdkserver.WithToolCapabilities(false))
	server.AddTool(
		sdkmcp.Tool{
			Name: "get_order",
			RawInputSchema: json.RawMessage(
				`{"type":"object","properties":{"id":{"type":"string"},` +
					`"count":{"type":"integer"}},"oneOf":[{"required":["id"]},` +
					`{"required":["count"]}],"additionalProperties":false}`,
			),
		},
		func(_ context.Context, request sdkmcp.CallToolRequest) (*sdkmcp.CallToolResult, error) {
			calls.Add(1)
			if request.GetArguments()["count"] != float64(2) {
				return sdkmcp.NewToolResultError("bad conversion"), nil
			}
			return &sdkmcp.CallToolResult{
				Content: []sdkmcp.Content{
					sdkmcp.NewTextContent("found"),
					sdkmcp.NewImageContent(testBase64PNG, "image/png"),
				},
			}, nil
		},
	)
	httpServer := httptest.NewServer(sdkserver.NewStreamableHTTPServer(server, sdkserver.WithStateLess(true)))
	defer httpServer.Close()
	manager := internalmcp.NewMCPManager(nil)
	defer manager.Shutdown()
	service := &types.MCPService{
		ID:            "orders",
		Name:          "Orders",
		Enabled:       true,
		URL:           &httpServer.URL,
		TransportType: types.MCPTransportHTTPStreamable,
	}
	ctx := catalogTestContext()
	registry := NewToolRegistry()
	gate := &proxyApprovalGate{}
	_, err := RegisterMCPTools(ctx, registry, []*types.MCPService{service}, manager, gate, 0, nil, nil)
	require.NoError(t, err)
	registry.prepareMCPTools(ctx, time.Second)
	name := MCPToolNamesByServiceID(registry)[service.ID][0]
	ctx = WithToolExecContext(
		ctx,
		&ToolExecContext{EventBus: event.NewEventBus(), ToolCallID: "direct-call", ApprovalCtx: ctx},
	)
	for _, args := range []string{`{}`, `{"id":"42","count":2}`, `{"count":2,"extra":true}`} {
		result, err := registry.ExecuteTool(ctx, name, json.RawMessage(args))
		require.NoError(t, err)
		require.False(t, result.Success, args)
	}
	require.Empty(t, gate.request.ToolCallID)
	require.Zero(t, calls.Load())
	result, err := registry.ExecuteTool(ctx, name, json.RawMessage(`{"count":"2"}`))
	require.NoError(t, err)
	require.True(t, result.Success, result.Error)
	require.Len(t, result.Images, 1)
	require.Contains(t, result.Images[0], testBase64PNG)
	require.Equal(t, name, gate.request.RegisteredToolName)
	require.Equal(t, "get_order", gate.request.MCPToolName)
	require.Equal(t, "direct-call", gate.request.ToolCallID)
	require.JSONEq(t, `{"count":2}`, string(gate.request.Args))
	gate.reject = true
	result, err = registry.ExecuteTool(ctx, name, json.RawMessage(`{"count":2}`))
	require.NoError(t, err)
	require.False(t, result.Success)
	require.EqualValues(t, 1, calls.Load())
}

func TestMCPDirectExposureLateStartupAndNonInteractiveContext(t *testing.T) {
	ctx, cancel := context.WithCancel(catalogTestContext())
	defer cancel()
	ctx = WithToolExecContext(ctx, &ToolExecContext{SessionID: "must-not-propagate"})
	fast := &types.MCPService{ID: "fast", Name: "Fast", Enabled: true}
	slow := &types.MCPService{ID: "slow", Name: "Slow", Enabled: true}
	release := make(chan struct{})
	c := newMCPCatalog(
		ctx,
		[]*types.MCPService{fast, slow},
		nil,
		func(ctx context.Context, service *types.MCPService, _ bool) ([]*MCPTool, error) {
			if _, ok := ToolExecFromContext(ctx); ok {
				return nil, errors.New("startup leaked interactive context")
			}
			if service.ID == "slow" {
				select {
				case <-release:
				case <-ctx.Done():
					return nil, ctx.Err()
				}
			}
			return []*MCPTool{catalogTestTool(service, "lookup", "find records", nil)}, nil
		},
		nil,
	)
	r := NewToolRegistry()
	installMCPCatalog(r, c)
	start := time.Now()
	r.prepareMCPTools(ctx, 100*time.Millisecond)
	require.Less(t, time.Since(start), time.Second)
	require.Len(t, MCPToolNamesByServiceID(r)[fast.ID], 1)
	require.Empty(t, MCPToolNamesByServiceID(r)[slow.ID])
	close(release)
	select {
	case <-c.preloadDone:
	case <-time.After(time.Second):
		t.Fatal("startup never completed")
	}
	r.RefreshMCPTools(ctx)
	require.Len(t, MCPToolNamesByServiceID(r)[slow.ID], 1)
}

func TestMCPDirectExposureRevalidatesIdentityPolicyAndSchema(t *testing.T) {
	ctx := catalogTestContext()
	service := &types.MCPService{ID: "s", Name: "Orders", Enabled: true}
	current := *service
	gate := &catalogPolicy{disabled: map[string]bool{}}
	schema := json.RawMessage(`{"type":"object","properties":{"id":{"type":"string"}},"required":["id"]}`)
	c := newMCPCatalog(
		ctx,
		[]*types.MCPService{service},
		gate,
		func(_ context.Context, s *types.MCPService, _ bool) ([]*MCPTool, error) {
			return []*MCPTool{NewMCPTool(s, &types.MCPTool{Name: "get", InputSchema: schema}, nil, gate, 0)}, nil
		},
		func(context.Context, uint64, string) (*types.MCPService, error) {
			snapshot := current
			return &snapshot, nil
		},
	)
	r := NewToolRegistry()
	installMCPCatalog(r, c)
	r.prepareMCPTools(ctx, time.Second)
	name := MCPToolNamesByServiceID(r)[service.ID][0]
	bound, _ := r.GetTool(name)
	// Keep the advertised definition and prove it cannot cross identities.
	other := types.WithPrincipal(ctx, types.Principal{Type: types.PrincipalWebUser, ID: "someone-else"})
	result, err := r.ExecuteTool(other, name, json.RawMessage(`{"id":"1"}`))
	require.NoError(t, err)
	require.False(t, result.Success)
	gate.disabled["s/get"] = true
	result, err = r.ExecuteTool(ctx, name, json.RawMessage(`{"id":"1"}`))
	require.NoError(t, err)
	require.False(t, result.Success)
	r.RefreshMCPTools(ctx)
	require.Empty(t, MCPToolNamesByServiceID(r)[service.ID])
	require.Len(t, r.GetModelFunctionDefinitions(), 1, "withdraw the proxy when no callable tools remain")
	gate.disabled["s/get"] = false
	current.UpdatedAt = current.UpdatedAt.Add(time.Second)
	schema = json.RawMessage(`{"type":"object","properties":{"count":{"type":"number"}},"required":["count"]}`)
	result, err = bound.Execute(ctx, json.RawMessage(`{"id":"1"}`))
	require.NoError(t, err)
	require.False(t, result.Success)
	require.Contains(t, result.Error, "definition changed")
	r.RefreshMCPTools(ctx)
	require.Equal(
		t,
		[]string{name},
		MCPToolNamesByServiceID(r)[service.ID],
		"schema changes keep the raw tool identity",
	)
	current.Enabled = false
	result, err = r.ExecuteTool(ctx, name, json.RawMessage(`{"count":1}`))
	require.NoError(t, err)
	require.False(t, result.Success)
	r.RefreshMCPTools(ctx)
	require.Empty(t, MCPToolNamesByServiceID(r)[service.ID])
}

func TestMCPDeferredSourcesStaySmallAndOnlyDescribedToolsLoad(t *testing.T) {
	ctx := catalogTestContext()
	service := &types.MCPService{ID: "many", Name: "Orders", Description: "Orders and shipping", Enabled: true}
	catalog := newMCPCatalog(
		ctx,
		[]*types.MCPService{service},
		nil,
		func(context.Context, *types.MCPService, bool) ([]*MCPTool, error) {
			result := make([]*MCPTool, 1000)
			for i := range result {
				result[i] = NewMCPTool(
					service,
					&types.MCPTool{
						Name:        fmt.Sprintf("tool_%d", i),
						Description: strings.Repeat("DO-NOT-SEND-ALL-DESCRIPTIONS ", 50),
						InputSchema: json.RawMessage(`{"type":"object","properties":{"id":{"type":"string"}}}`),
					},
					nil,
					nil,
					0,
				)
			}
			return result, nil
		},
		nil,
	)
	r := NewToolRegistry()
	installMCPCatalog(r, catalog)
	r.PrepareMCPTools(ctx)
	initial, err := json.Marshal(r.GetModelFunctionDefinitions())
	require.NoError(t, err)
	require.Len(t, r.GetModelFunctionDefinitions(), 1)
	require.Equal(t, ToolDiscoverMCPTools, r.GetModelFunctionDefinitions()[0].Name)
	require.Less(t, len(initial), 8192)
	require.Contains(t, string(initial), "Orders and shipping")
	require.NotContains(t, string(initial), "DO-NOT-SEND-ALL-DESCRIPTIONS")
	discoverPage(ctx, t, r, map[string]any{"mode": "list_tools", "server_id": "many"})
	r.RefreshMCPTools(ctx)
	require.Len(t, r.GetModelFunctionDefinitions(), 1, "listing must not expose the call proxy")
	failed, err := r.ExecuteTool(ctx, ToolDiscoverMCPTools,
		json.RawMessage(`{"mode":"describe","server_id":"many","tool_name":"guessed_tool"}`))
	require.NoError(t, err)
	require.False(t, failed.Success)
	r.RefreshMCPTools(ctx)
	require.Len(t, r.GetModelFunctionDefinitions(), 1, "a failed describe must not expose the call proxy")
	failed, err = r.ExecuteTool(ctx, ToolCallMCPTool,
		json.RawMessage(`{"tool_ref":"amap-maps__amap_geo","arguments":{}}`))
	require.NoError(t, err)
	require.False(t, failed.Success, "the execution gate must still reject hallucinated calls")
	require.Contains(t, failed.Error, "copy its tool_ref verbatim")
	result, err := r.ExecuteTool(
		ctx,
		ToolDiscoverMCPTools,
		json.RawMessage(`{"mode":"describe","server_id":"many","tool_name":"tool_500"}`),
	)
	require.NoError(t, err)
	require.True(t, result.Success)
	require.Len(t, r.GetModelFunctionDefinitions(), 1, "a tool call must not mutate the registry")
	r.RefreshMCPTools(ctx)
	loaded := r.GetModelFunctionDefinitions()
	require.Len(t, loaded, 3)
	var described map[string]any
	require.NoError(t, json.Unmarshal([]byte(result.Output), &described))
	tool, err := r.GetTool(described["function_name"].(string))
	require.NoError(t, err)
	require.Contains(t, tool.Description(), "DO-NOT-SEND-ALL-DESCRIPTIONS")
}

func TestMCPHistoryRestoresAdvertisedFunctions(t *testing.T) {
	restore := func(t *testing.T, history func(*MCPTool) chat.Message) {
		t.Helper()
		ctx, r, c, _, _ := catalogFixture(t, 1)
		snapshot, _, err := c.snapshot(ctx, "server-1", false)
		require.NoError(t, err)
		require.Len(t, snapshot, 1)
		require.Len(t, r.GetModelFunctionDefinitions(), 2)
		r.mcpPrepared = true
		r.RememberMCPHistory([]chat.Message{history(snapshot[0])})
		r.RefreshMCPTools(ctx)
		_, err = r.GetTool(mcpRegisteredName(snapshot[0]))
		require.NoError(t, err)
	}

	restore(t, func(tool *MCPTool) chat.Message {
		return chat.Message{
			Role: "assistant",
			ToolCalls: []chat.ToolCall{{
				Function: chat.FunctionCall{Name: mcpRegisteredName(tool)},
			}},
		}
	})
	restore(t, func(tool *MCPTool) chat.Message {
		return chat.Message{
			Role: "assistant",
			ToolCalls: []chat.ToolCall{{
				Function: chat.FunctionCall{
					Name:      ToolCallMCPTool,
					Arguments: `{"tool_ref":"` + mcpToolRef(tool) + `"}`,
				},
			}},
		}
	})
}
