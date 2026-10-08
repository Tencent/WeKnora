package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	agenttools "github.com/Tencent/WeKnora/internal/agent/tools"
	internalmcp "github.com/Tencent/WeKnora/internal/mcp"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/utils"
	sdkmcp "github.com/mark3labs/mcp-go/mcp"
	sdkserver "github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Exercise the actual Agent -> provider HTTP -> MCP -> provider history loop.
// Neither the user input nor the engine contains an MCP mention. A registry-only
// test would miss adapters silently dropping all tool definitions (Anthropic).
func TestMCPExposureWithoutMentionReachesProviderAndExecutes(t *testing.T) {
	utils.SetSSRFWhitelistFromRaw("127.0.0.1")
	t.Cleanup(utils.ResetSSRFWhitelistForTest)
	for _, variant := range []string{
		"openai", "anthropic", "openai-deferred", "anthropic-deferred",
		"openai-proxy-deferred", "anthropic-proxy-deferred",
		"openai-string-proxy-deferred", "anthropic-string-proxy-deferred",
	} {
		t.Run(variant, func(t *testing.T) {
			provider := strings.Split(variant, "-")[0]
			deferred := strings.HasSuffix(variant, "-deferred")
			proxyCall := strings.Contains(variant, "-proxy-")
			const serverID = "8f7a5b68-a7ab-4565-b6f3-1cae2578f040"
			var executed atomic.Int32
			const description = "Retrieve a customer order and its delivery status. Use the original " +
				"external order ID."
			server := sdkserver.NewMCPServer(
				"Orders",
				"1",
				sdkserver.WithToolCapabilities(false),
				sdkserver.WithInstructions("Order IDs belong to the external customer system."),
			)
			server.AddTool(
				sdkmcp.NewTool(
					"get_order",
					sdkmcp.WithDescription(description),
					sdkmcp.WithString("id", sdkmcp.Required()),
				),
				func(_ context.Context, r sdkmcp.CallToolRequest) (*sdkmcp.CallToolResult, error) {
					executed.Add(1)
					assert.Equal(t, "42", r.GetArguments()["id"])
					return sdkmcp.NewToolResultText("order 42 shipped"), nil
				},
			)
			var upstreamRequests atomic.Int32
			upstreamHandler := sdkserver.NewStreamableHTTPServer(server, sdkserver.WithStateLess(true))
			mcpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				upstreamRequests.Add(1)
				upstreamHandler.ServeHTTP(w, r)
			}))
			defer mcpServer.Close()
			manager := internalmcp.NewMCPManager(nil)
			defer manager.Shutdown()
			ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(7))
			registry := agenttools.NewToolRegistry()
			var metadata *agenttools.MCPMetadataIO
			if deferred {
				metadata = &agenttools.MCPMetadataIO{
					Get: func(context.Context, uint64, string) (*types.MCPMetadata, error) {
						return &types.MCPMetadata{
							Instructions: "Order IDs belong to the external customer system.",
							Tools: []*types.MCPTool{
								{
									Name:        "get_order",
									Description: description,
									InputSchema: json.RawMessage(
										`{"type":"object","properties":{"id":{"type":"string"}},"required":["id"]}`,
									),
								},
							},
						}, nil
					},
				}
			}
			_, err := agenttools.RegisterMCPTools(
				ctx,
				registry,
				[]*types.MCPService{
					{
						ID:            serverID,
						Name:          "Orders",
						Description:   "Query orders and shipping",
						Enabled:       true,
						URL:           &mcpServer.URL,
						TransportType: types.MCPTransportHTTPStreamable,
						AuthConfig:    &types.MCPAuthConfig{Token: "NEVER-IN-MODEL-CONTEXT"},
					},
				},
				manager,
				nil,
				0,
				nil,
				metadata,
			)
			require.NoError(t, err)
			if deferred {
				registry.PrepareMCPTools(ctx)
			} else {
				registry.PrepareMCPToolsDirect(ctx)
			}
			var requests atomic.Int32
			modelServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				assert.NotContains(t, string(body), "NEVER-IN-MODEL-CONTEXT")
				assert.NotContains(t, string(body), serverID, "the provider must only receive short routing IDs")
				var payload map[string]json.RawMessage
				if !assert.NoError(t, json.Unmarshal(body, &payload)) {
					w.WriteHeader(500)
					return
				}
				var definitions []map[string]json.RawMessage
				if !assert.NoError(t, json.Unmarshal(payload["tools"], &definitions)) {
					w.WriteHeader(500)
					return
				}
				toolName := ""
				proxyVisible := false
				for _, definition := range definitions {
					if provider == "openai" {
						var function map[string]json.RawMessage
						_ = json.Unmarshal(definition["function"], &function)
						definition = function
					}
					var name, desc string
					_ = json.Unmarshal(definition["name"], &name)
					_ = json.Unmarshal(definition["description"], &desc)
					if name == agenttools.ToolCallMCPTool {
						proxyVisible = true
						assert.Contains(t, string(body), `"enum":["mt1"]`)
					}
					if name == agenttools.ToolDiscoverMCPTools {
						assert.Contains(t, desc, `"server_id":"ms1"`)
						assert.Contains(t, string(body), `"enum":["ms1"]`)
					}
					if strings.HasPrefix(name, "mcp_") {
						toolName = name
						assert.Contains(t, desc, description)
						schemaKey := "parameters"
						if provider == "anthropic" {
							schemaKey = "input_schema"
						}
						assert.Contains(t, string(definition[schemaKey]), `"required":["id"]`)
					}
				}
				call := requests.Add(1)
				w.Header().Set("Content-Type", "text/event-stream")
				if deferred && call <= 2 {
					assert.False(t, proxyVisible, "providers must not offer a proxy before successful describe")
					assert.Zero(
						t,
						upstreamRequests.Load(),
						"cached discovery must not initialize an upstream connection",
					)
					assert.Empty(t, toolName, "full MCP functions must stay hidden until describe")
					assert.NotContains(t, string(payload["tools"]), description)
					assert.Contains(t, string(payload["tools"]), "Query orders and shipping")
					args := `{"mode":"list_tools","server_id":"ms1"}`
					if call == 2 {
						assert.Contains(t, string(payload["messages"]), "get_order")
						args = `{"mode":"describe","server_id":"ms1","tool_name":"get_order"}`
					}
					streamMCPTestCall(
						w,
						provider,
						agenttools.ToolDiscoverMCPTools,
						fmt.Sprintf("discovery_%d", call),
						args,
					)
					return
				}
				if !assert.NotEmpty(t, toolName, "the call request must include the concrete MCP definition") {
					w.WriteHeader(500)
					return
				}
				assert.True(t, proxyVisible, "a loaded definition enables the compatibility proxy")
				assert.Contains(t, string(payload["tools"]), "Order IDs belong to the external customer system.")
				if (!deferred && call == 1) || (deferred && call == 3) {
					if proxyCall {
						arguments := `{"tool_ref":"mt1","arguments":{"id":"42"}}`
						if strings.Contains(variant, "-string-") {
							arguments = `{"tool_ref":"mt1","arguments":"{\"id\":\"42\"}"}`
						}
						streamMCPTestCall(w, provider, agenttools.ToolCallMCPTool, "call_order",
							arguments)
						return
					}
					if provider == "openai" {
						_, _ = fmt.Fprintf(
							w,
							"data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,"+
								"\"id\":\"call_order\",\"type\":\"function\",\"function\":{\"name\":%q,"+
								"\"arguments\":\"{\\\"id\\\":\\\"42\\\"}\"}}]},\"finish_reason\":null}]}\n\ndata: "+
								"{\"choices\":[{\"delta\":{},\"finish_reason\":\"tool_calls\"}]}\n\ndata: [DONE]\n"+
								"\n",
							toolName,
						)
					} else {
						_, _ = fmt.Fprintf(w, "data: {\"type\":\"content_block_start\",\"index\":0,"+
							"\"content_block\":{\"type\":\"tool_use\",\"id\":\"call_order\",\"name\":%q,"+
							"\"input\":{}}}\n\ndata: {\"type\":\"content_block_delta\",\"index\":0,"+
							"\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"{\\\"id\\\":\\\"42\\\"}\"}}\n"+
							"\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\ndata: "+
							"{\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"tool_use\"}}\n\ndata: "+
							"{\"type\":\"message_stop\"}\n\n", toolName)
					}
					return
				}
				assert.Contains(t, string(payload["messages"]), "order 42 shipped")
				assert.Contains(t, string(payload["messages"]), "call_order")
				if provider == "openai" {
					assert.Contains(t, string(payload["messages"]), `"role":"tool"`)
					_, _ = fmt.Fprint(
						w,
						"data: {\"choices\":[{\"delta\":{\"content\":\"Order shipped.\"},"+
							"\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n",
					)
				} else {
					assert.Contains(t, string(payload["messages"]), `"type":"tool_result"`)
					_, _ = fmt.Fprint(w, "data: {\"type\":\"content_block_delta\",\"index\":0,"+
						"\"delta\":{\"type\":\"text_delta\",\"text\":\"Order shipped.\"}}\n\ndata: "+
						"{\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"}}\n\ndata: "+
						"{\"type\":\"message_stop\"}\n\n")
				}
			}))
			defer modelServer.Close()
			model, err := chat.NewRemoteChat(
				&chat.ChatConfig{BaseURL: modelServer.URL, APIKey: "test", ModelName: "test-model", Provider: provider},
			)
			require.NoError(t, err)
			engine := newTestEngine(t, model)
			engine.toolRegistry = registry
			state, err := engine.Execute(ctx, "session", "message", "查询订单42的配送状态", nil)
			require.NoError(t, err)
			require.Equal(t, "Order shipped.", state.FinalAnswer)
			require.EqualValues(t, 1, executed.Load())
			toolRound := 0
			if deferred {
				toolRound = 2
				require.Equal(t, serverID, state.RoundSteps[0].ToolCalls[0].Args["server_id"])
				require.Equal(t, serverID, state.RoundSteps[1].ToolCalls[0].Args["server_id"])
			}
			if proxyCall {
				require.Contains(t, state.RoundSteps[toolRound].ToolCalls[0].Args["tool_ref"], "mcpt_")
			}
			require.EqualValues(t, 2+toolRound, requests.Load())
			require.Equal(t, "get_order", state.RoundSteps[toolRound].ToolCalls[0].Target.ToolName)
		})
	}
}

func streamMCPTestCall(w http.ResponseWriter, provider, name, id, arguments string) {
	if provider == "openai" {
		_, _ = fmt.Fprintf(
			w,
			"data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":%q,"+
				"\"type\":\"function\",\"function\":{\"name\":%q,\"arguments\":%q}}]},"+
				"\"finish_reason\":\"tool_calls\"}]}\n\ndata: [DONE]\n\n",
			id,
			name,
			arguments,
		)
	} else {
		_, _ = fmt.Fprintf(w, "data: {\"type\":\"content_block_start\",\"index\":0,"+
			"\"content_block\":{\"type\":\"tool_use\",\"id\":%q,\"name\":%q,\"input\":{}}}\n\n"+
			"data: {\"type\":\"content_block_delta\",\"index\":0,"+
			"\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":%q}}\n\ndata: "+
			"{\"type\":\"content_block_stop\",\"index\":0}\n\ndata: "+
			"{\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"tool_use\"}}\n\ndata: "+
			"{\"type\":\"message_stop\"}\n\n", id, name, arguments)
	}
}
