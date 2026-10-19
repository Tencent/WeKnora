package service

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/agent"
	"github.com/Tencent/WeKnora/internal/agent/collaboration"
	"github.com/Tencent/WeKnora/internal/agent/tools"
	"github.com/Tencent/WeKnora/internal/event"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type collaborationAgents struct {
	interfaces.CustomAgentService
	agent *types.CustomAgent
}

func (f *collaborationAgents) GetAgentByID(context.Context, string) (*types.CustomAgent, error) {
	return f.agent, nil
}

type collaborationUsers struct {
	interfaces.UserService
	active bool
}

func (f *collaborationUsers) GetUserByID(context.Context, string) (*types.User, error) {
	return &types.User{IsActive: f.active}, nil
}

type collaborationMembers struct {
	interfaces.TenantMemberService
	active bool
}

func (f *collaborationMembers) GetMembership(context.Context, string, uint64) (*types.TenantMember, error) {
	if !f.active {
		return nil, nil
	}
	return &types.TenantMember{Status: types.TenantMemberStatusActive, Role: types.TenantRoleContributor}, nil
}

type collaborationTenants struct {
	interfaces.TenantService
	level types.AssistantAccessLevel
}

func (f *collaborationTenants) GetTenantByID(context.Context, uint64) (*types.Tenant, error) {
	return &types.Tenant{ID: 7, AssistantConfig: &types.AssistantConfig{MaxAccessLevel: f.level}}, nil
}

type collaborationMessages struct {
	interfaces.MessageService
	mu    sync.Mutex
	saved []*types.Message
}

func (f *collaborationMessages) CreateMessage(_ context.Context, m *types.Message) (*types.Message, error) {
	m.ID = uuid.NewString()
	return m, nil
}

func (f *collaborationMessages) UpdateMessage(_ context.Context, m *types.Message) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	snapshot := *m
	f.saved = append(f.saved, &snapshot)
	return nil
}

type collaborationSessions struct {
	interfaces.SessionService
	run func(context.Context, *types.QARequest, *event.EventBus) error
}

func (f *collaborationSessions) GetSession(_ context.Context, id string) (*types.Session, error) {
	return &types.Session{ID: id, TenantID: 7, UserID: "u"}, nil
}

func (f *collaborationSessions) AgentQA(ctx context.Context, req *types.QARequest, bus *event.EventBus) error {
	return f.run(ctx, req, bus)
}

func collaborationService(
	t *testing.T,
) (*AgentCollaboration, *collaborationAgents, *collaborationMessages, context.Context) {
	t.Helper()
	db, err := gorm.Open(
		sqlite.Open(filepath.Join(t.TempDir(), "collaboration.db")+"?_busy_timeout=5000&_journal_mode=WAL"),
		&gorm.Config{Logger: logger.Default.LogMode(logger.Silent)},
	)
	require.NoError(t, err)
	raw, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = raw.Close() })
	require.NoError(t, db.AutoMigrate(&collaboration.Record{}, &types.Session{}))
	agents := &collaborationAgents{
		agent: &types.CustomAgent{
			ID:       "a",
			TenantID: 7,
			Config:   types.CustomAgentConfig{AgentMode: types.AgentModeSmartReasoning},
		},
	}
	messages := &collaborationMessages{}
	s := NewAgentCollaboration(
		db,
		agents,
		messages,
		&collaborationUsers{active: true},
		&collaborationMembers{active: true},
		&collaborationTenants{},
	)
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(7))
	ctx = context.WithValue(ctx, types.UserIDContextKey, "u")
	ctx = types.WithCaller(ctx, types.Caller{TenantID: 7, UserID: "u", Role: types.TenantRoleContributor})
	ctx = types.WithPrincipal(ctx, types.Principal{Type: types.PrincipalWebUser, ID: "u"})
	return s, agents, messages, ctx
}

