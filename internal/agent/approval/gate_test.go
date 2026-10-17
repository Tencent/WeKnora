package approval

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/event"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func resolveFor(g *Gate, tenantID uint64, userID, pendingID string, d Decision) error {
	return g.Resolve(context.Background(), ResolveRequest{
		TenantID: tenantID, UserID: userID, PendingID: pendingID, Decision: d,
	})
}

type stubChecker struct {
	required   bool
	err        error
	enabled    *bool
	enabledErr error
}

func (s *stubChecker) IsRequired(ctx context.Context, tenantID uint64, serviceID, toolName string) (bool, error) {
	return s.required, s.err
}

func (s *stubChecker) IsEnabled(ctx context.Context, tenantID uint64, serviceID, toolName string) (bool, error) {
	if s.enabledErr != nil {
		return false, s.enabledErr
	}
	if s.enabled == nil {
		return true, nil
	}
	return *s.enabled, nil
}

func TestGate_RequestAndWait_Approve(t *testing.T) {
	bus := event.NewEventBus()
	g := NewGate(&config.Config{Agent: &config.AgentConfig{ToolApprovalTimeoutSeconds: 2}}, &stubChecker{required: true}, nil)

	ctx := context.Background()
	req := PendingRequest{
		TenantID:           1,
		SessionID:          "s1",
		AssistantMessageID: "m1",
		EventBus:           bus,
		ServiceID:          "svc",
		ServiceName:        "svcname",
		MCPToolName:        "danger_tool",
		RegisteredToolName: "mcp_svcname_danger_tool",
		Description:        "desc",
		Args:               json.RawMessage(`{"a":1}`),
		ToolCallID:         "tc1",
	}

	bus.On(event.EventToolApprovalRequired, func(_ context.Context, evt event.Event) error {
		data, ok := evt.Data.(event.ToolApprovalRequiredData)
		require.True(t, ok)
		require.NotEmpty(t, data.PendingID)
		go func() {
			_ = resolveFor(g, 1, "", data.PendingID, Decision{Approved: true, ModifiedArgs: json.RawMessage(`{"a":2}`)})
		}()
		return nil
	})

	d, err := g.RequestAndWait(ctx, req)
	require.NoError(t, err)
	require.True(t, d.Approved)
	require.JSONEq(t, `{"a":2}`, string(d.ModifiedArgs))
}

func TestGate_RequestAndWait_Timeout(t *testing.T) {
	g := NewGate(&config.Config{Agent: &config.AgentConfig{ToolApprovalTimeoutSeconds: 1}}, &stubChecker{required: true}, nil)
	ctx := context.Background()
	req := PendingRequest{
		TenantID:           1,
		SessionID:          "s1",
		AssistantMessageID: "m1",
		EventBus:           event.NewEventBus(),
		ServiceID:          "svc",
		ServiceName:        "svcname",
		MCPToolName:        "t",
		RegisteredToolName: "mcp_svcname_t",
		Args:               json.RawMessage(`{}`),
	}
	d, err := g.RequestAndWait(ctx, req)
	require.NoError(t, err)
	require.False(t, d.Approved)
	require.True(t, d.TimedOut)
}

func TestGate_UnattendedRunRefusesWithoutAsking(t *testing.T) {
	g := NewGate(&config.Config{Agent: &config.AgentConfig{ToolApprovalTimeoutSeconds: 30}},
		&stubChecker{required: true}, nil)
	bus := event.NewEventBus()
	raised := 0
	bus.On(event.EventToolApprovalRequired, func(context.Context, event.Event) error {
		raised++
		return nil
	})
	req := PendingRequest{
		TenantID: 1, UserID: "u1", SessionID: "s1", AssistantMessageID: "m1", EventBus: bus,
		ServiceID: "svc", ServiceName: "svcname", MCPToolName: "t", RegisteredToolName: "mcp_svcname_t",
		Args: json.RawMessage(`{}`),
	}
	ctx := types.WithUnattendedRun(context.Background())

	started := time.Now()
	d, err := g.RequestAndWait(ctx, req)
	require.NoError(t, err)
	require.False(t, d.Approved)
	require.Equal(t, UnattendedApprovalReason, d.Reason)

	d, err = g.RequestBuiltinApproval(ctx, req)
	require.NoError(t, err)
	require.False(t, d.Approved)
	require.Equal(t, UnattendedApprovalReason, d.Reason)

	require.Less(t, time.Since(started), time.Second, "nothing waits for an answer")
	require.Zero(t, raised, "no card is shown to nobody")
	prompts, err := g.broker.SessionPrompts(context.Background(), "s1", []string{
		types.AgentPromptPending, types.AgentPromptAnswered, types.AgentPromptExpired,
		types.AgentPromptCanceled, types.AgentPromptSuspended,
	})
	require.NoError(t, err)
	require.Empty(t, prompts, "no prompt is left pending")
}

