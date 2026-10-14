package approval

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/agent/hitl"
	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/event"
	"github.com/Tencent/WeKnora/internal/types"
)

func askRequest(bus *event.EventBus) AskRequest {
	return AskRequest{
		TenantID: 1, UserID: "user:u1", SessionID: "s1", AssistantMessageID: "m1", ToolCallID: "tc1",
		EventBus: bus,
		Question: types.AskUserPromptRequest{Question: "which env?", Options: []string{"dev", "prod"}},
	}
}

func answerPrompt(g *Gate, id, response string) (hitl.Resolution, error) {
	return g.ResolveResponse(context.Background(), hitl.ResolveRequest{
		TenantID: 1, UserID: "user:u1", SessionID: "s1", Kind: types.AgentPromptKindAskUser,
		PromptID: id, Response: json.RawMessage(response),
	})
}

func TestGate_AskUserAnsweredLive(t *testing.T) {
	bus := event.NewEventBus()
	g := NewGate(&config.Config{Agent: &config.AgentConfig{ToolApprovalTimeoutSeconds: 5}}, nil, nil)
	invalid := make(chan error, 1)
	bus.On(event.EventAgentPromptRequired, func(_ context.Context, evt event.Event) error {
		data := evt.Data.(event.AgentPromptRequiredData)
		assert.Equal(t, types.AgentPromptKindAskUser, data.Kind)
		assert.JSONEq(t, `{"question":"which env?","options":["dev","prod"]}`, string(data.Request))
		assert.False(t, data.Suspendable)
		go func() {
			_, err := answerPrompt(g, data.PendingID, `{"selected":["staging"]}`)
			invalid <- err
			_, _ = answerPrompt(g, data.PendingID, `{"selected":["prod"]}`)
		}()
		return nil
	})
	var resolved event.AgentPromptResolvedData
	bus.On(event.EventAgentPromptResolved, func(_ context.Context, evt event.Event) error {
		resolved = evt.Data.(event.AgentPromptResolvedData)
		return nil
	})

	a, err := g.AskUser(context.Background(), askRequest(bus))
	require.NoError(t, err)
	require.ErrorIs(t, <-invalid, ErrInvalidDecision, "an answer outside the options is rejected")
	require.True(t, a.Answered)
	assert.Equal(t, []string{"prod"}, a.Response.Selected)
	assert.True(t, resolved.Answered)
	assert.JSONEq(t, `{"selected":["prod"]}`, string(resolved.Response))

	row, err := g.broker.Store().Get(context.Background(), a.PromptID)
	require.NoError(t, err)
	assert.Equal(t, types.AgentPromptDone, row.Status)
}

func TestGate_AskUserDeclined(t *testing.T) {
	bus := event.NewEventBus()
	g := NewGate(&config.Config{Agent: &config.AgentConfig{ToolApprovalTimeoutSeconds: 5}}, nil, nil)
	bus.On(event.EventAgentPromptRequired, func(_ context.Context, evt event.Event) error {
		id := evt.Data.(event.AgentPromptRequiredData).PendingID
		go func() { _, _ = answerPrompt(g, id, `{"declined":true}`) }()
		return nil
	})
	a, err := g.AskUser(context.Background(), askRequest(bus))
	require.NoError(t, err)
	assert.False(t, a.Answered)
	assert.Contains(t, a.Reason, "declined")
}

func TestGate_AskUserSuspendsThenResumes(t *testing.T) {
	g := NewGate(&config.Config{Agent: &config.AgentConfig{
		ToolApprovalTimeoutSeconds: 60, PromptHotWaitSeconds: 1,
	}}, nil, nil)
	req := askRequest(event.NewEventBus())
	req.Snapshot, req.SnapshotVersion = []byte(`{"version":1}`), 1
	a, err := g.AskUser(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, a.Suspended)
	assert.Equal(t, types.AgentPromptKindAskUser, a.Suspended.Kind)

	_, err = g.ResolveResponse(context.Background(), hitl.ResolveRequest{
		TenantID: 1, UserID: "user:someone-else", SessionID: "s1", Kind: types.AgentPromptKindAskUser,
		PromptID: a.PromptID, Response: json.RawMessage(`{"selected":["dev"]}`),
	})
	require.ErrorIs(t, err, ErrUserMismatch)

	res, err := answerPrompt(g, a.PromptID, `{"selected":["dev"]}`)
	require.NoError(t, err)
	require.True(t, res.Resume)

	got := g.ResumeAnswer(context.Background(), res.Prompt, event.NewEventBus())
	require.True(t, got.Answered)
	assert.Equal(t, []string{"dev"}, got.Response.Selected)
}

func TestGate_ResolveResponseRefusesOAuth(t *testing.T) {
	g := NewGate(nil, nil, nil)
	_, err := g.ResolveResponse(context.Background(), hitl.ResolveRequest{
		TenantID: 1, UserID: "user:u1", Kind: types.AgentPromptKindMCPOAuth,
		PromptID: "p1", Response: json.RawMessage(`{"authorized":true}`),
	})
	require.ErrorIs(t, err, ErrInvalidDecision)
}
