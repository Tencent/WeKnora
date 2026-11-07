package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types/interfaces"

	"github.com/Tencent/WeKnora/internal/agent"
	"github.com/Tencent/WeKnora/internal/agent/approval"
	"github.com/Tencent/WeKnora/internal/agent/collaboration"
	"github.com/Tencent/WeKnora/internal/agent/hitl"
	"github.com/Tencent/WeKnora/internal/event"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func (f *collaborationMessages) GetMessage(_ context.Context, session, id string) (*types.Message, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := len(f.saved) - 1; i >= 0; i-- {
		if f.saved[i].ID == id && f.saved[i].SessionID == session {
			snapshot := *f.saved[i]
			return &snapshot, nil
		}
	}
	return nil, hitl.ErrPromptNotFound
}

func startCollaborationTestRuntime(ctx context.Context, t *testing.T, r *collaboration.Runtime) {
	t.Helper()
	r.Start(ctx)
	t.Cleanup(func() { stopCollaborationTestRuntime(t, r) })
}

func stopCollaborationTestRuntime(t *testing.T, r *collaboration.Runtime) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, r.Stop(ctx))
}

func TestDelegatedApprovalRecoveryAndLateAnswerCancellation(t *testing.T) {
	for _, cancelTask := range []bool{false, true} {
		t.Run(map[bool]string{false: "restart", true: "cancel-before-restart"}[cancelTask], func(t *testing.T) {
			s, _, _, ctx := collaborationService(t)
			streams := &childTestStreams{}
			s.streams = streams
			s.ChildEvents = func(
				_ context.Context, _ *types.Session, answer *types.Message, bus *event.EventBus,
			) func(context.Context) error {
				bus.On(event.EventAgentComplete, func(_ context.Context, evt event.Event) error {
					answer.Content = evt.Data.(event.AgentCompleteData).FinalAnswer
					return nil
				})
				return func(context.Context) error { return nil }
			}
			broker := hitl.NewBroker(nil, nil)
			gate := approval.NewGate(nil, nil, broker)
			s.BindRecovery(broker, gate, streams)
			scope := collaboration.Scope{TenantID: 7, UserID: "u", SessionID: "parent", ID: "parent-message"}
			require.NoError(t, s.Runtime.Store.Ensure(ctx, scope))
			var effects atomic.Int32
			s.Bind(
				&collaborationSessions{run: func(ctx context.Context, req *types.QARequest, bus *event.EventBus) error {
					var decision approval.Decision
					if req.Resume != nil {
						decision = gate.ResumeDecision(ctx, req.Resume.Prompt, bus)
					} else {
						snapshot, _ := json.Marshal(agent.RunSnapshot{Version: agent.RunSnapshotVersion})
						var err error
						decision,
							err = gate.RequestBuiltinApproval(ctx,
							approval.PendingRequest{
								TenantID:           7,
								UserID:             "web_user:u",
								SessionID:          req.Session.ID,
								AssistantMessageID: req.AssistantMessageID,
								ToolCallID:         "write",
								RegisteredToolName: "write",
								Args:               json.RawMessage(`{}`),
								Snapshot:           snapshot,
								SnapshotVersion:    agent.RunSnapshotVersion,
								EventBus:           bus,
							})
						if err != nil {
							return err
						}
					}
					if decision.Suspended != nil {
						return bus.Emit(
							ctx,
							event.Event{
								Type: event.EventAgentComplete,
								Data: event.AgentCompleteData{
									MessageID: req.AssistantMessageID,
									Suspended: decision.Suspended,
								},
							},
						)
					}
					if decision.Approved {
						effects.Add(1)
						gate.MarkExecuted(ctx, decision.PendingID)
					}
					return bus.Emit(
						ctx,
						event.Event{
							Type: event.EventAgentComplete,
							Data: event.AgentCompleteData{MessageID: req.AssistantMessageID, FinalAnswer: "done"},
						},
					)
				}},
			)
			config, err := s.resolve(ctx, "a", types.AssistantAccessReadOnly)
			require.NoError(t, err)
			_, err = s.Runtime.Spawn(ctx, scope, "spawn", "writer", "a", config, "write with approval")
			require.NoError(t, err)
			startCollaborationTestRuntime(ctx, t, s.Runtime)
			require.Eventually(t, func() bool {
				g, err := s.Runtime.Store.Read(ctx, scope)
				return err == nil && g.Members[0].Runs[0].State == "waiting"
			}, 3*time.Second, 5*time.Millisecond)
			g, err := s.Runtime.Store.Read(ctx, scope)
			require.NoError(t, err)
			run := g.Members[0].Runs[0]
			require.NotEmpty(t, run.MessageID)
			require.NotEmpty(t, run.PromptID)
			views, err := s.PendingPrompts(ctx, scope.SessionID)
			require.NoError(t, err)
			require.Len(t, views[""], 1)
			require.Equal(t, "writer", views[""][0].DelegatedTask)
			wrong := types.WithCaller(ctx, types.Caller{TenantID: 7, UserID: "other"})
			views, err = s.PendingPrompts(wrong, scope.SessionID)
			require.NoError(t, err)
			require.Empty(t, views)
			// Reconstruct both broker and scheduler from their durable stores.
			// A committed waiting row does not mean the old worker's post-commit
			// callbacks have returned. A real restart joins the process first.
			stopCollaborationTestRuntime(t, s.Runtime)
			broker = hitl.NewBroker(broker.Store(), nil, hitl.WithSuspensionStore(broker.Suspensions()))
			gate = approval.NewGate(nil, nil, broker)
			s.Runtime = collaboration.New(collaboration.NewStore(s.db), s.run)
			s.Runtime.Reconcile = s.reconcile
			s.BindRecovery(broker, gate, streams)
			answer, err := broker.Resolve(
				ctx,
				hitl.ResolveRequest{
					TenantID: 7,
					UserID:   "web_user:u",
					PromptID: run.PromptID,
					Kind:     types.AgentPromptKindToolApproval,
					Response: []byte(`{"approved":true}`),
				},
			)
			require.NoError(t, err)
			require.True(t, answer.Resume)
			// Simulate a long outage after recording the answer, before enqueueing it.
			expired := time.Now().Add(-time.Minute)
			_, err = broker.Store().
				Transition(ctx,
					run.PromptID,
					types.AgentPromptTransition{
						From: []string{
							types.AgentPromptAnswered,
						},
						To:         types.AgentPromptAnswered,
						LeaseUntil: &expired,
					})
			require.NoError(t, err)
			if cancelTask {
				require.NoError(t, s.Runtime.Cancel(ctx, scope))
			} else {
				_, err = broker.Reap(ctx, time.Now(), hitl.RunHooks{RecoverAnswered: s.RecoverAnswered})
				require.NoError(t, err)
				prompt, err := broker.Store().Get(ctx, run.PromptID)
				require.NoError(t, err)
				require.Equal(t, types.AgentPromptAnswered, prompt.Status)
			}
			startCollaborationTestRuntime(ctx, t, s.Runtime)
			want := "succeeded"
			if cancelTask {
				want = "cancelled"
			}
			require.Eventually(t, func() bool {
				g, err := s.Runtime.Store.Read(ctx, scope)
				// Cancel marks the child immediately; the restarted scheduler still
				// has to close its prompt before the whole group is settled.
				return err == nil && g.Members[0].Runs[0].State == want && (!cancelTask || g.Status == "cancelled")
			}, 3*time.Second, 5*time.Millisecond)
			if cancelTask {
				require.Zero(t, effects.Load())
				p, err := broker.Store().Get(ctx, run.PromptID)
				require.NoError(t, err)
				require.Equal(t, types.AgentPromptCanceled, p.Status)
				g, err = s.Runtime.Store.Read(ctx, scope)
				require.NoError(t, err)
				require.Equal(t, "cancelled", g.Status)
			} else {
				require.Equal(t, int32(1), effects.Load())
			}
			for range 3 {
				s.Runtime.Tick(ctx)
			}
			stopCollaborationTestRuntime(t, s.Runtime)
			require.NotEmpty(t, streams.events)
			var queries []interfaces.StreamEvent
			for _, e := range streams.events {
				if e.Type == types.ResponseTypeAgentQuery {
					queries = append(queries, e)
				}
			}
			require.Len(t, queries, 1)
			query := queries[0]
			if cancelTask {
				require.Zero(t, streams.resets)
				require.NotContains(t, query.Data, "resume_pending_id")
			} else {
				require.Equal(t, 1, streams.resets)
				require.Equal(t, run.PromptID, query.Data["resume_pending_id"])
				require.Equal(t, "write", query.Data["resume_tool_call_id"])
			}
			if cancelTask {
				require.Zero(t, effects.Load())
			} else {
				require.Equal(t, int32(1), effects.Load())
			}
		})
	}
}