func TestGate_NeedsApproval_NoChecker(t *testing.T) {
	g := NewGate(nil, nil, nil)
	require.False(t, g.NeedsApproval(context.Background(), 1, "x", "y"))
}

func TestGate_Resolve_NotFound(t *testing.T) {
	g := NewGate(&config.Config{Agent: &config.AgentConfig{ToolApprovalTimeoutSeconds: 1}}, &stubChecker{required: true}, nil)
	err := resolveFor(g, 1, "", "no-such-id", Decision{Approved: true})
	require.ErrorIs(t, err, ErrPendingNotFound)
}

func TestGate_Resolve_TenantMismatch(t *testing.T) {
	bus := event.NewEventBus()
	g := NewGate(&config.Config{Agent: &config.AgentConfig{ToolApprovalTimeoutSeconds: 2}}, &stubChecker{required: true}, nil)
	req := PendingRequest{
		TenantID: 1, EventBus: bus, SessionID: "s1", AssistantMessageID: "m1",
		ServiceID: "svc", MCPToolName: "t", Args: json.RawMessage(`{}`),
	}
	bus.On(event.EventToolApprovalRequired, func(_ context.Context, evt event.Event) error {
		data := evt.Data.(event.ToolApprovalRequiredData)
		go func() {
			require.ErrorIs(t, resolveFor(g, 999, "", data.PendingID, Decision{Approved: true}), ErrTenantMismatch)
			_ = resolveFor(g, 1, "", data.PendingID, Decision{Approved: false, Reason: "no"})
		}()
		return nil
	})
	d, err := g.RequestAndWait(context.Background(), req)
	require.NoError(t, err)
	require.False(t, d.Approved)
}

func TestGate_Resolve_UserMismatch(t *testing.T) {
	bus := event.NewEventBus()
	g := NewGate(&config.Config{Agent: &config.AgentConfig{ToolApprovalTimeoutSeconds: 2}}, &stubChecker{required: true}, nil)
	req := PendingRequest{
		TenantID: 1, UserID: "alice", EventBus: bus,
		SessionID: "s1", AssistantMessageID: "m1",
		ServiceID: "svc", MCPToolName: "t", Args: json.RawMessage(`{}`),
	}
	bus.On(event.EventToolApprovalRequired, func(_ context.Context, evt event.Event) error {
		data := evt.Data.(event.ToolApprovalRequiredData)
		go func() {
			require.ErrorIs(t, resolveFor(g, 1, "bob", data.PendingID, Decision{Approved: true}), ErrUserMismatch)
			_ = resolveFor(g, 1, "alice", data.PendingID, Decision{Approved: true})
		}()
		return nil
	})
	d, err := g.RequestAndWait(context.Background(), req)
	require.NoError(t, err)
	require.True(t, d.Approved)
}

// TestGate_Resolve_EmptyUserIDRejectedWhenWaiterHasUser guards against the
// previous fail-open short-circuit where an empty caller userID skipped the
// per-user check entirely (allowing same-tenant cross-user approval).
func TestGate_Resolve_EmptyUserIDRejectedWhenWaiterHasUser(t *testing.T) {
	bus := event.NewEventBus()
	g := NewGate(&config.Config{Agent: &config.AgentConfig{ToolApprovalTimeoutSeconds: 2}}, &stubChecker{required: true}, nil)
	req := PendingRequest{
		TenantID: 1, UserID: "alice", EventBus: bus,
		SessionID: "s1", AssistantMessageID: "m1",
		ServiceID: "svc", MCPToolName: "t", Args: json.RawMessage(`{}`),
	}
	bus.On(event.EventToolApprovalRequired, func(_ context.Context, evt event.Event) error {
		data := evt.Data.(event.ToolApprovalRequiredData)
		go func() {
			require.ErrorIs(t, resolveFor(g, 1, "", data.PendingID, Decision{Approved: true}), ErrUserMismatch)
			_ = resolveFor(g, 1, "alice", data.PendingID, Decision{Approved: false, Reason: "no"})
		}()
		return nil
	})
	d, err := g.RequestAndWait(context.Background(), req)
	require.NoError(t, err)
	require.False(t, d.Approved)
}

