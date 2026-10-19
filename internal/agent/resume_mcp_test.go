package agent

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Tencent/WeKnora/internal/agent/approval"
	"github.com/Tencent/WeKnora/internal/agent/tools"
	"github.com/Tencent/WeKnora/internal/event"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

type resumeMCPGate struct{ resumed bool }

func (*resumeMCPGate) IsEnabled(context.Context, uint64, string, string) (bool, error) {
	return true, nil
}
func (*resumeMCPGate) NeedsApproval(context.Context, uint64, string, string) bool { return true }
func (*resumeMCPGate) RequestAndWait(context.Context, approval.PendingRequest) (approval.Decision, error) {
	return approval.Decision{Reason: "unexpected new approval"}, nil
}

func (g *resumeMCPGate) ResumeDecision(context.Context, *types.AgentPrompt, *event.EventBus) approval.Decision {
	g.resumed = true
	return approval.Decision{Reason: "test rejects before external execution"}
}
func (*resumeMCPGate) MarkExecuted(context.Context, string) {}

func TestResumeRestoresFirstDeferredMCPCall(t *testing.T) {
	for _, proxy := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "proxy"}[proxy], func(t *testing.T) {
			ctx := context.WithValue(t.Context(), types.TenantIDContextKey, uint64(1))
			ctx = types.WithPrincipal(ctx, types.Principal{Type: "user", ID: "u"})
			gate := &resumeMCPGate{}
			newRegistry := func() *tools.ToolRegistry {
				r := tools.NewToolRegistry()
				services := []*types.MCPService{{ID: "svc", Name: "service", TenantID: 1, Enabled: true}}
				_, err := tools.RegisterMCPTools(ctx, r, services, nil, gate, 0, nil, &tools.MCPMetadataIO{
					Get: func(context.Context, uint64, string) (*types.MCPMetadata, error) {
						return &types.MCPMetadata{Tools: []*types.MCPTool{
							{Name: "read", InputSchema: json.RawMessage(`{"type":"object"}`)},
							{Name: "write", InputSchema: json.RawMessage(`{"type":"object"}`)},
						}}, nil
					},
				})
				require.NoError(t, err)
				r.PrepareMCPTools(ctx)
				return r
			}
			original := newRegistry()
			described, err := original.ExecuteTool(ctx, tools.ToolDiscoverMCPTools,
				json.RawMessage(`{"mode":"describe","server_id":"svc","tool_name":"write"}`))
			require.NoError(t, err)
			require.True(t, described.Success, described.Error)
			var definition map[string]any
			require.NoError(t, json.Unmarshal([]byte(described.Output), &definition))
			readDefinition, err := original.ExecuteTool(
				ctx,
				tools.ToolDiscoverMCPTools,
				json.RawMessage(`{"mode":"describe","server_id":"svc","tool_name":"read"}`),
			)
			require.NoError(t, err)
			require.True(t, readDefinition.Success)
			var readTool map[string]any
			require.NoError(t, json.Unmarshal([]byte(readDefinition.Output), &readTool))
			name, args := definition["function_name"].(string), `{}`
			if proxy {
				name = tools.ToolCallMCPTool
				data, err := json.Marshal(map[string]any{
					"tool_ref": definition["tool_ref"], "arguments": map[string]any{},
				})
				require.NoError(t, err)
				args = string(data)
			}
			snap := RunSnapshot{
				MCPToolRefs: original.MCPExposureSnapshot(),
				Version:     RunSnapshotVersion, Query: "do the work",
				PendingRound: PendingRound{Calls: []PendingCall{{ID: "call", Name: name, Arguments: args}}},
			}
			data, err := json.Marshal(snap)
			require.NoError(t, err)
			model := &mockChat{responses: []mockResponse{{chunks: []types.StreamResponse{
				{ResponseType: types.ResponseTypeAnswer, Content: "done", Done: true, FinishReason: "stop"},
			}}}}
			e := newTestEngine(t, model)
			e.toolRegistry = newRegistry()
			_, err = e.Resume(ctx, "s", "m", nil, data, answeredPrompt("call"))
			require.NoError(t, err)
			_, err = e.toolRegistry.GetTool(readTool["function_name"].(string))
			require.NoError(t, err, "described but uncalled tools must survive approval resume")
			proxyTool, err := e.toolRegistry.GetTool(tools.ToolCallMCPTool)
			require.NoError(t, err)
			require.Contains(t, string(proxyTool.Parameters()), readTool["tool_ref"].(string))
			require.True(t, gate.resumed, "must find the deferred tool and deliver the existing decision")
		})
	}
}