type collaborationStreams struct {
	interfaces.StreamManager
}

func (*collaborationStreams) SetLiveRun(context.Context, string, string, string) error { return nil }
func (*collaborationStreams) ClearLiveRun(context.Context, string, string) error       { return nil }
func (*collaborationStreams) AppendEvent(context.Context, string, string, interfaces.StreamEvent) error {
	return nil
}

func TestCollaborationParentAutomaticallyResumesOriginalMessage(t *testing.T) {
	for _, id := range []string{types.BuiltinWeKnoraAssistantID, "ordinary-agent"} {
		t.Run(id, func(t *testing.T) { testCollaborationParentResume(t, id) })
	}
}

func testCollaborationParentResume(t *testing.T, agentID string) {
	s, agents, messages, ctx := collaborationService(t)
	agents.agent.ID = agentID
	scope := collaboration.Scope{TenantID: 7, UserID: "u", SessionID: "parent", ID: "original-message"}
	b, err := s.Runtime.AcquireParent(ctx, scope)
	require.NoError(t, err)
	require.NoError(
		t,
		messages.UpdateMessage(ctx, &types.Message{ID: scope.ID, SessionID: scope.SessionID, Role: "assistant"}),
	)
	request, err := json.Marshal(
		types.ResumableAgentRequest{
			AgentID:       agentID,
			Query:         "original task",
			UserMessageID: "original-user",
		},
	)
	require.NoError(t, err)
	snapshot, err := json.Marshal(
		agent.RunSnapshot{
			Version:           agent.RunSnapshotVersion,
			CollaborationWait: true,
			BetweenRounds:     true,
			Query:             "original task",
			Request:           request,
		},
	)
	require.NoError(t, err)
	require.NoError(t, b.SaveWait(ctx, snapshot, time.Now().Add(time.Hour)))
	require.NoError(t, s.Runtime.Store.Change(ctx, scope, func(g *collaboration.Group) error {
		g.ParentLease = time.Now().Add(-time.Second)
		g.Events = []collaboration.Event{{Sequence: 1, Kind: "succeeded", Content: "child result"}}
		g.NextSequence = 1
		return nil
	}))
	var calls atomic.Int32
	s.streams = &collaborationStreams{}
	s.ParentEvents = func(_ context.Context,
		_ *types.Session,
		m *types.Message,
		bus *event.EventBus,
	) func(context.Context) error {
		bus.On(event.EventAgentComplete, func(_ context.Context, e event.Event) error {
			data := e.Data.(event.AgentCompleteData)
			m.Content = data.FinalAnswer
			m.IsCompleted = true
			return nil
		})
		return func(context.Context) error { return nil }
	}
	s.Bind(&collaborationSessions{run: func(ctx context.Context, req *types.QARequest, bus *event.EventBus) error {
		calls.Add(1)
		if req.AssistantMessageID != scope.ID || req.UserMessageID != "original-user" || req.Resume == nil ||
			req.Resume.Prompt != nil {
			return errors.New("resume identity lost")
		}
		owned := collaboration.FromContext(ctx)
		_, err := s.Runtime.Finish(owned.Context(ctx), scope, 1)
		if err != nil {
			return err
		}
		return bus.Emit(
			ctx,
			event.Event{
				Type: event.EventAgentComplete,
				Data: event.AgentCompleteData{FinalAnswer: "parent continued", MessageID: scope.ID},
			},
		)
	}})
	s.Runtime.Tick(ctx)
	require.Eventually(t, func() bool {
		m, err := messages.GetMessage(ctx, scope.SessionID, scope.ID)
		return err == nil && m.IsCompleted
	}, time.Second, 5*time.Millisecond)
	require.Equal(t, int32(1), calls.Load())
	m, err := messages.GetMessage(ctx, scope.SessionID, scope.ID)
	require.NoError(t, err)
	require.Equal(t, "parent continued", m.Content)
	s.Runtime.Tick(ctx)
	require.Equal(t, int32(1), calls.Load())
}