func TestGate_Resolve_AlreadyResolvedAfterTimeout(t *testing.T) {
	g := NewGate(&config.Config{Agent: &config.AgentConfig{ToolApprovalTimeoutSeconds: 1}}, &stubChecker{required: true}, nil)
	bus := event.NewEventBus()

	var pendingID string
	gotPending := make(chan struct{}, 1)
	bus.On(event.EventToolApprovalRequired, func(_ context.Context, evt event.Event) error {
		pendingID = evt.Data.(event.ToolApprovalRequiredData).PendingID
		gotPending <- struct{}{}
		return nil
	})

	lateErr := make(chan error, 1)
	go func() {
		<-gotPending
		// 1s timeout + small slack: the prompt row outlives the wait and
		// records the timeout, so a late decision cannot reopen it.
		<-time.After(1500 * time.Millisecond)
		lateErr <- resolveFor(g, 1, "", pendingID, Decision{Approved: true})
	}()

	d, err := g.RequestAndWait(context.Background(), PendingRequest{
		TenantID: 1, EventBus: bus, SessionID: "s",
		ServiceID: "svc", MCPToolName: "t", Args: json.RawMessage(`{}`),
	})
	require.NoError(t, err)
	require.True(t, d.TimedOut)
	require.ErrorIs(t, <-lateErr, ErrAlreadyResolved)

	row, err := g.broker.Store().Get(context.Background(), pendingID)
	require.NoError(t, err)
	require.Equal(t, types.AgentPromptExpired, row.Status)
}

func TestGate_ApprovedCallStaysExecutingUntilMarked(t *testing.T) {
	g := NewGate(&config.Config{Agent: &config.AgentConfig{ToolApprovalTimeoutSeconds: 5}},
		&stubChecker{required: true}, nil)
	bus := event.NewEventBus()
	bus.On(event.EventToolApprovalRequired, func(_ context.Context, evt event.Event) error {
		pendingID := evt.Data.(event.ToolApprovalRequiredData).PendingID
		go func() {
			require.ErrorIs(t,
				resolveFor(g, 1, "", pendingID, Decision{Approved: true, ModifiedArgs: json.RawMessage(`[1]`)}),
				ErrInvalidDecision)
			require.ErrorIs(t, g.Resolve(context.Background(), ResolveRequest{
				TenantID: 1, Kind: types.AgentPromptKindMCPOAuth, PendingID: pendingID,
				Decision: Decision{Approved: true},
			}), ErrPendingNotFound)
			require.NoError(t, resolveFor(g, 1, "", pendingID, Decision{Approved: true}))
		}()
		return nil
	})

	d, err := g.RequestAndWait(context.Background(), PendingRequest{
		TenantID: 1, EventBus: bus, SessionID: "s",
		ServiceID: "svc", MCPToolName: "t", Args: json.RawMessage(`{}`),
	})
	require.NoError(t, err)
	require.True(t, d.Approved)

	row, err := g.broker.Store().Get(context.Background(), d.PendingID)
	require.NoError(t, err)
	require.Equal(t, types.AgentPromptExecuting, row.Status)
	var request types.ToolApprovalPromptRequest
	require.NoError(t, json.Unmarshal(row.Request, &request))
	require.Equal(t, "svc", request.ServiceID)

	g.MarkExecuted(context.Background(), d.PendingID)
	row, err = g.broker.Store().Get(context.Background(), d.PendingID)
	require.NoError(t, err)
	require.Equal(t, types.AgentPromptDone, row.Status)
}

func TestGate_Resolve_RaceWinsAlreadyResolved(t *testing.T) {
	g := NewGate(&config.Config{Agent: &config.AgentConfig{ToolApprovalTimeoutSeconds: 30}}, &stubChecker{required: true}, nil)
	bus := event.NewEventBus()

	type result struct {
		first  error
		second error
	}
	resCh := make(chan result, 1)

	bus.On(event.EventToolApprovalRequired, func(_ context.Context, evt event.Event) error {
		pendingID := evt.Data.(event.ToolApprovalRequiredData).PendingID
		go func() {
			err1 := resolveFor(g, 1, "", pendingID, Decision{Approved: true})
			err2 := resolveFor(g, 1, "", pendingID, Decision{Approved: false})
			resCh <- result{first: err1, second: err2}
		}()
		return nil
	})

	d, err := g.RequestAndWait(context.Background(), PendingRequest{
		TenantID: 1, EventBus: bus, SessionID: "s",
		ServiceID: "svc", MCPToolName: "t", Args: json.RawMessage(`{}`),
	})
	require.NoError(t, err)
	require.True(t, d.Approved)
	r := <-resCh
	require.NoError(t, r.first)
	require.ErrorIs(t, r.second, ErrAlreadyResolved)
}

