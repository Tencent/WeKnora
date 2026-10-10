package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/Tencent/WeKnora/internal/agent/approval"
	"github.com/Tencent/WeKnora/internal/event"
	internalmcp "github.com/Tencent/WeKnora/internal/mcp"
	"github.com/Tencent/WeKnora/internal/modelcontext"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/utils"
	sdkmcp "github.com/mark3labs/mcp-go/mcp"
	sdkserver "github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/require"
)

type retryApprovalGate struct {
	proxyApprovalGate
	approvals atomic.Int32
}

func (g *retryApprovalGate) RequestAndWait(
	ctx context.Context, request approval.PendingRequest,
) (approval.Decision, error) {
	g.approvals.Add(1)
	return g.proxyApprovalGate.RequestAndWait(ctx, request)
}

func TestMCPCallRetryDoesNotRepeatAmbiguousExecution(t *testing.T) {
	utils.SetSSRFWhitelistFromRaw("127.0.0.1")
	t.Cleanup(utils.ResetSSRFWhitelistForTest)
	tests := []struct {
		name         string
		wantRequests int32
		wantExecuted int32
		wantSuccess  bool
		wantUnknown  bool
	}{
		{name: "http_error_after_execution", wantRequests: 1, wantExecuted: 1, wantUnknown: true},
		{name: "connection_lost_after_execution", wantRequests: 1, wantExecuted: 1, wantUnknown: true},
		{name: "rpc_error_after_execution", wantRequests: 1, wantExecuted: 1, wantUnknown: true},
		{name: "cancel_after_execution", wantRequests: 1, wantExecuted: 1, wantUnknown: true},
		{name: "expired_session", wantRequests: 2, wantExecuted: 1, wantSuccess: true},
		{name: "repeated_expired_session", wantRequests: 2},
		{name: "authorization_rejection", wantRequests: 2},
		{name: "tool_error", wantRequests: 1, wantExecuted: 1},
		{name: "success", wantRequests: 1, wantExecuted: 1, wantSuccess: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(catalogTestContext())
			t.Cleanup(cancel)
			var requests, executed, initialized atomic.Int32
			server := sdkserver.NewMCPServer("Orders", "1", sdkserver.WithToolCapabilities(false))
			server.AddTool(
				sdkmcp.NewTool("create_order"),
				func(context.Context, sdkmcp.CallToolRequest) (*sdkmcp.CallToolResult, error) {
					executed.Add(1)
					if tt.name == "tool_error" {
						return sdkmcp.NewToolResultError("order refused"), nil
					}
					return sdkmcp.NewToolResultText("order created"), nil
				},
			)
			transport := sdkserver.NewStreamableHTTPServer(server, sdkserver.WithDisableStreaming(true))
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost {
					transport.ServeHTTP(w, r)
					return
				}
				body, err := io.ReadAll(r.Body)
				if err != nil {
					t.Errorf("read request: %v", err)
					return
				}
				r.Body = io.NopCloser(bytes.NewReader(body))
				var request struct {
					ID     json.RawMessage `json:"id"`
					Method string          `json:"method"`
				}
				if err := json.Unmarshal(body, &request); err != nil {
					t.Errorf("decode request: %v", err)
					return
				}
				if request.Method == "initialize" {
					initialized.Add(1)
				}
				if request.Method != "tools/call" {
					transport.ServeHTTP(w, r)
					return
				}
				attempt := requests.Add(1)
				if tt.name == "authorization_rejection" {
					http.Error(w, "authorization required", http.StatusUnauthorized)
					return
				}
				if tt.name == "repeated_expired_session" || (tt.name == "expired_session" && attempt == 1) {
					http.Error(w, "session expired", http.StatusNotFound)
					return
				}
				// Execute the real SDK tool first, then lose/replace its response.
				// This models a committed write whose result never reaches the client.
				if attempt == 1 {
					switch tt.name {
					case "http_error_after_execution", "connection_lost_after_execution",
						"rpc_error_after_execution", "cancel_after_execution":
						recorder := httptest.NewRecorder()
						transport.ServeHTTP(recorder, r)
						switch tt.name {
						case "http_error_after_execution":
							http.Error(w, "response lost at gateway", http.StatusBadGateway)
						case "connection_lost_after_execution":
							conn, _, err := w.(http.Hijacker).Hijack()
							if err != nil {
								t.Errorf("hijack response: %v", err)
								return
							}
							_ = conn.Close()
						case "rpc_error_after_execution":
							w.Header().Set("Content-Type", "application/json")
							_ = json.NewEncoder(w).Encode(map[string]any{
								"jsonrpc": "2.0", "id": request.ID,
								"error": map[string]any{"code": -32603, "message": "post-write failure"},
							})
						case "cancel_after_execution":
							cancel()
						}
						return
					}
				}
				transport.ServeHTTP(w, r)
			}))
			t.Cleanup(upstream.Close)
			manager := internalmcp.NewMCPManager(nil)
			t.Cleanup(manager.Shutdown)
			service := &types.MCPService{
				ID: "orders", Name: "Orders", TenantID: 7, Enabled: true,
				URL: &upstream.URL, TransportType: types.MCPTransportHTTPStreamable,
			}
			registry, gate := NewToolRegistry(), &retryApprovalGate{}
			_, err := RegisterMCPTools(ctx, registry, []*types.MCPService{service}, manager, gate, 0, nil, nil)
			require.NoError(t, err)
			tool := describeTool(ctx, t, registry, service.ID, "create_order")
			ctx = WithToolExecContext(ctx, &ToolExecContext{
				EventBus: event.NewEventBus(), ToolCallID: "create-order", ApprovalCtx: ctx,
			})
			args, err := json.Marshal(map[string]any{"tool_ref": tool.ToolRef, "arguments": map[string]any{}})
			require.NoError(t, err)
			result, err := registry.ExecuteTool(ctx, ToolCallMCPTool, args)
			require.NoError(t, err)
			t.Logf("requests=%d, server executions=%d, approvals=%d",
				requests.Load(), executed.Load(), gate.approvals.Load())
			require.Equal(t, tt.wantRequests, requests.Load(), "one approval must not authorize blind replays")
			require.Equal(t, tt.wantExecuted, executed.Load())
			require.EqualValues(t, 1, gate.approvals.Load())
			require.Equal(t, tt.wantSuccess, result.Success)
			visible := modelcontext.NewRegistry(true).ModelToolResultForTool(ToolCallMCPTool, result)
			if tt.wantUnknown {
				require.Contains(t, visible, "outcome is unknown")
				require.Contains(t, visible, "Verify")
				require.NotContains(t, visible, "Failed to connect")
			} else {
				require.NotContains(t, visible, "outcome is unknown")
			}
			if tt.name == "expired_session" || tt.name == "repeated_expired_session" {
				require.EqualValues(t, 2, initialized.Load(), "retain bounded recovery of rejected sessions")
			}
			if tt.wantUnknown && tt.name != "cancel_after_execution" {
				// A subsequent deliberate call remains possible, with a new approval.
				ctx = WithToolExecContext(ctx, &ToolExecContext{
					EventBus: event.NewEventBus(), ToolCallID: "verified-retry", ApprovalCtx: ctx,
				})
				result, err = registry.ExecuteTool(ctx, ToolCallMCPTool, args)
				require.NoError(t, err)
				require.True(t, result.Success, result.Error)
				require.EqualValues(t, 2, executed.Load())
				require.EqualValues(t, 2, gate.approvals.Load())
			}
		})
	}
}