func TestInterruptedChildPromptIsCleanedEvenAfterFollowup(t *testing.T) {
	s, _, _, ctx := collaborationService(t)
	broker := hitl.NewBroker(nil, nil)
	s.BindRecovery(broker, nil, nil)
	scope := collaboration.Scope{TenantID: 7, UserID: "u", SessionID: "parent", ID: "message"}
	require.NoError(t, s.Runtime.Store.Ensure(ctx, scope))
	id, err := s.Runtime.Spawn(ctx, scope, "spawn", "writer", "a", "{}", "first")
	require.NoError(t, err)
	require.NoError(t, s.Runtime.Store.Change(ctx, scope, func(g *collaboration.Group) error {
		v := g.Members[0].Runs[0]
		v.State = "waiting"
		v.PromptID = "old-prompt"
		v.MessageID = "old-message"
		return nil
	}))
	g, err := s.Runtime.Store.Read(ctx, scope)
	require.NoError(t, err)
	require.NoError(
		t,
		broker.Store().
			Create(ctx,
				&types.AgentPrompt{
					ID:                 "old-prompt",
					TenantID:           7,
					UserID:             "web_user:u",
					SessionID:          g.Members[0].SessionID,
					AssistantMessageID: "old-message",
					Status:             types.AgentPromptSuspended,
				}),
	)
	require.NoError(t, s.Runtime.Interrupt(ctx, scope, id, ""))
	_, err = s.Runtime.Send(ctx, scope, "followup", id, "second", "", true)
	require.NoError(t, err)
	require.NoError(t, s.reconcile(ctx, scope))
	p, err := broker.Store().Get(ctx, "old-prompt")
	require.NoError(t, err)
	require.Equal(t, types.AgentPromptCanceled, p.Status)
	g, err = s.Runtime.Store.Read(ctx, scope)
	require.NoError(t, err)
	require.Empty(t, g.Members[0].Runs[0].PromptID)
	require.Equal(t, "queued", g.Members[0].Runs[1].State)
}