func boolPtr(v bool) *bool { return &v }

func TestGate_IsEnabled_NoCheckerKeepsToolsOn(t *testing.T) {
	g := NewGate(nil, nil, nil)
	enabled, err := g.IsEnabled(context.Background(), 1, "svc", "tool")
	require.NoError(t, err)
	require.True(t, enabled)
}

func TestGate_IsEnabled_MissingTenantFailClosed(t *testing.T) {
	g := NewGate(nil, &stubChecker{}, nil)
	enabled, err := g.IsEnabled(context.Background(), 0, "svc", "tool")
	require.NoError(t, err)
	require.False(t, enabled)
}

func TestGate_IsEnabled_HonorsChecker(t *testing.T) {
	g := NewGate(nil, &stubChecker{enabled: boolPtr(false)}, nil)
	enabled, err := g.IsEnabled(context.Background(), 1, "svc", "tool")
	require.NoError(t, err)
	require.False(t, enabled)
}

func TestGate_IsEnabled_CheckerErrorPropagates(t *testing.T) {
	g := NewGate(nil, &stubChecker{enabledErr: context.DeadlineExceeded}, nil)
	enabled, err := g.IsEnabled(context.Background(), 1, "svc", "tool")
	require.Error(t, err)
	require.False(t, enabled)
}

func TestAdapter_IsEnabled(t *testing.T) {
	a := &Adapter{Svc: &stubChecker{enabled: boolPtr(false)}}
	enabled, err := a.IsEnabled(context.Background(), 1, "svc", "tool")
	require.NoError(t, err)
	require.False(t, enabled)

	empty := &Adapter{}
	enabled, err = empty.IsEnabled(context.Background(), 1, "svc", "tool")
	require.NoError(t, err)
	require.True(t, enabled)
}

func TestGate_RequestAndWait_SuspendsAfterHotWait(t *testing.T) {
	bus := event.NewEventBus()
	g := NewGate(&config.Config{Agent: &config.AgentConfig{
		ToolApprovalTimeoutSeconds: 60, PromptHotWaitSeconds: 1,
	}}, &stubChecker{required: true}, nil)

	var required event.ToolApprovalRequiredData
	bus.On(event.EventToolApprovalRequired, func(_ context.Context, evt event.Event) error {
		required = evt.Data.(event.ToolApprovalRequiredData)
		return nil
	})
	resolved := false
	bus.On(event.EventToolApprovalResolved, func(context.Context, event.Event) error {
		resolved = true
		return nil
	})

	d, err := g.RequestAndWait(context.Background(), PendingRequest{
		TenantID: 1, SessionID: "s1", AssistantMessageID: "m1", EventBus: bus,
		ServiceID: "svc", ServiceName: "svcname", MCPToolName: "t", RegisteredToolName: "mcp_svcname_t",
		Args: json.RawMessage(`{}`), ToolCallID: "tc1",
		Snapshot: []byte(`{"version":1}`), SnapshotVersion: 1,
	})
	require.NoError(t, err)
	require.False(t, d.Approved)
	require.False(t, d.TimedOut)
	require.NotNil(t, d.Suspended)
	require.Equal(t, d.PendingID, d.Suspended.PromptID)
	require.Equal(t, types.AgentPromptKindToolApproval, d.Suspended.Kind)
	require.NotNil(t, d.Suspended.ExpiresAt)

	require.True(t, required.Suspendable)
	require.Equal(t, 1, required.HotWaitSeconds)
	require.False(t, resolved, "a suspended approval is still open")
}

