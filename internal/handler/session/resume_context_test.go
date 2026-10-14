package session

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/agent"
	"github.com/Tencent/WeKnora/internal/agent/hitl"
	"github.com/Tencent/WeKnora/internal/application/service"
	"github.com/Tencent/WeKnora/internal/stream"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

type resumeMessages struct {
	interfaces.MessageService
	rows map[string]*types.Message
}

func (m *resumeMessages) GetMessage(_ context.Context, sessionID, id string) (*types.Message, error) {
	row := m.rows[id]
	if row == nil || row.SessionID != sessionID {
		return nil, nil
	}
	cloned := *row
	return &cloned, nil
}

func (m *resumeMessages) UpdateMessage(_ context.Context, row *types.Message) error {
	cloned := *row
	m.rows[row.ID] = &cloned
	return nil
}

type resumeMessageRepo struct {
	interfaces.MessageRepository
	messages *resumeMessages
}

type resumeSessionService struct{ interfaces.SessionService }

func (*resumeSessionService) GetSession(_ context.Context, id string) (*types.Session, error) {
	return &types.Session{ID: id, TenantID: 1}, nil
}

func (r *resumeMessageRepo) GetMessage(ctx context.Context, sessionID, id string) (*types.Message, error) {
	return r.messages.GetMessage(ctx, sessionID, id)
}

func (r *resumeMessageRepo) UpdateMessage(ctx context.Context, m *types.Message) error {
	return r.messages.UpdateMessage(ctx, m)
}

func TestBuildResumeRequestKeepsTurnAndRestoresLegacySteering(t *testing.T) {
	c, ctx := newResolveAgentTestContext(1)
	messages := &resumeMessages{rows: map[string]*types.Message{
		"m": {ID: "m", SessionID: "sess-1", RequestID: "original-turn", RunState: types.MessageRunStateAwaitingInput},
		"u": {ID: "u", SessionID: "sess-1", Role: "user", Content: "use staging"},
	}}
	b := hitl.NewBroker(nil, nil)
	request, err := json.Marshal(types.ResumableAgentRequest{AgentID: "a", UserMessageID: "first"})
	require.NoError(t, err)
	snapshot, err := json.Marshal(agent.RunSnapshot{
		Version: agent.RunSnapshotVersion, Query: "original question", Request: request,
		PendingRound: agent.PendingRound{Step: types.AgentStep{UserMessagesBefore: []string{"u"}}},
	})
	require.NoError(t, err)
	require.NoError(t, b.Suspensions().Save(ctx, &types.AgentRunSuspension{
		AssistantMessageID: "m", PromptID: "p", Snapshot: types.JSON(snapshot),
	}))
	h := &Handler{
		promptBroker: b, messageService: messages, streamManager: stream.NewMemoryStreamManager(),
		sessionService:     &resumeSessionService{},
		customAgentService: &resolveOwnAgentStub{agent: &types.CustomAgent{ID: "a"}},
	}
	req, _, err := h.buildResumeRequest(ctx, c, &types.AgentPrompt{
		ID: "p", SessionID: "sess-1", AssistantMessageID: "m", RequestID: "transport",
	})
	require.NoError(t, err)
	require.Equal(t, "original question", req.query)
	require.Equal(t, req.query, req.userInput)
	require.Equal(t, "original-turn", req.requestID)
	require.Equal(t, "original question", req.buildQARequest().Query)
	snap, err := agent.DecodeRunSnapshot(req.resume.Snapshot)
	require.NoError(t, err)
	require.Equal(t, "use staging", snap.UserMessages["u"])
}

func TestResumeRecoversOwnerThatDiedBeforeSavingMessage(t *testing.T) {
	for _, ownerGone := range []bool{true, false} {
		t.Run(map[bool]string{true: "reaped", false: "self-suspended"}[ownerGone], func(t *testing.T) {
			shortResumeSettle(t, 20*time.Millisecond)
			ctx := t.Context()
			messages := &resumeMessages{rows: map[string]*types.Message{"m": {ID: "m", SessionID: "s"}}}
			b := hitl.NewBroker(nil, nil)
			require.NoError(t, b.Suspensions().Save(ctx, &types.AgentRunSuspension{
				AssistantMessageID: "m", PromptID: "p1",
				Snapshot: types.JSON(`{"version":1,"completed_steps":[{"thought":"saved work"}]}`),
			}))
			mgr := stream.NewMemoryStreamManager()
			require.NoError(t, mgr.SetLiveRun(ctx, "s", "m", "old"))
			h := &Handler{
				messageService: messages, promptBroker: b, streamManager: mgr,
				suspendedRuns: service.NewSuspendedRunService(b, &resumeMessageRepo{messages: messages}),
			}
			past := time.Now().Add(-time.Second)
			require.NoError(t, h.awaitSuspendedRunSettled(ctx, settlePrompt, ownerGone, &past))
			require.Equal(t, types.MessageRunStateAwaitingInput, messages.rows["m"].RunState)
			require.Equal(t, "saved work", messages.rows["m"].AgentSteps[0].Thought)
			live, _, err := mgr.GetLiveRun(ctx, "s")
			require.NoError(t, err)
			require.Empty(t, live)
		})
	}
}