func TestCollaborationRegistrationOnlyOffersRootSixAndChildMessage(t *testing.T) {
	s, _, _, ctx := collaborationService(t)
	for _, tc := range []struct {
		name string
		cfg  *types.AgentConfig
		want int
	}{
		{"assistant", &types.AgentConfig{OperationsAgentID: types.BuiltinWeKnoraAssistantID}, 6},
		{"ordinary", &types.AgentConfig{}, 6},
		{
			"shared",
			&types.AgentConfig{
				OperationsAgentID:   types.BuiltinWeKnoraAssistantID,
				SharedAgentReadOnly: true,
			},
			0,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			registry := tools.NewToolRegistry()
			require.NoError(t, s.attach(ctx, &agent.AgentEngine{}, registry, tc.cfg, tc.name+"-session", tc.name))
			require.Len(t, registry.ListTools(), tc.want)
		})
	}
	registry := tools.NewToolRegistry()
	child := &collaboration.Binding{
		Runtime: s.Runtime,
		Child:   &collaboration.Work{Member: collaboration.Member{ConfigJSON: `{}`}},
	}
	require.NoError(
		t,
		s.attach(
			collaboration.WithBinding(ctx, child),
			&agent.AgentEngine{},
			registry,
			&types.AgentConfig{},
			"child",
			"child-message",
		),
	)
	require.Equal(t, []string{"send_message"}, registry.ListTools())
}

func TestCollaborationRejectsForeignInternalAndQuickAnswerAgents(t *testing.T) {
	s, agents, _, ctx := collaborationService(t)
	_, err := s.resolve(ctx, "a", types.AssistantAccessReadOnly)
	require.NoError(t, err)
	agents.agent.TenantID = 8
	_, err = s.resolve(ctx, "a", types.AssistantAccessReadOnly)
	require.Error(t, err)
	agents.agent.TenantID = 7
	agents.agent.Config.AgentMode = types.AgentModeQuickAnswer
	_, err = s.resolve(ctx, "a", types.AssistantAccessReadOnly)
	require.Error(t, err)
	agents.agent.Config.AgentMode = types.AgentModeSmartReasoning
	_, err = s.resolve(ctx, types.BuiltinWikiFixerID, types.AssistantAccessReadOnly)
	require.Error(t, err)
	_, err = s.resolve(ctx, types.BuiltinSkillInstallerID, types.AssistantAccessReadOnly)
	require.Error(t, err)
}

func TestCollaborationWorkerRevalidatesAuthority(t *testing.T) {
	s, _, _, ctx := collaborationService(t)
	scope := collaboration.Scope{TenantID: 7, UserID: "u", SessionID: "s", ID: "m"}
	s.users = &collaborationUsers{active: false}
	_, err := s.workerContext(ctx, scope)
	require.ErrorContains(t, err, "inactive")
	s.users = &collaborationUsers{active: true}
	s.members = &collaborationMembers{active: false}
	_, err = s.workerContext(ctx, scope)
	require.ErrorContains(t, err, "membership")
}

func TestCollaborationResultPublishedAfterTranscriptAndCapturesEventErrors(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "error-event"}[fail], func(t *testing.T) {
			s, _, messages, ctx := collaborationService(t)
			scope := collaboration.Scope{TenantID: 7, UserID: "u", SessionID: "s", ID: "m"}
			require.NoError(t, s.Runtime.Store.Ensure(ctx, scope))
			s.Bind(
				&collaborationSessions{run: func(ctx context.Context, req *types.QARequest, bus *event.EventBus) error {
					if !req.Interactive || !req.Suspendable {
						return errors.New("expected suspendable child with forwarded interaction")
					}
					if req.Session.ID == scope.SessionID {
						return errors.New("child reused parent conversation")
					}
					if types.CallerFromContext(ctx).UserID != "u" {
						return errors.New("caller missing")
					}
					if fail {
						_ = bus.Emit(
							ctx,
							event.Event{Type: event.EventError, Data: event.ErrorData{Error: "model unavailable"}},
						)
						return nil
					}
					return bus.Emit(
						ctx,
						event.Event{
							Type: event.EventAgentComplete,
							Data: event.AgentCompleteData{
								FinalAnswer: "verified answer",
								MessageID:   req.AssistantMessageID,
								AgentSteps: []types.AgentStep{
									{
										ToolCalls: []types.ToolCall{
											{Name: "search_knowledge", Result: &types.ToolResult{Success: true}},
										},
									},
								},
								KnowledgeRefs: []interface{}{
									&types.SearchResult{KnowledgeTitle: "Source", KnowledgeID: "doc"},
								},
							},
						},
					)
				}},
			)
			snapshot, err := s.resolve(ctx, "a", types.AssistantAccessReadOnly)
			require.NoError(t, err)
			_, err = s.Runtime.Spawn(ctx, scope, "spawn", "one", "a", snapshot, "q")
			require.NoError(t, err)
			s.Runtime.Poll = 5 * time.Millisecond
			s.Runtime.Start(context.Background())
			t.Cleanup(func() {
				stop, cancel := context.WithTimeout(context.Background(), time.Second)
				defer cancel()
				require.NoError(t, s.Runtime.Stop(stop))
			})
			require.Eventually(t, func() bool {
				g, err := s.Runtime.Store.Read(ctx, scope)
				if err != nil || len(g.Events) == 0 {
					return false
				}
				messages.mu.Lock()
				defer messages.mu.Unlock()
				return len(messages.saved) == 1
			}, 3*time.Second, 5*time.Millisecond)
			g, err := s.Runtime.Store.Read(ctx, scope)
			require.NoError(t, err)
			run := g.Members[0].Runs[0]
			messages.mu.Lock()
			defer messages.mu.Unlock()
			require.True(t, messages.saved[0].IsCompleted)
			require.Equal(t, run.Result.MessageID, messages.saved[0].ID)
			if fail {
				require.Equal(t, "failed", run.State)
				require.Contains(t, run.Result.Error, "model unavailable")
			} else {
				require.Equal(t, "succeeded", run.State)
				require.Contains(t, run.Result.Evidence, "Source")
				require.Contains(t, run.Result.Evidence, "search_knowledge")
				require.Equal(t, "verified answer", messages.saved[0].Content)
			}
		})
	}
}

