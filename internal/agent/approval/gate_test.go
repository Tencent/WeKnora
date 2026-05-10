package approval

import (
	"context"
	"encoding/json"
	"testing"

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
			_ = g.Resolve(1, data.PendingID, Decision{Approved: true, ModifiedArgs: json.RawMessage(`{"a":2}`)})
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
