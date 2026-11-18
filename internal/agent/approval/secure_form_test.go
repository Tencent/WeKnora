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

func secureFormRequest(bus *event.EventBus) SecureFormRequest {
	return SecureFormRequest{
		TenantID: 1, UserID: "web_user:u1", SessionID: "s1", AssistantMessageID: "m1", ToolCallID: "tc1",
		EventBus: bus,
		Form: types.SecureFormPromptRequest{
			Title: "登录", Kind: "login", Site: "a.com",
			Domains: []string{"a.com"}, Parts: []string{"username", "password"},
		},
	}
}

func resolveSecureForm(g *Gate, id, response string) (hitl.Resolution, error) {
	return g.ResolveSecureForm(context.Background(), hitl.ResolveRequest{
		TenantID: 1, UserID: "web_user:u1", SessionID: "s1", PromptID: id, Response: json.RawMessage(response),
	})
}

func TestGate_SecureFormAnsweredLive(t *testing.T) {
	bus := event.NewEventBus()
	g := NewGate(&config.Config{Agent: &config.AgentConfig{ToolApprovalTimeoutSeconds: 5}}, nil, nil)
	rejected := make(chan error, 2)
	bus.On(event.EventAgentPromptRequired, func(_ context.Context, evt event.Event) error {
		data := evt.Data.(event.AgentPromptRequiredData)
		assert.Equal(t, types.AgentPromptKindSecureForm, data.Kind)
		go func() {
			// The generic endpoint path refuses this kind outright.
			_, err := g.ResolveResponse(context.Background(), hitl.ResolveRequest{
				TenantID: 1, UserID: "web_user:u1", Kind: types.AgentPromptKindSecureForm,
				PromptID: data.PendingID, Response: json.RawMessage(`{"handle":"cred_1","fields":["password"]}`),
			})
			rejected <- err
			// A raw answer with values never reaches the row.
			rawValues := `{"handle":"cred_1","fields":["password"],"values":{"password":"hunter2"}}`
			_, err = resolveSecureForm(g, data.PendingID, rawValues)
			rejected <- err
			okBody := `{"handle":"cred_1","fields":["username","password"],"username":"alice"}`
			_, _ = resolveSecureForm(g, data.PendingID, okBody)
		}()
		return nil
	})
	var resolved event.AgentPromptResolvedData
	bus.On(event.EventAgentPromptResolved, func(_ context.Context, evt event.Event) error {
		resolved = evt.Data.(event.AgentPromptResolvedData)
		return nil
	})

	a, err := g.RequestSecureForm(context.Background(), secureFormRequest(bus))
	require.NoError(t, err)
	require.ErrorIs(t, <-rejected, ErrInvalidDecision)
	require.ErrorIs(t, <-rejected, ErrInvalidDecision)
	require.True(t, a.Answered)
	assert.Equal(t, "cred_1", a.Response.Handle)
	assert.Equal(t, "alice", a.Response.Username)
	assert.True(t, resolved.Answered)
	assert.NotContains(t, string(resolved.Response), "hunter2")

	row, err := g.SecureFormPrompt(context.Background(), a.PromptID)
	require.NoError(t, err)
	assert.Equal(t, types.AgentPromptDone, row.Status)
	assert.NotContains(t, string(row.Response), "hunter2")
}

func TestGate_SecureFormDeclined(t *testing.T) {
	bus := event.NewEventBus()
	g := NewGate(&config.Config{Agent: &config.AgentConfig{ToolApprovalTimeoutSeconds: 5}}, nil, nil)
	bus.On(event.EventAgentPromptRequired, func(_ context.Context, evt event.Event) error {
		id := evt.Data.(event.AgentPromptRequiredData).PendingID
		go func() { _, _ = resolveSecureForm(g, id, `{"declined":true}`) }()
		return nil
	})
	a, err := g.RequestSecureForm(context.Background(), secureFormRequest(bus))
	require.NoError(t, err)
	assert.False(t, a.Answered)
	assert.Contains(t, a.Reason, "declined")
}

func TestGate_SecureFormNotAnswerableGenerically(t *testing.T) {
	assert.False(t, AnswerableKinds[types.AgentPromptKindSecureForm])
}