func TestCollaborationPinnedAccessDoesNotAllowImplicitWrite(t *testing.T) {
	s, _, _, ctx := collaborationService(t)
	scope := collaboration.Scope{TenantID: 7, UserID: "u", SessionID: "s", ID: "m"}
	require.NoError(t, s.Runtime.Store.Ensure(ctx, scope))
	snapshot, err := s.resolve(ctx, "a", types.AssistantAccessConfirm)
	require.NoError(t, err)
	_, err = s.Runtime.Spawn(ctx, scope, "spawn", "one", "a", snapshot, "q")
	require.NoError(t, err)
	require.NoError(t, s.Runtime.Store.Change(ctx, scope, func(g *collaboration.Group) error {
		v := g.Members[0].Runs[0]
		v.State = "running"
		v.Owner = "owner"
		v.Lease = time.Now().Add(time.Minute)
		return nil
	}))
	g, err := s.Runtime.Store.Read(ctx, scope)
	require.NoError(t, err)
	work := &collaboration.Work{Scope: scope, Member: *g.Members[0], Run: *g.Members[0].Runs[0]}
	b := &collaboration.Binding{Runtime: s.Runtime, Scope: scope, Child: work}
	r := tools.NewToolRegistry()
	write := &collaborationWriteTool{
		BaseTool: tools.NewBaseTool("unsafe_write", "d", json.RawMessage(`{"type":"object"}`)),
	}
	r.RegisterTool(write)
	require.NoError(
		t,
		s.attach(collaboration.WithBinding(ctx, b), &agent.AgentEngine{}, r, &types.AgentConfig{}, "child", "cm"),
	)
	_, err = r.ExecuteTool(ctx, "unsafe_write", json.RawMessage(`{}`))
	require.ErrorContains(t, err, "requires approval")
	require.False(t, write.called)
	snapshot, err = s.resolve(ctx, "a", types.AssistantAccessAutoWrite)
	require.NoError(t, err)
	work.Member.ConfigJSON = snapshot
	s.tenants = &collaborationTenants{level: types.AssistantAccessAutoWrite}
	require.NoError(
		t,
		s.attach(collaboration.WithBinding(ctx, b), &agent.AgentEngine{}, r, &types.AgentConfig{}, "child", "cm"),
	)
	_, err = r.ExecuteTool(ctx, "unsafe_write", json.RawMessage(`{}`))
	require.NoError(t, err)
	require.True(t, write.called)
	write.called = false
	s.tenants = &collaborationTenants{level: types.AssistantAccessReadOnly}
	_, err = r.ExecuteTool(ctx, "unsafe_write", json.RawMessage(`{}`))
	require.ErrorContains(t, err, "access changed")
	require.False(t, write.called)
	s.members = &collaborationMembers{active: false}
	_, err = r.ExecuteTool(ctx, "unsafe_write", json.RawMessage(`{}`))
	require.ErrorContains(t, err, "membership")
	require.False(t, write.called)
}

type collaborationWriteTool struct {
	tools.BaseTool
	called bool
}

func (t *collaborationWriteTool) Execute(context.Context, json.RawMessage) (*types.ToolResult, error) {
	t.called = true
	return &types.ToolResult{Success: true}, nil
}