func TestGate_ResolveSuspendedThenResumeDecision(t *testing.T) {
	bus := event.NewEventBus()
	g := NewGate(&config.Config{Agent: &config.AgentConfig{
		ToolApprovalTimeoutSeconds: 60, PromptHotWaitSeconds: 1,
	}}, &stubChecker{required: true}, nil)
	d, err := g.RequestAndWait(context.Background(), PendingRequest{
		TenantID: 1, SessionID: "s1", AssistantMessageID: "m1", EventBus: bus,
		ServiceID: "svc", ServiceName: "svcname", MCPToolName: "t", RegisteredToolName: "mcp_svcname_t",
		Args: json.RawMessage(`{"a":1}`), ToolCallID: "tc1",
		Snapshot: []byte(`{"version":1}`), SnapshotVersion: 1,
	})
	require.NoError(t, err)
	require.NotNil(t, d.Suspended)

	res, err := g.ResolveForResume(context.Background(), ResolveRequest{
		TenantID: 1, PendingID: d.PendingID,
		Decision: Decision{Approved: true, ModifiedArgs: json.RawMessage(`{"a":2}`)},
	})
	require.NoError(t, err)
	require.True(t, res.Resume)

	var resolved event.ToolApprovalResolvedData
	resumeBus := event.NewEventBus()
	resumeBus.On(event.EventToolApprovalResolved, func(_ context.Context, evt event.Event) error {
		resolved = evt.Data.(event.ToolApprovalResolvedData)
		return nil
	})
	got := g.ResumeDecision(context.Background(), res.Prompt, resumeBus)
	require.True(t, got.Approved)
	require.JSONEq(t, `{"a":2}`, string(got.ModifiedArgs))
	require.Equal(t, d.PendingID, resolved.PendingID)
	require.True(t, resolved.Approved)
	require.False(t, g.ResumeDecision(context.Background(), res.Prompt, nil).Approved,
		"an approval can execute only once")

	row, err := g.broker.Store().Get(context.Background(), d.PendingID)
	require.NoError(t, err)
	require.Equal(t, types.AgentPromptExecuting, row.Status)
	g.MarkExecuted(context.Background(), d.PendingID)
	row, err = g.broker.Store().Get(context.Background(), d.PendingID)
	require.NoError(t, err)
	require.Equal(t, types.AgentPromptDone, row.Status)
}

func TestValidateToolApprovalResponse_IsStrict(t *testing.T) {
	for body, ok := range map[string]bool{
		`{"approved":true}`:                               true,
		`{"approved":false,"reason":"no"}`:                true,
		`{"approved":true,"modified_args":{"q":"x"}}`:     true,
		`{"decision":"approve"}`:                          false,
		`{}`:                                              false,
		`{"approved":true,"extra":1}`:                     false,
		`{"approved":false,"modified_args":{"q":"x"}}`:    false,
		`{"approved":true,"modified_args":null}`:          false,
		`{"approved":true,"modified_args":["not","obj"]}`: false,
	} {
		err := validateToolApprovalResponse(nil, json.RawMessage(body))
		if ok {
			require.NoError(t, err, body)
		} else {
			require.Error(t, err, body)
		}
	}
}

func TestAnsweredPromptOnlyCurrentOwnerCanContinue(t *testing.T) {
	for _, kind := range []string{types.AgentPromptKindToolApproval, types.AgentPromptKindAskUser} {
		for _, accept := range []bool{true, false} {
			name := kind + "/decline"
			if accept {
				name = kind + "/accept"
			}
			t.Run(name, func(t *testing.T) {
				ctx := t.Context()
				g := NewGate(nil, nil, nil)
				response := `{"approved":false}`
				if kind == types.AgentPromptKindAskUser {
					response = `{"declined":true}`
					if accept {
						response = `{"text":"staging"}`
					}
				} else if accept {
					response = `{"approved":true}`
				}
				current := &types.AgentPrompt{
					ID: "p", Kind: kind, Status: types.AgentPromptAnswered,
					OwnerInstance: "new-run", Response: types.JSON(response),
				}
				require.NoError(t, g.broker.Store().Create(ctx, current))
				stale := *current
				stale.OwnerInstance = "old-run"
				if kind == types.AgentPromptKindAskUser {
					denied := g.ResumeAnswer(ctx, &stale, nil)
					require.False(t, denied.Answered)
					require.NotNil(t, denied.Suspended)
					require.True(t, denied.Suspended.Detached)
					winner := g.ResumeAnswer(ctx, current, nil)
					require.Nil(t, winner.Suspended)
					require.Equal(t, accept, winner.Answered)
					duplicate := g.ResumeAnswer(ctx, current, nil)
					require.NotNil(t, duplicate.Suspended)
					require.True(t, duplicate.Suspended.Detached)
				} else {
					denied := g.ResumeDecision(ctx, &stale, nil)
					require.False(t, denied.Approved)
					require.NotNil(t, denied.Suspended)
					require.True(t, denied.Suspended.Detached)
					winner := g.ResumeDecision(ctx, current, nil)
					require.Nil(t, winner.Suspended)
					require.Equal(t, accept, winner.Approved)
					duplicate := g.ResumeDecision(ctx, current, nil)
					require.NotNil(t, duplicate.Suspended)
					require.True(t, duplicate.Suspended.Detached)
				}
			})
		}
	}
}