func TestDelegatedSessionCannotResumeThroughOrdinaryQA(t *testing.T) {
	s := &sessionService{}
	err := s.AgentQA(
		context.Background(),
		&types.QARequest{
			Session: &types.Session{ID: "child", Kind: types.SessionKindAgentDelegated},
			Resume:  &types.AgentResume{},
		},
		event.NewEventBus(),
	)
	require.ErrorContains(t, err, "scheduler-owned execution")
}

func TestCollaborationParentSetupFailureStillCompletesMessage(t *testing.T) {
	s, agents, messages, ctx := collaborationService(t)
	agents.agent.ID = types.BuiltinWeKnoraAssistantID
	scope := collaboration.Scope{TenantID: 7, UserID: "u", SessionID: "parent", ID: "message"}
	b, err := s.Runtime.AcquireParent(ctx, scope)
	require.NoError(t, err)
	require.NoError(t, messages.UpdateMessage(ctx, &types.Message{ID: scope.ID, SessionID: scope.SessionID}))
	request, _ := json.Marshal(types.ResumableAgentRequest{AgentID: types.BuiltinWeKnoraAssistantID})
	snapshot, _ := json.Marshal(
		agent.RunSnapshot{
			Version:           agent.RunSnapshotVersion,
			CollaborationWait: true,
			BetweenRounds:     true,
			Request:           request,
		},
	)
	s.streams = &collaborationStreams{}
	var completions int
	s.ParentEvents = func(_ context.Context,
		_ *types.Session,
		m *types.Message,
		bus *event.EventBus,
	) func(context.Context) error {
		bus.On(event.EventAgentComplete, func(_ context.Context, e event.Event) error {
			completions++
			m.IsCompleted = true
			m.Content = e.Data.(event.AgentCompleteData).FinalAnswer
			return nil
		})
		return func(context.Context) error { return nil }
	}
	s.Bind(&collaborationSessions{run: func(context.Context, *types.QARequest, *event.EventBus) error {
		return errors.New("model unavailable before engine starts")
	}})
	require.ErrorContains(t, s.resumeParent(ctx, b, snapshot), "model unavailable")
	require.Equal(t, 1, completions)
	msg, err := messages.GetMessage(ctx, scope.SessionID, scope.ID)
	require.NoError(t, err)
	require.True(t, msg.IsCompleted)
	require.Contains(t, msg.Content, "model unavailable")
}

