package handler

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

type sessionPromptRepo struct {
	interfaces.AgentPromptRepository
	rows  []*types.AgentPrompt
	calls int
}

func (r *sessionPromptRepo) ListBySession(
	_ context.Context, sessionID string, statuses []string,
) ([]*types.AgentPrompt, error) {
	r.calls++
	var out []*types.AgentPrompt
	for _, p := range r.rows {
		for _, s := range statuses {
			if p.SessionID == sessionID && p.Status == s {
				out = append(out, p)
			}
		}
	}
	return out, nil
}

func TestAttachPendingPrompts(t *testing.T) {
	expires := time.Now().Add(time.Hour)
	caller := types.Principal{Type: "user", ID: "u1"}
	repo := &sessionPromptRepo{rows: []*types.AgentPrompt{
		{
			ID: "p1", SessionID: "s1", AssistantMessageID: "m1", UserID: caller.StorageID(),
			Kind: types.AgentPromptKindToolApproval, ToolCallID: "call-1", Status: types.AgentPromptSuspended,
			Request: types.JSON(`{"service_name":"svc"}`), ExpiresAt: &expires,
		},
		{
			ID: "p2", SessionID: "s1", AssistantMessageID: "m2", UserID: "user:someone-else",
			Kind: types.AgentPromptKindToolApproval, Status: types.AgentPromptSuspended,
		},
	}}
	h := &MessageHandler{Prompts: repo}
	ctx := types.WithPrincipal(context.Background(), caller)

	awaiting := func(id string) *types.Message {
		return &types.Message{ID: id, RunState: types.MessageRunStateAwaitingInput}
	}
	m1, m2, done := awaiting("m1"), awaiting("m2"), &types.Message{ID: "m3"}
	h.attachPendingPrompts(ctx, "s1", []*types.Message{m1, m2, done})

	require.NotNil(t, m1.PendingPrompt)
	assert.Equal(t, "p1", m1.PendingPrompt.ID)
	assert.Equal(t, "call-1", m1.PendingPrompt.ToolCallID)
	assert.JSONEq(t, `{"service_name":"svc"}`, string(m1.PendingPrompt.Request))
	assert.Equal(t, &expires, m1.PendingPrompt.ExpiresAt)
	assert.Nil(t, m2.PendingPrompt, "another user's prompt stays hidden")
	assert.Nil(t, done.PendingPrompt)

	repo.calls = 0
	h.attachPendingPrompts(ctx, "s1", []*types.Message{{ID: "m3"}})
	assert.Zero(t, repo.calls, "no lookup without an awaiting message")
}
