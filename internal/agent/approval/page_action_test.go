package approval

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/agent/hitl"
	"github.com/Tencent/WeKnora/internal/event"
	"github.com/Tencent/WeKnora/internal/types"
)

func pageActionRequest(bus *event.EventBus) PageActionRequest {
	return PageActionRequest{
		TenantID: 1, UserID: "user:u1", SessionID: "s1", AssistantMessageID: "m1", ToolCallID: "tc1",
		EventBus: bus, Action: types.PageActionPromptRequest{Action: types.PageActionRead},
	}
}

func answerPageAction(g *Gate, id, response string) error {
	_, err := g.ResolveResponse(context.Background(), hitl.ResolveRequest{
		TenantID: 1, UserID: "user:u1", SessionID: "s1", Kind: types.AgentPromptKindPageAction,
		PromptID: id, Response: json.RawMessage(response),
	})
	return err
}

func TestGate_PageActionAnswered(t *testing.T) {
	bus := event.NewEventBus()
	g := NewGate(nil, nil, nil)
	invalid := make(chan error, 1)
	bus.On(event.EventAgentPromptRequired, func(_ context.Context, evt event.Event) error {
		data := evt.Data.(event.AgentPromptRequiredData)
		assert.Equal(t, types.AgentPromptKindPageAction, data.Kind)
		assert.Equal(t, "tc1", data.ToolCallID)
		assert.False(t, data.Suspendable)
		assert.JSONEq(t, `{"action":"read"}`, string(data.Request))
		go func() {
			invalid <- answerPageAction(g, data.PendingID, `{"applied":[{"ref":"e1"}]}`)
			_ = answerPageAction(g, data.PendingID, `{"location":"Docs","snapshot":"heading \"Docs\""}`)
		}()
		return nil
	})

	res, err := g.RequestPageAction(context.Background(), pageActionRequest(bus))
	require.NoError(t, err)
	require.ErrorIs(t, <-invalid, ErrInvalidDecision, "a read answered with a fill result is rejected")
	require.True(t, res.Answered)
	assert.Equal(t, "Docs", res.Response.Location)
	assert.Equal(t, `heading "Docs"`, res.Response.Snapshot)
}

func TestGate_PageActionTimesOut(t *testing.T) {
	g := NewGate(nil, nil, nil)
	req := pageActionRequest(event.NewEventBus())
	req.Timeout = 50 * time.Millisecond
	res, err := g.RequestPageAction(context.Background(), req)
	require.NoError(t, err)
	assert.False(t, res.Answered)
	assert.True(t, res.TimedOut)
	assert.Equal(t, "the page did not respond", res.Reason)
}

func TestGate_PageActionRefusesAnotherUser(t *testing.T) {
	bus := event.NewEventBus()
	g := NewGate(nil, nil, nil)
	bus.On(event.EventAgentPromptRequired, func(_ context.Context, evt event.Event) error {
		id := evt.Data.(event.AgentPromptRequiredData).PendingID
		go func() {
			_, err := g.ResolveResponse(context.Background(), hitl.ResolveRequest{
				TenantID: 1, UserID: "user:intruder", SessionID: "s1", Kind: types.AgentPromptKindPageAction,
				PromptID: id, Response: json.RawMessage(`{"snapshot":"forged"}`),
			})
			assert.ErrorIs(t, err, ErrUserMismatch)
			_ = answerPageAction(g, id, `{"declined":true}`)
		}()
		return nil
	})
	res, err := g.RequestPageAction(context.Background(), pageActionRequest(bus))
	require.NoError(t, err)
	require.True(t, res.Answered)
	assert.True(t, res.Response.Declined)
	assert.Empty(t, res.Response.Snapshot)
}