type cancellationEventStreams struct {
	interfaces.StreamManager
	fail   bool
	events []interfaces.StreamEvent
}

func (s *cancellationEventStreams) AppendEvent(_ context.Context, _, _ string, evt interfaces.StreamEvent) error {
	if s.fail {
		return errors.New("stream temporarily unavailable")
	}
	s.events = append(s.events, evt)
	return nil
}

func TestInterruptedChildCancellationClosesOnlyItsPromptAndRetriesDelivery(t *testing.T) {
	for _, kind := range []string{types.AgentPromptKindToolApproval, types.AgentPromptKindAskUser} {
		t.Run(kind, func(t *testing.T) {
			s, _, _, ctx := collaborationService(t)
			broker := hitl.NewBroker(nil, nil)
			streams := &cancellationEventStreams{fail: true}
			s.BindRecovery(broker, nil, streams)
			scope := collaboration.Scope{TenantID: 7, UserID: "u", SessionID: "parent", ID: "message"}
			_, err := s.Runtime.AcquireParent(ctx, scope)
			require.NoError(t, err)
			for _, name := range []string{"canceled", "sibling"} {
				_, err = s.Runtime.Spawn(ctx, scope, name, name, "a", "{}", "task")
				require.NoError(t, err)
			}
			require.NoError(t, s.Runtime.Store.Change(ctx, scope, func(g *collaboration.Group) error {
				for _, m := range g.Members {
					v := m.Runs[0]
					v.State, v.PromptID, v.MessageID = "waiting", m.Name+"-prompt", m.Name+"-message"
				}
				return nil
			}))
			g, err := s.Runtime.Store.Read(ctx, scope)
			require.NoError(t, err)
			for _, m := range g.Members {
				require.NoError(
					t,
					broker.Store().
						Create(ctx,
							&types.AgentPrompt{
								ID:                 m.Runs[0].PromptID,
								Kind:               kind,
								TenantID:           7,
								UserID:             "web_user:u",
								SessionID:          m.SessionID,
								AssistantMessageID: m.Runs[0].MessageID,
								Status:             types.AgentPromptSuspended,
							}),
				)
			}
			require.NoError(t, s.Runtime.Interrupt(ctx, scope, "canceled", ""))
			require.ErrorContains(t, s.reconcile(ctx, scope), "stream temporarily unavailable")
			g, err = s.Runtime.Store.Read(ctx, scope)
			require.NoError(t, err)
			require.NotEmpty(t, g.Members[0].Runs[0].PromptID, "keep delivery pending across retry/restart")
			streams.fail = false
			require.NoError(t, s.reconcile(ctx, scope))
			require.NoError(t, s.reconcile(ctx, scope))
			var promptEvents []interfaces.StreamEvent
			for _, evt := range streams.events {
				if evt.Type != types.ResponseType("collaboration_status") {
					promptEvents = append(promptEvents, evt)
				}
			}
			require.Len(t, promptEvents, 1)
			data := promptEvents[0].Data
			require.Equal(t, "canceled-prompt", data["pending_id"])
			require.Equal(t, true, data["canceled"])
			require.Equal(t, scope.ID, data["assistant_message_id"])
			g, err = s.Runtime.Store.Read(ctx, scope)
			require.NoError(t, err)
			require.Empty(t, g.Members[0].Runs[0].PromptID)
			require.Equal(t, "waiting", g.Members[1].Runs[0].State)
			require.Equal(t, "sibling-prompt", g.Members[1].Runs[0].PromptID)
			_, err = broker.Resolve(
				ctx,
				hitl.ResolveRequest{
					TenantID: 7,
					UserID:   "web_user:u",
					PromptID: "canceled-prompt",
					Kind:     kind,
					Response: []byte(`{"approved":true}`),
				},
			)
			require.Error(t, err)
		})
	}
}

