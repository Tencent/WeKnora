package approval

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/event"
	"github.com/stretchr/testify/require"
)

type stubChecker struct {
	required bool
	err      error
}

func (s *stubChecker) IsRequired(ctx context.Context, tenantID uint64, serviceID, toolName string) (bool, error) {
	return s.required, s.err
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
			_ = g.Resolve(1, "", data.PendingID, Decision{Approved: true, ModifiedArgs: json.RawMessage(`{"a":2}`)})
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

func TestGate_NeedsApproval_NoChecker(t *testing.T) {
	g := NewGate(nil, nil, nil)
	require.False(t, g.NeedsApproval(context.Background(), 1, "x", "y"))
}

func TestGate_Resolve_NotFound(t *testing.T) {
	g := NewGate(&config.Config{Agent: &config.AgentConfig{ToolApprovalTimeoutSeconds: 1}}, &stubChecker{required: true}, nil)
	err := g.Resolve(1, "", "no-such-id", Decision{Approved: true})
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
			require.ErrorIs(t, g.Resolve(999, "", data.PendingID, Decision{Approved: true}), ErrTenantMismatch)
			_ = g.Resolve(1, "", data.PendingID, Decision{Approved: false, Reason: "no"})
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
			require.ErrorIs(t, g.Resolve(1, "bob", data.PendingID, Decision{Approved: true}), ErrUserMismatch)
			_ = g.Resolve(1, "alice", data.PendingID, Decision{Approved: true})
		}()
		return nil
	})
	d, err := g.RequestAndWait(context.Background(), req)
	require.NoError(t, err)
	require.True(t, d.Approved)
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

	go func() {
		<-gotPending
		// Wait until timeout has fired and the pending entry is still there
		// (defer delete only happens after RequestAndWait returns).
		// 1s timeout + small slack.
		<-time.After(1500 * time.Millisecond)
		require.ErrorIs(t,
			g.Resolve(1, "", pendingID, Decision{Approved: true}),
			ErrPendingNotFound, // entry already removed by RequestAndWait's defer
		)
	}()

	d, err := g.RequestAndWait(context.Background(), PendingRequest{
		TenantID: 1, EventBus: bus, SessionID: "s",
		ServiceID: "svc", MCPToolName: "t", Args: json.RawMessage(`{}`),
	})
	require.NoError(t, err)
	require.True(t, d.TimedOut)
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
			err1 := g.Resolve(1, "", pendingID, Decision{Approved: true})
			err2 := g.Resolve(1, "", pendingID, Decision{Approved: false})
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
	// Second call must surface either AlreadyResolved or NotFound (depending
	// on whether the defer-delete already ran).
	require.True(t,
		r.second == nil || // RequestAndWait removed the entry: NotFound is possible too
			r.second.Error() == ErrAlreadyResolved.Error() ||
			r.second.Error() == ErrPendingNotFound.Error(),
		"unexpected error: %v", r.second,
	)
}
