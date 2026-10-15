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

func TestAssistantMCPAccessAcrossProxyAndDirectCalls(t *testing.T) {
	utils.SetSSRFWhitelistFromRaw("127.0.0.1")
	t.Cleanup(utils.ResetSSRFWhitelistForTest)
	var calls, requests atomic.Int32
	server := sdkserver.NewMCPServer("external", "1", sdkserver.WithToolCapabilities(false))
	server.AddTool(sdkmcp.NewTool("write", sdkmcp.WithNumber("value", sdkmcp.Required())),
		func(_ context.Context, req sdkmcp.CallToolRequest) (*sdkmcp.CallToolResult, error) {
			calls.Add(1)
			if req.GetArguments()["value"] != float64(2) {
				return sdkmcp.NewToolResultError("approval edits were lost"), nil
			}
			return sdkmcp.NewToolResultText("written"), nil
		})
	transport := sdkserver.NewStreamableHTTPServer(server, sdkserver.WithStateLess(true))
	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		transport.ServeHTTP(w, r)
	}))
	t.Cleanup(httpServer.Close)
	manager := internalmcp.NewMCPManager(nil)
	t.Cleanup(manager.Shutdown)
	service := &types.MCPService{
		ID: "external", Name: "External", TenantID: 7, Enabled: true,
		URL: &httpServer.URL, TransportType: types.MCPTransportHTTPStreamable,
	}
	ctx := catalogTestContext()
	interactive := WithToolExecContext(ctx, &ToolExecContext{
		SessionID: "s1", UserID: "web_user:catalog-user", ToolCallID: "call-1",
		EventBus: event.NewEventBus(), Interactive: true,
	})

	t.Run("read-only never connects or discovers", func(t *testing.T) {
		registry := NewToolRegistry()
		registry.SetAssistantAccess(types.AssistantAccessReadOnly, &fakeApprover{})
		count, err := RegisterMCPTools(ctx, registry, []*types.MCPService{service}, manager,
			&catalogPolicy{}, 0, nil, nil)
		require.NoError(t, err)
		require.Zero(t, count)
		registry.PrepareMCPToolsDirect(ctx)
		require.Empty(t, registry.ListTools())
		require.Zero(t, requests.Load())
	})

	t.Run("full auto skips confirmation but retains MCP enablement", func(t *testing.T) {
		calls.Store(0)
		registry := NewToolRegistry()
		registry.SetAssistantAccess(types.AssistantAccessFullAuto, nil)
		policy := &alwaysAskMCPPolicy{catalogPolicy: catalogPolicy{disabled: map[string]bool{}}}
		_, err := RegisterMCPTools(ctx, registry, []*types.MCPService{service}, manager, policy, 0, nil, nil)
		require.NoError(t, err)
		registry.PrepareMCPToolsDirect(ctx)
		snapshot, _, err := registry.mcpCatalog().snapshot(ctx, service.ID, false)
		require.NoError(t, err)
		require.Len(t, snapshot, 1)
		proxyArgs, err := json.Marshal(map[string]any{
			"tool_ref": mcpToolRef(snapshot[0]), "arguments": map[string]any{"value": 2},
		})
		require.NoError(t, err)
		for _, tc := range []struct {
			name string
			args json.RawMessage
		}{
			{ToolCallMCPTool, proxyArgs}, {mcpRegisteredName(snapshot[0]), json.RawMessage(`{"value":2}`)},
		} {
			for _, callCtx := range []context.Context{ctx, interactive} {
				res, err := registry.ExecuteTool(callCtx, tc.name, tc.args)
				require.NoError(t, err)
				require.True(t, res.Success, res.Error)
			}
		}
		require.EqualValues(t, 4, calls.Load())
		require.Zero(t, policy.asked)
		policy.disabled["external/write"] = true
		res, err := registry.ExecuteTool(interactive, ToolCallMCPTool, proxyArgs)
		require.NoError(t, err)
		require.False(t, res.Success)
		require.EqualValues(t, 4, calls.Load())
		policy.disabled["external/write"] = false
		registry.assistantAccess.gate = &fakeApprover{
			decision: approval.Decision{Approved: false, Reason: "declined earlier"},
		}
		resumed := WithToolExecContext(ctx, &ToolExecContext{
			EventBus: event.NewEventBus(), Answered: &types.AgentPrompt{Kind: types.AgentPromptKindToolApproval},
		})
		res, err = registry.ExecuteTool(resumed, ToolCallMCPTool, proxyArgs)
		require.NoError(t, err)
		require.False(t, res.Success)
		require.Contains(t, res.Error, "declined earlier")
		require.EqualValues(t, 4, calls.Load())
	})

	for _, level := range []types.AssistantAccessLevel{types.AssistantAccessConfirm, types.AssistantAccessAutoWrite} {
		for _, direct := range []bool{false, true} {
			name := string(level) + "/proxy"
			if direct {
				name = string(level) + "/direct"
			}
			t.Run(name, func(t *testing.T) {
				calls.Store(0)
				registry := NewToolRegistry()
				gate := &fakeApprover{decision: approval.Decision{Approved: false, Reason: "declined"}}
				registry.SetAssistantAccess(level, gate)
				policy := &catalogPolicy{disabled: map[string]bool{}}
				_, err := RegisterMCPTools(ctx, registry, []*types.MCPService{service}, manager, policy, 0, nil, nil)
				require.NoError(t, err)
				registry.PrepareMCPToolsDirect(ctx)
				snapshot, _, err := registry.mcpCatalog().snapshot(ctx, service.ID, false)
				require.NoError(t, err)
				require.Len(t, snapshot, 1)
				name, args := ToolCallMCPTool, json.RawMessage(nil)
				args, err = json.Marshal(map[string]any{
					"tool_ref": mcpToolRef(snapshot[0]), "arguments": map[string]any{"value": 1},
				})
				require.NoError(t, err)
				if direct {
					name, args = mcpRegisteredName(snapshot[0]), json.RawMessage(`{"value":1}`)
				}

				// A service configured to skip MCP approval still cannot skip
				// assistant approval, including runs without an interactive UI.
				res, err := registry.ExecuteTool(ctx, name, args)
				require.NoError(t, err)
				require.False(t, res.Success)
				require.Empty(t, gate.requests)
				res, err = registry.ExecuteTool(interactive, name, args)
				require.NoError(t, err)
				require.False(t, res.Success)
				require.Len(t, gate.requests, 1)
				require.Zero(t, calls.Load())
				require.Equal(t, service.ID, gate.requests[0].ServiceID)
				require.JSONEq(t, `{"value":1}`, string(gate.requests[0].Args))

				gate.decision = approval.Decision{Suspended: &types.PromptSuspension{}}
				res, err = registry.ExecuteTool(interactive, name, args)
				require.NoError(t, err)
				require.NotNil(t, res.Suspend)
				require.Zero(t, calls.Load(), "suspending approval must not invoke the server")

				gate.decision = approval.Decision{
					Approved: true, PendingID: "p1", ModifiedArgs: json.RawMessage(`{"value":2}`),
				}
				res, err = registry.ExecuteTool(interactive, name, args)
				require.NoError(t, err)
				require.True(t, res.Success, res.Error)
				require.EqualValues(t, 1, calls.Load())
				require.Equal(t, []string{"p1"}, gate.executed)

				registry.assistantAccess.gate = nil
				res, err = registry.ExecuteTool(interactive, name, args)
				require.NoError(t, err)
				require.False(t, res.Success, "an absent approval broker must fail closed")
				require.EqualValues(t, 1, calls.Load())
				registry.assistantAccess.gate = gate

				// A prior approval cannot override current MCP enablement.
				policy.disabled["external/write"] = true
				resumed := WithToolExecContext(ctx, &ToolExecContext{
					EventBus: event.NewEventBus(),
					Answered: &types.AgentPrompt{Kind: types.AgentPromptKindToolApproval},
				})
				res, err = registry.ExecuteTool(resumed, name, args)
				require.NoError(t, err)
				require.False(t, res.Success)
				require.EqualValues(t, 1, calls.Load())
				policy.disabled["external/write"] = false

				gate.decision = approval.Decision{Approved: false, Reason: "declined on resume"}
				res, err = registry.ExecuteTool(resumed, name, args)
				require.NoError(t, err)
				require.False(t, res.Success)
				require.Contains(t, res.Error, "declined on resume")
				require.EqualValues(t, 1, calls.Load())

				// Simulate resuming with a lower effective workspace ceiling.
				snapshot[0].assistantAccess.Level = types.AssistantAccessReadOnly
				gate.decision = approval.Decision{Approved: true}
				res, err = snapshot[0].Execute(resumed, json.RawMessage(`{"value":2}`))
				require.NoError(t, err)
				require.False(t, res.Success)
				require.Contains(t, res.Error, "read-only")
				require.EqualValues(t, 1, calls.Load())
			})
		}
	}
}

// A saved MCP approval preference must not reintroduce confirmation in full auto.
type alwaysAskMCPPolicy struct {
	catalogPolicy
	asked int
}

func (*alwaysAskMCPPolicy) NeedsApproval(context.Context, uint64, string, string) bool { return true }

func (p *alwaysAskMCPPolicy) RequestAndWait(context.Context, approval.PendingRequest) (approval.Decision, error) {
	p.asked++
	return approval.Decision{Approved: false}, nil
}