type interruptedParentStreams struct {
	collaborationStreams
	events chan interfaces.StreamEvent
}

func (s *interruptedParentStreams) AppendEvent(_ context.Context, _, _ string, e interfaces.StreamEvent) error {
	s.events <- e
	return nil
}

func TestCollaborationAbandonedParentClosesTranscriptWithoutModelReplay(t *testing.T) {
	s, _, messages, ctx := collaborationService(t)
	scope := collaboration.Scope{TenantID: 7, UserID: "u", SessionID: "parent", ID: "interrupted-parent"}
	_, err := s.Runtime.AcquireParent(ctx, scope)
	require.NoError(t, err)
	require.NoError(
		t,
		messages.UpdateMessage(ctx, &types.Message{ID: scope.ID, SessionID: scope.SessionID, Role: "assistant"}),
	)
	require.NoError(
		t,
		s.Runtime.Store.Change(
			ctx,
			scope,
			func(g *collaboration.Group) error { g.ParentLease = time.Now().Add(-time.Second); return nil },
		),
	)
	streams := &interruptedParentStreams{events: make(chan interfaces.StreamEvent, 4)}
	s.streams = streams
	var calls atomic.Int32
	s.Bind(
		&collaborationSessions{
			run: func(context.Context, *types.QARequest, *event.EventBus) error { calls.Add(1); return nil },
		},
	)
	startCollaborationTestRuntime(ctx, t, s.Runtime)
	for _, want := range []types.ResponseType{"error", "complete"} {
		select {
		case e := <-streams.events:
			require.Equal(t, want, e.Type)
			require.Contains(t, e.Content, "没有可安全恢复的检查点")
			require.Equal(t, e.Content, e.Data["final_content"])
			require.True(t, e.Done)
		case <-time.After(3 * time.Second):
			t.Fatal("missing terminal event", want)
		}
	}
	m, err := messages.GetMessage(ctx, scope.SessionID, scope.ID)
	require.NoError(t, err)
	require.True(t, m.IsCompleted)
	require.Contains(t, m.Content, "没有可安全恢复的检查点")
	require.Zero(t, calls.Load())
	// Terminal stream delivery precedes the durable acknowledgement.
	require.Eventually(t, func() bool {
		g, err := s.Runtime.Store.Read(ctx, scope)
		return err == nil && g.Status == "cancelled" && !g.ParentClosePending
	}, time.Second, 5*time.Millisecond)
	stopCollaborationTestRuntime(t, s.Runtime)
}

