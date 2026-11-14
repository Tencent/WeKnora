package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/agent"
	"github.com/Tencent/WeKnora/internal/agent/approval"
	"github.com/Tencent/WeKnora/internal/agent/collaboration"
	"github.com/Tencent/WeKnora/internal/agent/hitl"
	"github.com/Tencent/WeKnora/internal/agent/tools"
	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/event"
	internalmcp "github.com/Tencent/WeKnora/internal/mcp"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/utils"
	sdkmcp "github.com/mark3labs/mcp-go/mcp"
	sdkserver "github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/require"
)

type collaborationMCPModel struct {
	call        bool
	truncations int
	name        string
}

func (m *collaborationMCPModel) Chat(context.Context, []chat.Message, *chat.ChatOptions) (*types.ChatResponse, error) {
	return &types.ChatResponse{}, nil
}
func (m *collaborationMCPModel) GetModelID() string   { return "test" }
func (m *collaborationMCPModel) GetModelName() string { return "test" }

func (m *collaborationMCPModel) ChatStream(
	_ context.Context,
	_ []chat.Message,
	_ *chat.ChatOptions,
) (<-chan types.StreamResponse, error) {
	ch := make(chan types.StreamResponse, 1)
	resp := types.StreamResponse{
		ResponseType: types.ResponseTypeAnswer,
		Content:      "child finished",
		Done:         true,
		FinishReason: "stop",
	}
	if m.call {
		m.call = false
		resp.Content = ""
		resp.FinishReason = "tool_calls"
		resp.ToolCalls = []types.LLMToolCall{
			{ID: "write-call", Type: "function", Function: types.FunctionCall{Name: m.name, Arguments: `{}`}},
		}
	}
	if len(resp.ToolCalls) == 0 && m.truncations > 0 {
		m.truncations--
		resp.Content = "partial answer"
		resp.FinishReason = "length"
	}
	ch <- resp
	close(ch)
	return ch, nil
}

// Exercise the actual engine snapshot, MCP approval gate, scheduler restart,
// resumed tool and HTTP MCP transport. Only the language model is scripted.
func TestDelegatedMCPApprovalRealEngine(t *testing.T) {
	for _, inherited := range []bool{false, true} {
		name := "template"
		if inherited {
			name = "inherited"
		}
		t.Run(name, func(t *testing.T) { testCollaborationMCPApproval(t, inherited) })
	}
}

