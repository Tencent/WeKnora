package service

import (
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/agent"
	"github.com/Tencent/WeKnora/internal/agent/approval"
	"github.com/Tencent/WeKnora/internal/agent/collaboration"
	"github.com/Tencent/WeKnora/internal/agent/hitl"
	"github.com/Tencent/WeKnora/internal/agent/tools"
	"github.com/Tencent/WeKnora/internal/event"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

func TestInheritedApprovalBelongsOnlyToOriginalParent(t *testing.T) {
	s, _, _, ctx := collaborationService(t)
	s.BindRecovery(hitl.NewBroker(nil, nil), nil, nil)
	scope := collaboration.Scope{TenantID: 7, UserID: "u", SessionID: "parent", ID: "first-answer"}
	b, err := s.Runtime.AcquireParent(ctx, scope)
	require.NoError(t, err)
	_, err = s.Runtime.Spawn(ctx, scope, "spawn", "child", "a", "{}", "task")
	require.NoError(t, err)
	require.NoError(t, s.Runtime.Store.Change(ctx, scope, func(g *collaboration.Group) error {
		run := g.Members[0].Runs[0]
		run.State, run.MessageID = "succeeded", "child-answer"
		return nil
	}))
	g, err := s.Runtime.Store.Read(ctx, scope)
	require.NoError(t, err)
	require.NoError(t, s.broker.Store().Create(ctx, &types.AgentPrompt{
		ID: "old-prompt", TenantID: 7, UserID: "web_user:u", SessionID: g.Members[0].SessionID,
		AssistantMessageID: "child-answer", Kind: types.AgentPromptKindToolApproval, Status: types.AgentPromptDone,
		Request:  types.JSON(`{"service_id":"service","mcp_tool_name":"tool","args":{}}`),
		Response: types.JSON(`{"approved":true}`),
	}))
	finished, err := s.Runtime.Finish(b.Context(ctx), scope, 0)
	require.NoError(t, err)
	require.True(t, finished)
	next := scope
	next.ID = "second-answer"
	_, err = s.Runtime.AcquireParent(ctx, next)
	require.NoError(t, err)
	views, err := s.PendingPrompts(ctx, scope.SessionID)
	require.NoError(t, err)
	require.Len(t, views[scope.ID], 1)
	t.Logf("original approvals=%d new-turn approvals=%d", len(views[scope.ID]), len(views[next.ID]))
	require.Empty(t, views[next.ID], "new parent did not run any task or request approval")
	_, err = s.Runtime.Send(ctx, next, "continue", g.Members[0].ID, "continue task", "", true)
	require.NoError(t, err)
	require.NoError(t, s.Runtime.Store.Change(ctx, next, func(g *collaboration.Group) error {
		g.Members[0].Runs[1].MessageID = "new-child-answer"
		return nil
	}))
	require.NoError(t, s.broker.Store().Create(ctx, &types.AgentPrompt{
		ID: "new-prompt", TenantID: 7, UserID: "web_user:u", SessionID: g.Members[0].SessionID,
		AssistantMessageID: "new-child-answer", Kind: types.AgentPromptKindToolApproval, Status: types.AgentPromptDone,
		Request: types.JSON(`{"args":{}}`), Response: types.JSON(`{"approved":true}`),
	}))
	views, err = s.PendingPrompts(ctx, scope.SessionID)
	require.NoError(t, err)
	require.Len(t, views[scope.ID], 1)
	require.Len(t, views[next.ID], 1, "continuing an inherited child must not reattach the baseline approval")
	require.Equal(t, "new-prompt", views[next.ID][0].ID)
}

type childConfirmationGate struct {
	approval.BuiltinApproval
	approved           bool
	requests, executed int
}

func (g *childConfirmationGate) RequestBuiltinApproval(
	context.Context,
	approval.PendingRequest,
) (approval.Decision, error) {
	g.requests++
	return approval.Decision{Approved: g.approved, PendingID: "approval"}, nil
}
func (g *childConfirmationGate) MarkExecuted(context.Context, string) { g.executed++ }

func TestChildWikiWriteMustNotBypassConfirmation(t *testing.T) {
	for name := range assistantWriteToolRisk {
		levels := []types.AssistantAccessLevel{types.AssistantAccessReadOnly, types.AssistantAccessConfirm}
		for _, level := range levels {
			t.Run(name+"/"+string(level), func(t *testing.T) { testChildWikiConfirmation(t, name, level) })
		}
	}
	t.Run("delete/auto_write", func(t *testing.T) {
		testChildWikiConfirmation(t, tools.ToolWikiDeletePage, types.AssistantAccessAutoWrite)
	})
}

func testChildWikiConfirmation(t *testing.T, name string, level types.AssistantAccessLevel, inherited ...bool) {
	s, agents, _, ctx := collaborationService(t)
	s.tenants = &collaborationTenants{level: level}
	scope := collaboration.Scope{TenantID: 7, UserID: "u", SessionID: "parent", ID: "parent-answer"}
	require.NoError(t, s.Runtime.Store.Ensure(ctx, scope))
	snapshot, err := s.resolve(ctx, "a", level)
	if len(inherited) > 0 && inherited[0] {
		ctx = context.WithValue(ctx, childSourceKey{}, childSource{Agent: agents.agent, ModelID: "model"})
		snapshot, err = inheritedChildSnapshot(ctx, &types.AgentConfig{AssistantAccess: level}, []string{name})
	}
	require.NoError(t, err)
	_, err = s.Runtime.Spawn(ctx, scope, "spawn", "child", "a", snapshot, "task")
	require.NoError(t, err)
	require.NoError(t, s.Runtime.Store.Change(ctx, scope, func(g *collaboration.Group) error {
		r := g.Members[0].Runs[0]
		r.State, r.Owner, r.Lease = "running", "owner", time.Now().Add(time.Minute)
		return nil
	}))
	g, err := s.Runtime.Store.Read(ctx, scope)
	require.NoError(t, err)
	b := &collaboration.Binding{
		Runtime: s.Runtime,
		Scope:   scope,
		Child:   &collaboration.Work{Scope: scope, Member: *g.Members[0], Run: *g.Members[0].Runs[0]},
	}
	gate := &childConfirmationGate{}
	s.gate = gate
	registry := tools.NewToolRegistry()
	write := &collaborationWriteTool{
		BaseTool: tools.NewBaseTool(name, "fake harmless delete", json.RawMessage(`{"type":"object"}`)),
	}
	registry.RegisterTool(write)
	if len(inherited) > 0 && inherited[0] {
		registry.RegisterTool(&collaborationWriteTool{BaseTool: tools.NewBaseTool("newly_available_tool", "new", nil)})
	}
	require.NoError(
		t,
		s.attach(
			collaboration.WithBinding(ctx, b),
			&agent.AgentEngine{},
			registry,
			&types.AgentConfig{},
			g.Members[0].SessionID,
			"child-answer",
		),
	)
	if len(inherited) > 0 && inherited[0] {
		_, err := registry.GetTool("newly_available_tool")
		require.Error(t, err, "child must not acquire newly available tools")
	}
	registered, err := registry.GetTool(name)
	require.NoError(t, err)
	result, err := registry.ExecuteTool(ctx, name, json.RawMessage(`{}`))
	t.Logf("tool_type=%T effect_executed=%v result=%+v error=%v", registered, write.called, result, err)
	require.NoError(t, err)
	require.False(t, result.Success)
	require.False(t, write.called, "missing interactive approval must fail closed")
	approvedCtx := tools.WithToolExecContext(ctx, &tools.ToolExecContext{
		EventBus: event.NewEventBus(), Mode: types.RunModeAttended, SessionID: g.Members[0].SessionID,
		AssistantMessageID: "child-answer", UserID: "web_user:u", ToolCallID: "write-call",
	})
	result, err = registry.ExecuteTool(approvedCtx, name, json.RawMessage(`{}`))
	require.NoError(t, err)
	require.False(t, result.Success)
	if level == types.AssistantAccessReadOnly {
		require.False(t, write.called, "a read-only child cannot write even with approval")
		require.Zero(t, gate.requests, "a read-only child does not ask for a write")
		return
	}
	require.False(t, write.called, "denied approval must not execute")
	require.Equal(t, 1, gate.requests)
	gate.approved = true
	result, err = registry.ExecuteTool(approvedCtx, name, json.RawMessage(`{}`))
	require.NoError(t, err)
	require.True(t, result.Success)
	require.True(t, write.called)
	require.Equal(t, 2, gate.requests)
	require.Equal(t, 1, gate.executed)
}

func TestChildPanicMustFinalizeTranscript(t *testing.T) {
	s, _, messages, ctx := collaborationService(t)
	streams := &childTestStreams{}
	s.streams = streams
	scope := collaboration.Scope{TenantID: 7, UserID: "u", SessionID: "parent", ID: "group"}
	require.NoError(t, s.Runtime.Store.Ensure(ctx, scope))
	config, err := s.resolve(ctx, "a", types.AssistantAccessReadOnly)
	require.NoError(t, err)
	_, err = s.Runtime.Spawn(ctx, scope, "spawn", "worker", "a", config, "test")
	require.NoError(t, err)
	var work collaboration.Work
	require.NoError(t, s.Runtime.Store.Change(ctx, scope, func(g *collaboration.Group) error {
		m := g.Members[0]
		r := m.Runs[0]
		r.State, r.Owner, r.Lease = "running", "owner", time.Now().Add(time.Minute)
		work = collaboration.Work{Scope: scope, Member: *m, Run: *r}
		return nil
	}))
	finalized := false
	s.ChildEvents = func(context.Context, *types.Session, *types.Message, *event.EventBus) func(context.Context) error {
		return func(context.Context) error { finalized = true; return nil }
	}
	s.Bind(&collaborationSessions{run: func(context.Context, *types.QARequest, *event.EventBus) error {
		panic("executor failure")
	}})
	result, err := s.run(ctx, work)
	require.ErrorContains(t, err, "executor failure")
	require.Contains(t, result.Answer, "executor failure")
	t.Logf("persisted_messages=%d stream_finalized=%v live=%q", len(messages.saved), finalized, streams.live)
	require.Len(t, messages.saved, 1, "panic must persist terminal transcript before runtime marks run failed")
	require.True(t, messages.saved[0].IsCompleted)
	require.True(t, finalized, "panic must close the child stream")
}

type parentClosureStreams struct {
	childTestStreams
	fail bool
}

func (s *parentClosureStreams) AppendEvent(
	ctx context.Context,
	session, message string,
	e interfaces.StreamEvent,
) error {
	if s.fail {
		return errors.New("delivery unavailable")
	}
	return s.childTestStreams.AppendEvent(ctx, session, message, e)
}

type parentClosureMessages struct {
	*collaborationMessages
	fail bool
}

func (m *parentClosureMessages) UpdateMessage(ctx context.Context, msg *types.Message) error {
	if m.fail {
		return errors.New("persistence unavailable")
	}
	return m.collaborationMessages.UpdateMessage(ctx, msg)
}

func TestExpiredParentClosurePreservesTranscriptAndRetriesStream(t *testing.T) {
	s, _, messages, ctx := collaborationService(t)
	streams := &parentClosureStreams{fail: true}
	s.streams = streams
	scope := collaboration.Scope{TenantID: 7, UserID: "u", SessionID: "parent", ID: "answer"}
	b, err := s.Runtime.AcquireParent(ctx, scope)
	require.NoError(t, err)
	require.NoError(t, messages.UpdateMessage(ctx, &types.Message{
		ID: scope.ID, SessionID: scope.SessionID, Content: "saved progress",
		AgentSteps: []types.AgentStep{{Thought: "saved timeline"}},
	}))
	require.NoError(t, s.Runtime.Store.Change(ctx, scope, func(g *collaboration.Group) error {
		g.Status = "cancelling"
		g.ParentClosePending = true
		g.Deadline = time.Now().Add(-time.Second)
		return nil
	}))
	faultyMessages := &parentClosureMessages{collaborationMessages: messages, fail: true}
	s.messages = faultyMessages
	require.ErrorContains(t, s.finishInterruptedParent(ctx, b), "persistence unavailable")
	require.Empty(t, streams.events, "never publish completion before its transcript is saved")
	require.Len(t, messages.saved, 1)
	faultyMessages.fail = false
	require.ErrorContains(t, s.finishInterruptedParent(ctx, b), "delivery unavailable")
	msg, err := messages.GetMessage(ctx, scope.SessionID, scope.ID)
	require.NoError(t, err)
	require.True(t, msg.IsCompleted)
	require.Contains(t, msg.Content, "saved progress")
	require.Contains(t, msg.Content, "超过执行时限")
	require.Len(t, msg.AgentSteps, 1)
	streams.fail = false
	require.NoError(t, s.finishInterruptedParent(ctx, b))
	require.Len(t, messages.saved, 2, "retry must not overwrite or duplicate the saved answer")
	require.Len(t, streams.events, 1)
	require.Equal(t, types.ResponseType("complete"), streams.events[0].Type)
	require.Equal(t, msg.Content, streams.events[0].Data["final_content"])
}

func TestChildPanicAfterDeliveredFollowupPersistsFailureWithoutReplay(t *testing.T) {
	s, _, messages, ctx := collaborationService(t)
	streams := &childTestStreams{}
	s.streams = streams
	scope := collaboration.Scope{TenantID: 7, UserID: "u", SessionID: "parent", ID: "group"}
	require.NoError(t, s.Runtime.Store.Ensure(ctx, scope))
	config, err := s.resolve(ctx, "a", types.AssistantAccessReadOnly)
	require.NoError(t, err)
	id, err := s.Runtime.Spawn(ctx, scope, "spawn", "child", "a", config, "task")
	require.NoError(t, err)
	_, err = s.Runtime.Send(ctx, scope, "followup", id, "additional work", "", true)
	require.NoError(t, err)
	var effects atomic.Int32
	s.ChildEvents = func(
		_ context.Context, session *types.Session, answer *types.Message, bus *event.EventBus,
	) func(context.Context) error {
		bus.On(event.EventAgentThought, func(context.Context, event.Event) error {
			answer.AgentSteps = []types.AgentStep{{Thought: "progress before panic"}}
			return nil
		})
		return func(ctx context.Context) error {
			return streams.AppendEvent(ctx, session.ID, answer.ID, interfaces.StreamEvent{Type: "complete"})
		}
	}
	s.Bind(&collaborationSessions{run: func(ctx context.Context, _ *types.QARequest, bus *event.EventBus) error {
		b := collaboration.FromContext(ctx)
		if _, err := b.Poll(ctx); err != nil {
			return err
		}
		if err := bus.Emit(ctx, event.Event{Type: event.EventAgentThought}); err != nil {
			return err
		}
		effects.Add(1)
		panic("panic after effect")
	}})
	s.Runtime.Poll = 5 * time.Millisecond
	startCollaborationTestRuntime(ctx, t, s.Runtime)
	require.Eventually(t, func() bool {
		g, err := s.Runtime.Store.Read(ctx, scope)
		return err == nil && g.Members[0].Runs[0].State == "failed"
	}, 3*time.Second, 5*time.Millisecond)
	stopCollaborationTestRuntime(t, s.Runtime)
	require.Len(t, messages.saved, 1)
	require.True(t, messages.saved[0].IsCompleted)
	require.Contains(t, messages.saved[0].Content, "panic after effect")
	require.Len(t, messages.saved[0].AgentSteps, 1)
	require.Equal(t, types.ResponseType("complete"), streams.events[len(streams.events)-1].Type)
	// A new scheduler sees the durable failed run and its consumed mailbox.
	restarted := collaboration.New(s.Runtime.Store, s.run)
	for range 3 {
		restarted.Tick(ctx)
	}
	g, err := s.Runtime.Store.Read(ctx, scope)
	require.NoError(t, err)
	require.Len(t, g.Members[0].Runs, 1)
	require.Positive(t, g.Members[0].Runs[0].Result.ConsumedSequence)
	require.Equal(t, int32(1), effects.Load())
}