func TestCompletedCollaborationRestoresAllApprovalHistory(t *testing.T) {
	s, _, _, ctx := collaborationService(t)
	broker := hitl.NewBroker(nil, nil)
	s.BindRecovery(broker, nil, nil)
	scope := collaboration.Scope{TenantID: 7, UserID: "u", SessionID: "parent", ID: "message"}
	_, err := s.Runtime.AcquireParent(ctx, scope)
	require.NoError(t, err)
	_, err = s.Runtime.Spawn(ctx, scope, "reader", "reader", "a", "{}", "task")
	require.NoError(t, err)
	require.NoError(t, s.Runtime.Store.Change(ctx, scope, func(g *collaboration.Group) error {
		g.Status = "completed"
		g.Members[0].Runs[0].MessageID = "child-message"
		g.Members[0].Runs[0].State = "succeeded"
		g.Members[0].Runs[0].PromptID = ""
		return nil
	}))
	g, err := s.Runtime.Store.Read(ctx, scope)
	require.NoError(t, err)
	for i, status := range []string{types.AgentPromptDone, types.AgentPromptDone, types.AgentPromptSuspended} {
		require.NoError(
			t,
			broker.Store().
				Create(ctx,
					&types.AgentPrompt{
						ID: fmt.Sprintf("p%d",
							i),
						TenantID:           7,
						UserID:             "web_user:u",
						SessionID:          g.Members[0].SessionID,
						AssistantMessageID: "child-message",
						Kind:               types.AgentPromptKindToolApproval,
						Status:             status,
						CreatedAt:          time.Now().Add(time.Duration(i) * time.Second),
						Response:           types.JSON(`{"approved":true}`),
					}),
		)
	}
	require.NoError(
		t,
		broker.Store().
			Create(ctx,
				&types.AgentPrompt{
					ID:                 "other-owner",
					TenantID:           7,
					UserID:             "web_user:other",
					SessionID:          g.Members[0].SessionID,
					AssistantMessageID: "child-message",
					Kind:               types.AgentPromptKindToolApproval,
					Status:             types.AgentPromptDone,
				}),
	)
	rows, err := s.PendingPrompts(ctx, "parent")
	require.NoError(t, err)
	require.Len(t, rows["message"], 3)
	require.Equal(t, "reader", rows["message"][0].DelegatedTask)
	require.Equal(t, types.AgentPromptDone, rows["message"][0].Status)
	require.Equal(
		t,
		types.AgentPromptCanceled,
		rows["message"][2].Status,
		"stale open prompt must not revive completed group",
	)
}

func TestStopPromptReleasesExpiredChildWithoutStoppingSiblings(t *testing.T) {
	s, _, _, ctx := collaborationService(t)
	scope := collaboration.Scope{TenantID: 7, UserID: "u", SessionID: "parent", ID: "parent-message"}
	require.NoError(t, s.Runtime.Store.Ensure(ctx, scope))
	require.NoError(t, s.Runtime.Store.Change(ctx, scope, func(g *collaboration.Group) error {
		g.ParentMessageID = "parent-message"
		g.Members = []*collaboration.Member{
			{
				ID: "child-1", Name: "writer", SessionID: "child-session",
				Runs: []*collaboration.Run{{
					ID: "run-1", State: "waiting", PromptID: "prompt-1", MessageID: "child-message",
				}},
			},
			{
				ID: "child-2", Name: "reader", SessionID: "other-session",
				Runs: []*collaboration.Run{{ID: "run-2", State: "running"}},
			},
		}
		return nil
	}))
	require.NoError(t, s.StopPrompt(ctx, &types.AgentPrompt{
		ID:                 "prompt-1",
		TenantID:           7,
		UserID:             "web_user:u",
		SessionID:          "child-session",
		AssistantMessageID: "child-message",
		Status:             types.AgentPromptExpired,
	}))
	g, err := s.Runtime.Store.Read(ctx, scope)
	require.NoError(t, err)
	require.Equal(t, "active", g.Status)
	require.Equal(t, "interrupted", g.Members[0].Runs[0].State)
	require.Empty(t, g.Members[0].Runs[0].PromptID)
	require.Equal(t, "running", g.Members[1].Runs[0].State)
	_, err = s.Runtime.Send(ctx, scope, "follow", "writer", "continue the draft", "", true)
	require.NoError(t, err)
	g, err = s.Runtime.Store.Read(ctx, scope)
	require.NoError(t, err)
	require.Equal(t, "queued", g.Members[0].Runs[len(g.Members[0].Runs)-1].State)
}