func testCollaborationMCPApproval(t *testing.T, inherited bool) {
	utils.SetSSRFWhitelistFromRaw("127.0.0.1")
	t.Cleanup(utils.ResetSSRFWhitelistForTest)
	for _, mode := range []string{"approve", "revise", "partial", "fallback_partial", "reject", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			s, agents, messages, ctx := collaborationService(t)
			var effects atomic.Int32
			server := sdkserver.NewMCPServer("Writer", "1", sdkserver.WithToolCapabilities(false))
			server.AddTool(
				sdkmcp.NewTool("write"),
				func(context.Context, sdkmcp.CallToolRequest) (*sdkmcp.CallToolResult, error) {
					effects.Add(1)
					return sdkmcp.NewToolResultText("written"), nil
				},
			)
			transport := httptest.NewServer(
				sdkserver.NewStreamableHTTPServer(server, sdkserver.WithDisableStreaming(true)),
			)
			defer transport.Close()
			manager := internalmcp.NewMCPManager(nil)
			defer manager.Shutdown()
			svc := &types.MCPService{
				ID:            "writer",
				Name:          "writer",
				TenantID:      7,
				Enabled:       true,
				URL:           &transport.URL,
				TransportType: types.MCPTransportHTTPStreamable,
			}
			require.NoError(t, s.db.AutoMigrate(&types.AgentPrompt{}, &types.AgentRunSuspension{}))
			broker := hitl.NewBroker(
				repository.NewAgentPromptRepository(s.db),
				nil,
				hitl.WithSuspensionStore(repository.NewAgentRunSuspensionRepository(s.db)),
			)
			gate := approval.NewGate(nil, nil, broker)
			s.BindRecovery(broker, gate, nil)
			s.Bind(
				&collaborationSessions{run: func(ctx context.Context, req *types.QARequest, bus *event.EventBus) error {
					if inherited && req.SummaryModelID != "selected-model" {
						return fmt.Errorf("inherited model lost across approval recovery")
					}
					registry := tools.NewToolRegistry()
					tool := tools.NewMCPTool(
						svc,
						&types.MCPTool{
							Name:        "write",
							InputSchema: json.RawMessage(`{"type":"object","properties":{}}`),
						},
						manager,
						gate,
						0,
					)
					registry.RegisterTool(tool)
					cfg := &types.AgentConfig{MaxIterations: 5}
					if mode == "fallback_partial" {
						cfg.MaxIterations = 1
					}
					truncations := 0
					if mode == "revise" {
						truncations = 1
					}
					if mode == "partial" || mode == "fallback_partial" {
						truncations = 2
					}
					engine := agent.NewAgentEngine(
						cfg,
						&collaborationMCPModel{truncations: truncations, call: req.Resume == nil, name: tool.Name()},
						registry,
						bus,
						nil,
						nil,
						req.Session.ID,
						"",
					)
					if err := s.attach(ctx, engine, registry, cfg, req.Session.ID, req.AssistantMessageID); err != nil {
						return err
					}
					engine.SetRunMode(types.RunModeAttended)
					engine.EnableSuspension(json.RawMessage(`{}`))
					if req.Resume != nil {
						_, err := engine.Resume(
							ctx,
							req.Session.ID,
							req.AssistantMessageID,
							nil,
							req.Resume.Snapshot,
							req.Resume.Prompt,
						)
						return err
					}
					_, err := engine.Execute(ctx, req.Session.ID, req.AssistantMessageID, req.Query, nil, nil)
					return err
				}},
			)
			scope := collaboration.Scope{TenantID: 7, UserID: "u", SessionID: "parent", ID: "parent-message"}
			require.NoError(t, s.Runtime.Store.Ensure(ctx, scope))
			config, err := s.resolve(ctx, "a", types.AssistantAccessConfirm)
			id := "a"
			if inherited {
				id = ""
				ctx = context.WithValue(ctx, childSourceKey{}, childSource{
					Agent: agents.agent, ModelID: "selected-model",
				})
				name := tools.NewMCPTool(svc, &types.MCPTool{Name: "write"}, manager, gate, 0).Name()
				config, err = inheritedChildSnapshot(ctx, &types.AgentConfig{
					MaxIterations: 5, AssistantAccess: types.AssistantAccessConfirm,
				}, []string{name})
			}
			require.NoError(t, err)
			_, err = s.Runtime.Spawn(ctx, scope, "spawn", "writer", id, config, "write")
			require.NoError(t, err)
			startCollaborationTestRuntime(ctx, t, s.Runtime)
			require.Eventually(t, func() bool {
				g, _ := s.Runtime.Store.Read(ctx, scope)
				return g != nil && g.Members[0].Runs[0].State == "waiting"
			}, 5*time.Second, 5*time.Millisecond)
			g, err := s.Runtime.Store.Read(ctx, scope)
			require.NoError(t, err)
			run := g.Members[0].Runs[0]
			require.Len(t, g.Events, 1, "the parent receives structured state without raw request/response payloads")
			require.Equal(t, "waiting", g.Events[0].Kind)
			var waiting collaboration.RuntimeNotice
			require.NoError(t, json.Unmarshal([]byte(g.Events[0].Content), &waiting))
			require.Equal(t, "approval_required", waiting.Type)
			require.Equal(t, "waiting", waiting.ExecutionState)
			require.Equal(t, run.PromptID, waiting.PromptID)
			require.Equal(t, "write", waiting.Tool)
			require.Empty(t, waiting.Decision)
			require.Zero(t, effects.Load())
			p, err := broker.Store().Get(ctx, run.PromptID)
			require.NoError(t, err)
			require.Equal(t, "web_user:u", p.UserID)
			views, err := s.PendingPrompts(ctx, scope.SessionID)
			require.NoError(t, err)
			require.Len(t, views[""], 1)
			require.NotNil(t, run.Progress)
			// Rebuild the runtime and broker on their existing stores.
			// Join post-commit status delivery before replacing shared dependencies.
			stopCollaborationTestRuntime(t, s.Runtime)
			broker = hitl.NewBroker(
				repository.NewAgentPromptRepository(s.db),
				nil,
				hitl.WithSuspensionStore(repository.NewAgentRunSuspensionRepository(s.db)),
			)
			gate = approval.NewGate(nil, nil, broker)
			s.Runtime = collaboration.New(collaboration.NewStore(s.db), s.run)
			s.Runtime.Reconcile = s.reconcile
			s.BindRecovery(broker, gate, nil)
			response := []byte(`{"approved":true}`)
			if mode == "reject" {
				response = []byte(`{"approved":false}`)
			}
			_, err = broker.Resolve(
				ctx,
				hitl.ResolveRequest{
					TenantID: 7,
					UserID:   "web_user:u",
					PromptID: run.PromptID,
					Kind:     types.AgentPromptKindToolApproval,
					Response: response,
				},
			)
			require.NoError(t, err)
			if mode == "cancel" {
				require.NoError(t, s.Runtime.Cancel(ctx, scope))
			}
			startCollaborationTestRuntime(ctx, t, s.Runtime)
			want := "succeeded"
			if mode == "partial" || mode == "fallback_partial" {
				want = "failed"
			}
			if mode == "cancel" {
				want = "cancelled"
			}
			require.Eventually(t, func() bool {
				g, _ := s.Runtime.Store.Read(ctx, scope)
				return g != nil && g.Members[0].Runs[0].State == want
			}, 5*time.Second, 5*time.Millisecond)
			settled, err := s.Runtime.Store.Read(ctx, scope)
			require.NoError(t, err)
			resumedCount := 0
			for _, evt := range settled.Events {
				if evt.Kind != "resumed" {
					continue
				}
				resumedCount++
				var notice collaboration.RuntimeNotice
				require.NoError(t, json.Unmarshal([]byte(evt.Content), &notice))
				require.Equal(t, "approval_resolved", notice.Type)
				require.Equal(t, "queued", notice.ExecutionState)
				require.Equal(t, run.PromptID, notice.PromptID)
				require.Equal(t, run.ID, notice.RunID)
				require.Equal(t, "write", notice.Tool)
				wantDecision := "approved"
				if mode == "reject" {
					wantDecision = "rejected"
				}
				require.Equal(t, wantDecision, notice.Decision)
			}
			if mode == "cancel" {
				require.Zero(t, resumedCount)
			} else {
				require.Equal(t, 1, resumedCount)
			}
			if mode != "reject" && mode != "cancel" {
				require.Equal(t, int32(1), effects.Load())
			} else {
				require.Zero(t, effects.Load())
			}
			if mode != "cancel" {
				m, err := messages.GetMessage(ctx, g.Members[0].SessionID, run.MessageID)
				require.NoError(t, err)
				require.True(t, m.IsCompleted)
				require.Empty(t, m.RunState)
				if mode == "partial" || mode == "fallback_partial" {
					require.Equal(t, "partial answer", m.Content)
					g, err := s.Runtime.Store.Read(ctx, scope)
					require.NoError(t, err)
					require.True(t, g.Members[0].Runs[0].Result.Truncated)
					require.Contains(t, g.Members[0].Runs[0].Result.Error, "partial")
				} else {
					require.Equal(t, "child finished", m.Content)
				}
				require.NotEmpty(t, m.AgentSteps)
				call := m.AgentSteps[0].ToolCalls[0]
				require.NotNil(t, call.Result)
				require.Equal(t, mode != "reject", call.Result.Success, call.Result.Error)
			}
			s.Runtime.Tick(ctx)
			s.Runtime.Tick(ctx)
			stopCollaborationTestRuntime(t, s.Runtime)
		})
	}
}
