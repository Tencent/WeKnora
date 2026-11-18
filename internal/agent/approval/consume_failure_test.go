package approval

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/agent/hitl"
	"github.com/Tencent/WeKnora/internal/event"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

type consumeFailureStore struct{ hitl.Store }

func (s consumeFailureStore) Transition(ctx context.Context, id string, tr types.AgentPromptTransition) (bool, error) {
	if tr.To == types.AgentPromptExecuting || tr.To == types.AgentPromptDone {
		return false, errors.New("consume unavailable")
	}
	return s.Store.Transition(ctx, id, tr)
}

func TestConsumeFailureDoesNotDetachOrAuthorizeRun(t *testing.T) {
	for _, kind := range []string{types.AgentPromptKindToolApproval, types.AgentPromptKindAskUser} {
		t.Run(kind, func(t *testing.T) {
			ctx := t.Context()
			store := consumeFailureStore{hitl.NewMemoryStore()}
			g := NewGate(nil, nil, hitl.NewBroker(store, nil))
			response := types.JSON(`{"approved":true,"modified_args":{"dangerous":true}}`)
			if kind == types.AgentPromptKindAskUser {
				response = types.JSON(`{"text":"staging"}`)
			}
			prompt := &types.AgentPrompt{
				ID: "p", Kind: kind, Status: types.AgentPromptAnswered, OwnerInstance: "owner", Response: response,
			}
			require.NoError(t, store.Create(ctx, prompt))
			bus := event.NewEventBus()
			var emitted bool
			bus.On(event.EventToolApprovalResolved, func(_ context.Context, e event.Event) error {
				emitted = true
				require.False(t, e.Data.(event.ToolApprovalResolvedData).Approved)
				return nil
			})
			bus.On(event.EventAgentPromptResolved, func(_ context.Context, e event.Event) error {
				emitted = true
				require.False(t, e.Data.(event.AgentPromptResolvedData).Answered)
				return nil
			})
			if kind == types.AgentPromptKindToolApproval {
				decision := g.ResumeDecision(ctx, prompt, bus)
				require.False(t, decision.Approved)
				require.Empty(t, decision.ModifiedArgs)
				require.Nil(t, decision.Suspended,
					"storage failure must complete normally, not detach and strand the message")
				require.Contains(t, decision.Reason, "could not confirm")
			} else {
				answer := g.ResumeAnswer(ctx, prompt, bus)
				require.False(t, answer.Answered)
				require.Empty(t, answer.Response)
				require.Nil(t, answer.Suspended)
				require.Contains(t, answer.Reason, "could not confirm")
			}
			require.True(t, emitted, "the client must learn that the interaction ended")
			row, err := store.Get(ctx, "p")
			require.NoError(t, err)
			require.Equal(t, types.AgentPromptFailed, row.Status)
		})
	}
}

func TestOAuthConsumeFailureReportsFailureWithoutRetryingTool(t *testing.T) {
	ctx := t.Context()
	store := consumeFailureStore{hitl.NewMemoryStore()}
	g := NewGate(nil, nil, hitl.NewBroker(store, nil))
	bus := event.NewEventBus()
	var promptID string
	var emitted bool
	bus.On(event.EventMCPOAuthRequired, func(_ context.Context, e event.Event) error {
		promptID = e.Data.(event.MCPOAuthRequiredData).PendingID
		return g.Resolve(ctx, ResolveRequest{
			TenantID: 1, PendingID: promptID, Kind: types.AgentPromptKindMCPOAuth, Decision: Decision{Approved: true},
		})
	})
	bus.On(event.EventMCPOAuthResolved, func(_ context.Context, e event.Event) error {
		emitted = true
		require.False(t, e.Data.(event.MCPOAuthResolvedData).Authorized)
		return nil
	})
	decision, err := g.RequestOAuthAndWait(ctx, OAuthPendingRequest{
		TenantID: 1, SessionID: "s", ServiceID: "svc", EventBus: bus, WaitTimeout: time.Second,
	})
	require.NoError(t, err)
	require.False(t, decision.Approved)
	require.Contains(t, decision.Reason, "could not confirm")
	require.True(t, emitted)
	row, err := store.Get(ctx, promptID)
	require.NoError(t, err)
	require.Equal(t, types.AgentPromptFailed, row.Status)
}
