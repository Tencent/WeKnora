package session

import (
	"context"
	"encoding/json"
	"path/filepath"
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

type resumeKBService struct {
	interfaces.KnowledgeBaseService
	*wikiFixerKBLookupStub
}

func (s *resumeKBService) GetKnowledgeBaseByIDOnly(ctx context.Context, id string) (*types.KnowledgeBase, error) {
	return s.wikiFixerKBLookupStub.GetKnowledgeBaseByIDOnly(ctx, id)
}

type resumeKBShareService struct {
	interfaces.KBShareService
	*wikiFixerKBShareStub
}

func (s *resumeKBShareService) CheckTenantKBPermission(
	ctx context.Context, kbID string, tenantID uint64, role types.TenantRole,
) (types.OrgMemberRole, bool, error) {
	return s.wikiFixerKBShareStub.CheckTenantKBPermission(ctx, kbID, tenantID, role)
}

func TestBuildResumeRequestRevalidatesSharedKBBuiltin(t *testing.T) {
	require.NoError(t, types.LoadBuiltinAgentsConfig(filepath.Join("..", "..", "..", "config")))
	for _, agentID := range []string{types.BuiltinWeKnoraAssistantID, types.BuiltinWikiFixerID} {
		for _, allowed := range []bool{true, false} {
			name := agentID + "/editor"
			if !allowed {
				name = agentID + "/revoked"
			}
			t.Run(name, func(t *testing.T) {
				c, ctx := newResolveAgentTestContext(1)
				ctx = context.WithValue(ctx, types.TenantRoleContextKey, types.TenantRoleContributor)
				messages := &resumeMessages{rows: map[string]*types.Message{
					"m": {ID: "m", SessionID: "s", RunState: types.MessageRunStateAwaitingInput},
				}}
				b := hitl.NewBroker(nil, nil)
				request, err := json.Marshal(types.ResumableAgentRequest{
					AgentID: agentID, AgentTenantID: 2, KnowledgeBaseIDs: []string{"shared-kb"},
					PageContext: &types.PageContext{KnowledgeBaseID: "shared-kb"},
				})
				require.NoError(t, err)
				snapshot, err := json.Marshal(agent.RunSnapshot{Version: agent.RunSnapshotVersion, Request: request})
				require.NoError(t, err)
				require.NoError(t, b.Suspensions().Save(ctx, &types.AgentRunSuspension{
					AssistantMessageID: "m", PromptID: "p", Snapshot: types.JSON(snapshot),
				}))
				permission := types.OrgRoleViewer
				if allowed {
					permission = types.OrgRoleEditor
				}
				share := &wikiFixerKBShareStub{permission: permission, isShared: true}
				own := types.GetBuiltinAgent(agentID, 1)
				h := &Handler{
					promptBroker: b, messageService: messages, sessionService: &resumeSessionService{},
					streamManager:      stream.NewMemoryStreamManager(),
					customAgentService: &resolveOwnAgentStub{agent: own},
					agentShareService:  &resolveAgentShareStub{err: service.ErrAgentShareNotFound},
					knowledgebaseService: &resumeKBService{wikiFixerKBLookupStub: &wikiFixerKBLookupStub{
						kb: &types.KnowledgeBase{ID: "shared-kb", TenantID: 2},
					}},
					kbShareService: &resumeKBShareService{wikiFixerKBShareStub: share},
				}
				req, _, err := h.buildResumeRequest(ctx, c, &types.AgentPrompt{
					ID: "p", SessionID: "s", AssistantMessageID: "m", Kind: types.AgentPromptKindToolApproval,
				})
				if !allowed {
					require.Error(t, err)
					require.Nil(t, req)
					require.Equal(t, types.MessageRunStateAwaitingInput, messages.rows["m"].RunState)
				} else {
					require.NoError(t, err)
					require.Equal(t, uint64(2), req.effectiveTenantID)
					require.Equal(t, uint64(2), req.customAgent.TenantID)
					require.Equal(t, []string{"shared-kb"}, req.customAgent.Config.KnowledgeBases)
					require.False(t, req.sharedAgentReadOnly)
					require.Equal(t, "shared-kb", req.buildQARequest().PageContext.KnowledgeBaseID)
				}
				require.Equal(t, "shared-kb", share.checkedKBID)
				require.Equal(t, uint64(1), share.checkedTenantID)
				require.Equal(t, uint64(1), own.TenantID)
			})
		}
	}
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
