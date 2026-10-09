package approval

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/event"
	"github.com/stretchr/testify/require"
)

func hostRequest(bus *event.EventBus) HostPendingRequest {
	return HostPendingRequest{
		TenantID: 1, UserID: "u1", SessionID: "s1", AssistantMessageID: "m1",
		ToolCallID: "tc1", EventBus: bus,
		Host: event.HostApprovalPayload{Reason: "delete", Command: "rm a", Cwd: "/w", AllowSession: true},
	}
}

// A nil MCP checker turns RequestAndWait into an automatic yes. The host
// entry point must still block for the user.
func TestRequestHostAndWaitBlocksWithoutChecker(t *testing.T) {
	bus := event.NewEventBus()
	g := NewGate(&config.Config{Agent: &config.AgentConfig{ToolApprovalTimeoutSeconds: 2}}, nil, nil)
	var seen event.ToolApprovalRequiredData
	bus.On(event.EventToolApprovalRequired, func(_ context.Context, evt event.Event) error {
		seen = evt.Data.(event.ToolApprovalRequiredData)
		go func() {
			_ = g.Resolve(1, "u1", seen.PendingID, Decision{Approved: true, Scope: "session", Access: "read"})
		}()
		return nil
	})
	var resolved event.ToolApprovalResolvedData
	bus.On(event.EventToolApprovalResolved, func(_ context.Context, evt event.Event) error {
		resolved = evt.Data.(event.ToolApprovalResolvedData)
		return nil
	})

	d, err := g.RequestHostAndWait(context.Background(), hostRequest(bus))
	require.NoError(t, err)
	require.True(t, d.Approved)
	require.Equal(t, "session", d.Scope)
	require.Equal(t, "read", d.Access)
	require.Equal(t, HostApprovalKindCommand, seen.Kind)
	require.NotNil(t, seen.Host)
	require.Equal(t, "rm a", seen.Host.Command)
	require.Equal(t, "session", resolved.Scope)
}

func TestRequestHostAndWaitTimesOut(t *testing.T) {
	g := NewGate(&config.Config{Agent: &config.AgentConfig{ToolApprovalTimeoutSeconds: 1}}, nil, nil)
	d, err := g.RequestHostAndWait(context.Background(), hostRequest(event.NewEventBus()))
	require.NoError(t, err)
	require.False(t, d.Approved)
	require.True(t, d.TimedOut)
}

func TestRequestHostAndWaitNeedsOwner(t *testing.T) {
	g := NewGate(nil, nil, nil)
	req := hostRequest(event.NewEventBus())
	req.UserID = ""
	_, err := g.RequestHostAndWait(context.Background(), req)
	require.Error(t, err)
}

func TestRequestHostAndWaitRejectsOtherUser(t *testing.T) {
	bus := event.NewEventBus()
	g := NewGate(&config.Config{Agent: &config.AgentConfig{ToolApprovalTimeoutSeconds: 2}}, nil, nil)
	bus.On(event.EventToolApprovalRequired, func(_ context.Context, evt event.Event) error {
		id := evt.Data.(event.ToolApprovalRequiredData).PendingID
		go func() {
			require.ErrorIs(t, g.Resolve(1, "mallory", id, Decision{Approved: true}), ErrUserMismatch)
			_ = g.Resolve(1, "u1", id, Decision{Approved: false, Reason: "no"})
		}()
		return nil
	})
	d, err := g.RequestHostAndWait(context.Background(), hostRequest(bus))
	require.NoError(t, err)
	require.False(t, d.Approved)
}

// MCP events must serialize exactly as before when Kind and Host are unset.
func TestMCPApprovalEventOmitsHostFields(t *testing.T) {
	raw, err := json.Marshal(event.ToolApprovalRequiredData{PendingID: "p"})
	require.NoError(t, err)
	require.NotContains(t, string(raw), `"kind"`)
	require.NotContains(t, string(raw), `"host"`)
	raw, err = json.Marshal(event.ToolApprovalResolvedData{PendingID: "p"})
	require.NoError(t, err)
	require.NotContains(t, string(raw), `"scope"`)
}