func TestStopPromptKeepsUncertainChildExecution(t *testing.T) {
	s, _, _, ctx := collaborationService(t)
	scope := collaboration.Scope{TenantID: 7, UserID: "u", SessionID: "parent", ID: "parent-message"}
	require.NoError(t, s.Runtime.Store.Ensure(ctx, scope))
	require.NoError(t, s.Runtime.Store.Change(ctx, scope, func(g *collaboration.Group) error {
		g.ParentMessageID = "parent-message"
		g.Members = []*collaboration.Member{{
			ID: "child-1", Name: "writer", SessionID: "child-session",
			Runs: []*collaboration.Run{{
				ID: "run-1", State: "running", PromptID: "prompt-1", MessageID: "child-message",
			}},
		}}
		return nil
	}))
	require.NoError(t, s.StopPrompt(ctx, &types.AgentPrompt{
		ID:                 "prompt-1",
		TenantID:           7,
		UserID:             "web_user:u",
		SessionID:          "child-session",
		AssistantMessageID: "child-message",
		Status:             types.AgentPromptFailed,
	}))
	g, err := s.Runtime.Store.Read(ctx, scope)
	require.NoError(t, err)
	require.Equal(t, "active", g.Status)
	require.Equal(t, "needs_attention", g.Members[0].Runs[0].State)
	_, err = s.Runtime.Send(ctx, scope, "follow", "writer", "retry", "", true)
	require.Error(t, err)
}

func TestStopPromptStillCancelsTheParentRun(t *testing.T) {
	s, _, _, ctx := collaborationService(t)
	scope := collaboration.Scope{TenantID: 7, UserID: "u", SessionID: "parent", ID: "parent-message"}
	require.NoError(t, s.Runtime.Store.Ensure(ctx, scope))
	require.NoError(t, s.Runtime.Store.Change(ctx, scope, func(g *collaboration.Group) error {
		g.ParentMessageID = "parent-message"
		g.Members = []*collaboration.Member{{
			ID: "child-1", Name: "writer", SessionID: "child-session",
			Runs: []*collaboration.Run{{ID: "run-1", State: "queued"}},
		}}
		return nil
	}))
	require.NoError(t, s.StopPrompt(ctx, &types.AgentPrompt{
		ID:                 "parent-prompt",
		TenantID:           7,
		UserID:             "web_user:u",
		SessionID:          "parent",
		AssistantMessageID: "parent-message",
		Status:             types.AgentPromptExpired,
	}))
	g, err := s.Runtime.Store.Read(ctx, scope)
	require.NoError(t, err)
	require.Contains(t, []string{"cancelling", "cancelled"}, g.Status)
	require.Equal(t, "cancelled", g.Members[0].Runs[0].State)
}

func TestCollaborationPromptStateUsesBrokerDecisionOnly(t *testing.T) {
	for _, tc := range []struct{ status, response, decision string }{
		{"answered", `{"approved":true}`, "approved"},
		{"answered", `{"approved":false}`, "rejected"},
		{"answered", `{}`, ""},
		{"answered", `{"approved":"true"}`, ""},
		{"answered", `broken`, ""},
		{"suspended", `{"approved":true}`, ""},
		{"canceled", `{"approved":true}`, "canceled"},
		{"expired", ``, "expired"},
	} {
		t.Run(tc.status+tc.response, func(t *testing.T) {
			p := &types.AgentPrompt{
				ID:         "prompt",
				Kind:       types.AgentPromptKindToolApproval,
				ToolCallID: "call",
				Status:     tc.status,
				Request: types.JSON(
					"{\"service_name\":\"KM\",\"mcp_tool_name\":\"hot-articles\",\"description\":\"PRIVATE_DESCR" +
						"IPTION\",\"args\":{\"secret\":\"PRIVATE_TOKEN\"}}",
				),
				Response: types.JSON(tc.response),
			}
			projection := collaborationPromptState(p)
			require.Equal(t, tc.decision, projection.Decision)
			require.Equal(t, "hot-articles", projection.Tool)
			require.Equal(t, "call", projection.ToolCallID)
			encoded, err := json.Marshal(projection)
			require.NoError(t, err)
			require.NotContains(t, string(encoded), "PRIVATE_")
		})
	}
	// A question answer must not accidentally be interpreted as tool authorization.
	p := &types.AgentPrompt{
		ID:       "question",
		Kind:     types.AgentPromptKindAskUser,
		Status:   types.AgentPromptAnswered,
		Response: types.JSON(`{"approved":true,"text":"PRIVATE_ANSWER"}`),
	}
	projection := collaborationPromptState(p)
	require.Empty(t, projection.Decision)
	require.Empty(t, projection.Tool)
}
