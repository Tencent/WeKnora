package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
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

type numberPrecisionApprovalGate struct {
	modifiedArgsGate
}

func (g *numberPrecisionApprovalGate) ResumeDecision(
	_ context.Context, p *types.AgentPrompt, _ *event.EventBus,
) approval.Decision {
	return approval.Decision{PendingID: p.ID, Approved: true, ModifiedArgs: g.modified}
}

func TestMCPProxyPreservesNumericArgumentsOnWire(t *testing.T) {
	utils.SetSSRFWhitelistFromRaw("127.0.0.1")
	t.Cleanup(utils.ResetSSRFWhitelistForTest)
	const exactArgs = `{"id":9007199254740993,"nested":{"ids":[18446744073709551615,-9007199254740993]},` +
		`"amount":0.1234567890123456789,"label":"9007199254740993","active":true,"empty":null}`
	const schema = `{"type":"object","properties":{
		"id":{"type":"integer"},"amount":{"type":"number"},
		"nested":{"type":"object","properties":{"ids":{"type":"array","items":{"type":"integer"}}}}
	}}`
	for _, mode := range []string{"original", "edited_approval", "resumed_approval"} {
		t.Run(mode, func(t *testing.T) {
			server := sdkserver.NewMCPServer("precision", "1", sdkserver.WithToolCapabilities(false))
			server.AddTool(sdkmcp.Tool{Name: "store_record", RawInputSchema: json.RawMessage(schema)},
				func(context.Context, sdkmcp.CallToolRequest) (*sdkmcp.CallToolResult, error) {
					return sdkmcp.NewToolResultText("stored"), nil
				})
			transport := sdkserver.NewStreamableHTTPServer(server, sdkserver.WithDisableStreaming(true))
			captured := make(chan json.RawMessage, 2)
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodPost {
					body, err := io.ReadAll(r.Body)
					if err != nil {
						t.Errorf("read request: %v", err)
						http.Error(w, "read failed", http.StatusBadRequest)
						return
					}
					r.Body = io.NopCloser(bytes.NewReader(body))
					var request struct {
						Method string `json:"method"`
						Params struct {
							Arguments json.RawMessage `json:"arguments"`
						} `json:"params"`
					}
					if err := json.Unmarshal(body, &request); err != nil {
						t.Errorf("decode request: %v", err)
						http.Error(w, "decode failed", http.StatusBadRequest)
						return
					}
					if request.Method == "tools/call" {
						captured <- request.Params.Arguments
					}
				}
				transport.ServeHTTP(w, r)
			}))
			t.Cleanup(upstream.Close)
			manager := internalmcp.NewMCPManager(nil)
			t.Cleanup(manager.Shutdown)
			service := &types.MCPService{
				ID: "precision", Name: "Precision", TenantID: 7, Enabled: true,
				URL: &upstream.URL, TransportType: types.MCPTransportHTTPStreamable,
			}
			ctx := catalogTestContext()
			registry, gate := NewToolRegistry(), &numberPrecisionApprovalGate{}
			input := json.RawMessage(exactArgs)
			if mode != "original" {
				input = json.RawMessage(`{"obsolete":"must be replaced"}`)
				gate.modified = json.RawMessage(exactArgs)
			}
			_, err := RegisterMCPTools(ctx, registry, []*types.MCPService{service}, manager, gate, 0, nil, nil)
			require.NoError(t, err)
			tool := describeTool(ctx, t, registry, service.ID, "store_record")
			meta := &ToolExecContext{EventBus: event.NewEventBus(), ToolCallID: "store-record", ApprovalCtx: ctx}
			if mode == "resumed_approval" {
				meta.Answered = &types.AgentPrompt{ID: "approved", Kind: types.AgentPromptKindToolApproval}
			}
			ctx = WithToolExecContext(ctx, meta)
			args, err := json.Marshal(map[string]any{"tool_ref": tool.ToolRef, "arguments": input})
			require.NoError(t, err)
			result, err := registry.ExecuteTool(ctx, ToolCallMCPTool, args)
			require.NoError(t, err)
			require.True(t, result.Success, result.Error)
			require.Len(t, captured, 1)
			// Inspect the actual HTTP payload before the server SDK decodes numbers.
			var received map[string]any
			decoder := json.NewDecoder(bytes.NewReader(<-captured))
			decoder.UseNumber()
			require.NoError(t, decoder.Decode(&received))
			require.Equal(t, map[string]any{
				"id": json.Number("9007199254740993"),
				"nested": map[string]any{"ids": []any{
					json.Number("18446744073709551615"), json.Number("-9007199254740993"),
				}},
				"amount": json.Number("0.1234567890123456789"),
				"label":  "9007199254740993", "active": true, "empty": nil,
			}, received)
		})
	}
}
