package tools

import (
	"context"
	"encoding/json"
	"errors"
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

type modifiedArgsGate struct {
	proxyApprovalGate
	modified json.RawMessage
}

func (g *modifiedArgsGate) RequestAndWait(
	_ context.Context,
	request approval.PendingRequest,
) (approval.Decision, error) {
	g.request = request
	return approval.Decision{Approved: true, ModifiedArgs: g.modified}, nil
}

func TestMCPCatalogPreservesAndValidatesRawSchemaOverHTTP(t *testing.T) {
	utils.SetSSRFWhitelistFromRaw("127.0.0.1")
	t.Cleanup(utils.ResetSSRFWhitelistForTest)
	schema := json.RawMessage(`{
  "$schema": "http://json-schema.org/draft-07/schema#",
  "type": "object",
  "definitions": {
    "contact": {
      "type": "string",
      "minLength": 1
    }
  },
  "properties": {
    "email": {
      "$ref": "#/definitions/contact"
    },
    "phone": {
      "$ref": "#/definitions/contact"
    }
  },
  "oneOf": [
    {
      "required": [
        "email"
      ]
    },
    {
      "required": [
        "phone"
      ]
    }
  ],
  "additionalProperties": false
}`)
	server := sdkserver.NewMCPServer(
		"catalog",
		"1",
		sdkserver.WithToolCapabilities(false),
		sdkserver.WithPaginationLimit(1),
	)
	var calls atomic.Int32
	received := make(chan map[string]any, 4)
	handler := func(_ context.Context, request sdkmcp.CallToolRequest) (*sdkmcp.CallToolResult, error) {
		calls.Add(1)
		received <- request.GetArguments()
		return sdkmcp.NewToolResultText("ok"), nil
	}
	server.AddTool(sdkmcp.Tool{Name: "contact", RawInputSchema: schema}, handler)
	server.AddTool(sdkmcp.NewTool("other"), handler)
	httpServer := httptest.NewServer(sdkserver.NewStreamableHTTPServer(server, sdkserver.WithStateLess(true)))
	defer httpServer.Close()
	manager := internalmcp.NewMCPManager(nil)
	defer manager.Shutdown()
	service := &types.MCPService{
		ID:            "test",
		Name:          "test",
		TenantID:      7,
		Enabled:       true,
		URL:           &httpServer.URL,
		TransportType: types.MCPTransportHTTPStreamable,
	}
	ctx, registry := catalogTestContext(), NewToolRegistry()
	gate := &modifiedArgsGate{}
	_, err := RegisterMCPTools(ctx, registry, []*types.MCPService{service}, manager, gate, 0, nil)
	require.NoError(t, err)
	page := discoverPage(ctx, t, registry, map[string]any{"mode": "list_tools", "server_id": "test"})
	require.Len(t, page.Tools, 2, "read every protocol page")
	result, err := registry.ExecuteTool(ctx, ToolDiscoverMCPTools, json.RawMessage(`{
  "mode": "describe",
  "server_id": "test",
  "tool_name": "contact"
}`))
	require.NoError(t, err)
	require.True(t, result.Success, result.Error)
	var described struct {
		InputSchema json.RawMessage `json:"input_schema"`
		ToolRef     string          `json:"tool_ref"`
	}
	require.NoError(t, json.Unmarshal([]byte(result.Output), &described))
	require.JSONEq(t, string(schema), string(described.InputSchema))
	ctx = WithToolExecContext(
		ctx,
		&ToolExecContext{EventBus: event.NewEventBus(), ToolCallID: "proxy", ApprovalCtx: ctx},
	)
	invoke := func(ref, args string) *types.ToolResult {
		raw, _ := json.Marshal(map[string]any{"tool_ref": ref, "arguments": json.RawMessage(args)})
		result, err := registry.ExecuteTool(ctx, ToolCallMCPTool, raw)
		require.NoError(t, err)
		return result
	}
	for _, args := range []string{`{}`, `{"email":"x","phone":"y"}`, `{"email":""}`, `{"email":"x","extra":true}`} {
		result := invoke(described.ToolRef, args)
		require.False(t, result.Success, args)
		require.Zero(t, calls.Load())
		require.Empty(t, gate.request.ToolCallID, "invalid input must not request approval")
	}
	gate.modified = json.RawMessage(`{}`)
	require.False(t, invoke(described.ToolRef, `{"email":"x"}`).Success)
	require.Zero(t, calls.Load(), "invalid approval edits must not reach the server")
	gate.modified = json.RawMessage(`{"phone":"123"}`)
	result = invoke(described.ToolRef, `{"email":"x"}`)
	require.True(t, result.Success, result.Error)
	require.Equal(t, map[string]any{"phone": "123"}, <-received, "approval replaces rather than merges arguments")
	require.EqualValues(t, 1, calls.Load())
	// Change only a root constraint. The old typed schema round-trip lost it,
	// so the old reference incorrectly survived a refresh.
	var changed map[string]any
	require.NoError(t, json.Unmarshal(schema, &changed))
	changed["oneOf"] = []any{map[string]any{"required": []string{"email"}}}
	newSchema, _ := json.Marshal(changed)
	server.AddTool(sdkmcp.Tool{Name: "contact", RawInputSchema: newSchema}, handler)
	page = discoverPage(ctx, t, registry, map[string]any{"mode": "list_tools", "server_id": "test", "refresh": true})
	require.NotEqual(t, described.ToolRef, page.Tools[0].ToolRef)
	require.False(t, invoke(described.ToolRef, `{"email":"x"}`).Success)
	require.EqualValues(t, 1, calls.Load())
}

type countingCatalogPolicy struct {
	catalogPolicy
	checks, batches int
}

func (g *countingCatalogPolicy) IsEnabled(ctx context.Context, tenant uint64, server, name string) (bool, error) {
	g.checks++
	return g.catalogPolicy.IsEnabled(ctx, tenant, server, name)
}

func (g *countingCatalogPolicy) EnabledTools(
	_ context.Context,
	_ uint64,
	server string,
	names []string,
) (map[string]bool, error) {
	g.batches++
	if g.err != nil {
		return nil, g.err
	}
	result := make(map[string]bool, len(names))
	for _, name := range names {
		result[name] = !g.disabled[server+"/"+name]
	}
	return result, nil
}

func TestMCPCatalogPolicyQueriesAreBounded(t *testing.T) {
	ctx, registry, catalog, _, _ := catalogFixture(t, 1000)
	gate := &countingCatalogPolicy{catalogPolicy: catalogPolicy{disabled: map[string]bool{}}}
	catalog.gate = gate
	page := discoverPage(ctx, t, registry, map[string]any{"mode": "list_tools", "server_id": "server-1", "limit": 1})
	require.Equal(t, 1, gate.batches)
	require.Zero(t, gate.checks)
	result, err := registry.ExecuteTool(ctx, ToolDiscoverMCPTools, json.RawMessage(`{
  "mode": "describe",
  "server_id": "server-1",
  "tool_name": "tool_000"
}`))
	require.NoError(t, err)
	require.True(t, result.Success, result.Error)
	require.Equal(t, 1, gate.checks)
	proxy, err := registry.GetTool(ToolCallMCPTool)
	require.NoError(t, err)
	raw, _ := json.Marshal(map[string]any{"tool_ref": page.Tools[0].ToolRef, "arguments": map[string]any{"count": 1}})
	_, _, err = proxy.(*MCPCallTool).resolve(ctx, raw)
	require.NoError(t, err)
	require.Equal(t, 2, gate.checks)
	require.Equal(t, 1, gate.batches)
	gate.disabled["server-1/tool_000"] = true
	_, _, err = proxy.(*MCPCallTool).resolve(ctx, raw)
	require.ErrorContains(t, err, "no longer available")
	result, err = registry.ExecuteTool(ctx, ToolDiscoverMCPTools, json.RawMessage(`{
  "mode": "describe",
  "server_id": "server-1",
  "tool_name": "tool_000"
}`))
	require.NoError(t, err)
	require.False(t, result.Success)
	gate.err = errors.New("private database error")
	result, err = registry.ExecuteTool(ctx, ToolDiscoverMCPTools, json.RawMessage(`{
  "mode": "list_tools",
  "server_id": "server-1"
}`))
	require.NoError(t, err)
	require.False(t, result.Success)
	require.NotContains(t, result.Error, "private database")
}

func TestMCPServerCursorSurvivesStatusChanges(t *testing.T) {
	ctx, registry, catalog, _, _ := catalogFixture(t, 1)
	first := discoverPage(ctx, t, registry, map[string]any{"mode": "list_servers", "limit": 1})
	discoverPage(ctx, t, registry, map[string]any{"mode": "list_tools", "server_id": first.Servers[0].ServerID})
	second := discoverPage(
		ctx,
		t,
		registry,
		map[string]any{"mode": "list_servers", "limit": 1, "cursor": first.NextCursor},
	)
	require.False(t, second.HasMore)
	require.NotEqual(t, first.Servers[0].ServerID, second.Servers[0].ServerID)
	delete(catalog.servers, second.Servers[0].ServerID)
	raw, _ := json.Marshal(map[string]any{"mode": "list_servers", "cursor": first.NextCursor})
	result, err := registry.ExecuteTool(ctx, ToolDiscoverMCPTools, raw)
	require.NoError(t, err)
	require.False(t, result.Success, "membership changes still invalidate the cursor")
}
