package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/agent"
	"github.com/Tencent/WeKnora/internal/agent/hitl"
	"github.com/Tencent/WeKnora/internal/application/repository"
	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/event"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

const automationTenant uint64 = 7

type automationMessages struct {
	interfaces.MessageService
	mu   sync.Mutex
	rows map[string]*types.Message
}

func (f *automationMessages) CreateMessage(_ context.Context, m *types.Message) (*types.Message, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	m.ID = uuid.NewString()
	copied := *m
	f.rows[m.ID] = &copied
	return m, nil
}

func (f *automationMessages) GetMessage(_ context.Context, _, id string) (*types.Message, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	m, ok := f.rows[id]
	if !ok {
		return nil, errors.New("message not found")
	}
	copied := *m
	return &copied, nil
}

func (f *automationMessages) UpdateMessage(_ context.Context, m *types.Message) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	copied := *m
	f.rows[m.ID] = &copied
	return nil
}

// automationCall is what one AgentQA call of a run saw.
type automationCall struct {
	req      *types.QARequest
	mode     types.RunMode
	via      string
	standing types.GrantScope
}

type automationSessions struct {
	interfaces.SessionService
	db    *gorm.DB
	mu    sync.Mutex
	calls []automationCall
	run   func(context.Context, *types.QARequest, *event.EventBus) error
}

func (f *automationSessions) GetSession(ctx context.Context, id string) (*types.Session, error) {
	var s types.Session
	if err := f.db.WithContext(ctx).Where("id = ?", id).First(&s).Error; err != nil {
		return nil, err
	}
	return &s, nil
}

func (f *automationSessions) AgentQA(ctx context.Context, req *types.QARequest, bus *event.EventBus) error {
	principal, _ := types.PrincipalFromContext(ctx)
	standing, _ := types.StandingGrantScopeFromContext(ctx)
	f.mu.Lock()
	f.calls = append(f.calls, automationCall{req: req, mode: req.Mode, via: principal.ActingVia, standing: standing})
	f.mu.Unlock()
	return f.run(ctx, req, bus)
}

func (f *automationSessions) last() automationCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[len(f.calls)-1]
}

func complete(ctx context.Context, bus *event.EventBus, data event.AgentCompleteData) error {
	return bus.Emit(ctx, event.Event{Type: event.EventAgentComplete, Data: data})
}

type automationAgents struct {
	interfaces.CustomAgentService
	agent *types.CustomAgent
}

func (f *automationAgents) GetAgentByID(_ context.Context, id string) (*types.CustomAgent, error) {
	if id != types.BuiltinWeKnoraAssistantID {
		return nil, errors.New("automations must load the WeKnora assistant")
	}
	return f.agent, nil
}

type automationFixture struct {
	svc      *AutomationService
	db       *gorm.DB
	sessions *automationSessions
	messages *automationMessages
	users    *collaborationUsers
	members  *collaborationMembers
	agents   *automationAgents
	broker   *hitl.Broker
	now      time.Time
	ctx      context.Context
}

func newAutomationFixture(t *testing.T) *automationFixture {
	t.Helper()
	db, err := gorm.Open(
		sqlite.Open(filepath.Join(t.TempDir(), "automation.db")+"?_busy_timeout=5000&_journal_mode=WAL"),
		&gorm.Config{Logger: logger.Default.LogMode(logger.Silent)},
	)
	require.NoError(t, err)
	raw, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = raw.Close() })
	require.NoError(t, db.AutoMigrate(&types.Automation{}, &types.AutomationRun{}, &types.Notification{},
		&types.Session{}, &types.AgentGrant{}, &types.AgentPrompt{}))
	f := &automationFixture{
		db:       db,
		messages: &automationMessages{rows: map[string]*types.Message{}},
		users:    &collaborationUsers{active: true},
		members:  &collaborationMembers{active: true},
		agents: &automationAgents{agent: &types.CustomAgent{
			ID: types.BuiltinWeKnoraAssistantID, IsBuiltin: true,
			Config: types.CustomAgentConfig{AgentMode: types.AgentModeSmartReasoning, AllowedTools: []string{"x"}},
		}},
		broker: hitl.NewBroker(nil, nil),
		now:    time.Date(2026, 10, 19, 1, 0, 0, 0, time.UTC),
	}
	done := func(ctx context.Context, _ *types.QARequest, bus *event.EventBus) error {
		return complete(ctx, bus, event.AgentCompleteData{FinalAnswer: "done"})
	}
	f.sessions = &automationSessions{db: db, run: done}
	f.svc = NewAutomationService(db, f.agents, f.users, f.members, &collaborationTenants{},
		f.messages, repository.NewAgentGrantRepository(db), NewNotificationService(db), nil)
	f.svc.now = func() time.Time { return f.now }
	f.svc.Bind(f.sessions, f.broker, nil, nil)
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, automationTenant)
	ctx = context.WithValue(ctx, types.UserIDContextKey, "u")
	ctx = types.WithCaller(ctx, types.Caller{
		TenantID: automationTenant, UserID: "u", Role: types.TenantRoleContributor,
	})
	f.ctx = types.WithPrincipal(ctx, types.Principal{Type: types.PrincipalWebUser, ID: "u"})
	return f
}

func (f *automationFixture) create(t *testing.T, p types.AutomationPatch) *types.Automation {
	t.Helper()
	if p.Name == nil {
		p.Name = ptr("daily")
	}
	if p.Instruction == nil {
		p.Instruction = ptr("summarize {{automation_name}} at {{now}}")
	}
	if p.Trigger == nil {
		p.Trigger = &types.AutomationTrigger{Type: types.AutomationTriggerCron, Expr: "0 9 * * *"}
	}
	a, err := f.svc.Create(f.ctx, &p)
	require.NoError(t, err)
	return a
}

func (f *automationFixture) runs(t *testing.T, id string) []types.AutomationRun {
	t.Helper()
	var runs []types.AutomationRun
	require.NoError(t, f.db.Where("automation_id = ?", id).Order("scheduled_at, created_at").Find(&runs).Error)
	return runs
}

func (f *automationFixture) reload(t *testing.T, id string) *types.Automation {
	t.Helper()
	var a types.Automation
	require.NoError(t, f.db.Unscoped().Where("id = ?", id).First(&a).Error)
	return &a
}

// settleRun runs scheduler ticks until run id rests.
func (f *automationFixture) settleRun(t *testing.T, id string) types.AutomationRun {
	t.Helper()
	var run types.AutomationRun
	require.Eventually(t, func() bool {
		f.svc.tick(context.Background())
		require.NoError(t, f.db.Where("id = ?", id).First(&run).Error)
		return run.Status != types.AutomationRunQueued && run.Status != types.AutomationRunRunning &&
			len(f.svc.slots) == 0
	}, 5*time.Second, 10*time.Millisecond)
	return run
}

func TestAutomationCreateValidatesAndSchedules(t *testing.T) {
	f := newAutomationFixture(t)
	a := f.create(t, types.AutomationPatch{Trigger: &types.AutomationTrigger{
		Type: types.AutomationTriggerCron, Expr: "0 9 * * *", Timezone: "Asia/Shanghai",
	}})
	require.Equal(t, time.Date(2026, 10, 20, 1, 0, 0, 0, time.UTC), a.NextRunAt.UTC())
	require.Equal(t, types.AssistantAccessConfirm, a.AccessLevel)
	require.Equal(t, types.AutomationSessionPerRun, a.SessionMode)

	for name, p := range map[string]types.AutomationPatch{
		"too frequent": {Trigger: &types.AutomationTrigger{Type: types.AutomationTriggerCron, Expr: "* * * * *"}},
		"bad mode":     {SessionMode: ptr("forever")},
		"once in past": {Trigger: &types.AutomationTrigger{
			Type: types.AutomationTriggerOnce, At: f.now.Add(-time.Hour).Format(time.RFC3339),
		}},
	} {
		p.Name, p.Instruction = ptr("n"), ptr("i")
		if p.Trigger == nil {
			p.Trigger = &types.AutomationTrigger{Type: types.AutomationTriggerCron, Expr: "0 9 * * *"}
		}
		_, err := f.svc.Create(f.ctx, &p)
		var appErr *apperrors.AppError
		require.Truef(t, errors.As(err, &appErr), "%s: %v", name, err)
	}

	other := types.WithCaller(f.ctx, types.Caller{TenantID: automationTenant, UserID: "v"})
	other = types.WithPrincipal(other, types.Principal{Type: types.PrincipalWebUser, ID: "v"})
	_, err := f.svc.Get(other, a.ID)
	require.Error(t, err)
	list, err := f.svc.List(other)
	require.NoError(t, err)
	require.Empty(t, list)

	acting := types.WithPrincipal(f.ctx, types.Principal{
		Type: types.PrincipalWebUser, ID: "u", ActingVia: "automation:x",
	})
	_, err = f.svc.List(acting)
	require.Error(t, err, "an automation run must not manage automations")
}

func TestAutomationClaimRunsOnceAcrossInstances(t *testing.T) {
	f := newAutomationFixture(t)
	a := f.create(t, types.AutomationPatch{})
	f.now = a.NextRunAt.Add(time.Second)
	stale := *a
	require.NoError(t, f.svc.claim(f.ctx, a, f.now))
	require.NoError(t, f.svc.claim(f.ctx, &stale, f.now))
	runs := f.runs(t, a.ID)
	require.Len(t, runs, 1)
	require.Equal(t, types.AutomationRunSchedule, runs[0].TriggerKind)
	require.Equal(t, a.NextRunAt.UTC(), runs[0].ScheduledAt.UTC())
	require.True(t, f.reload(t, a.ID).NextRunAt.After(f.now))
}

func TestAutomationMisfireAndOverlap(t *testing.T) {
	f := newAutomationFixture(t)
	skip := f.create(t, types.AutomationPatch{})
	catchUp := f.create(t, types.AutomationPatch{MisfirePolicy: ptr(types.AutomationPolicyRunOnce)})
	f.now = skip.NextRunAt.Add(3 * 24 * time.Hour)
	require.NoError(t, f.svc.claimDue(f.ctx, f.now))
	require.Empty(t, f.runs(t, skip.ID))
	runs := f.runs(t, catchUp.ID)
	require.Len(t, runs, 1)
	require.Equal(t, types.AutomationRunMisfire, runs[0].TriggerKind)

	// The caught-up run is still queued, so the next due run is skipped.
	f.now = f.reload(t, catchUp.ID).NextRunAt.Add(time.Second)
	require.NoError(t, f.svc.claimDue(f.ctx, f.now))
	runs = f.runs(t, catchUp.ID)
	require.Len(t, runs, 2)
	require.Equal(t, types.AutomationRunSkipped, runs[1].Status)
}

func TestAutomationRunSucceedsAsOwnerUnattended(t *testing.T) {
	f := newAutomationFixture(t)
	a := f.create(t, types.AutomationPatch{AccessLevel: ptr(types.AssistantAccessAutoWrite)})
	run, err := f.svc.RunNow(f.ctx, a.ID)
	require.NoError(t, err)
	_, err = f.svc.RunNow(f.ctx, a.ID)
	require.Error(t, err, "a second manual run while one is queued")

	done := f.settleRun(t, run.ID)
	require.Equal(t, types.AutomationRunSucceeded, done.Status)
	require.Equal(t, "done", done.Answer)
	call := f.sessions.last()
	require.Equal(t, types.BuiltinWeKnoraAssistantID, call.req.CustomAgent.ID)
	require.Equal(t, types.RunModeDeferred, call.mode)
	require.Equal(t, types.AutomationActingVia(a.ID), call.via)
	require.Equal(t, types.GrantScope{
		Scope: types.AgentGrantScopeAutomation, ScopeID: a.ID,
		BindingHash: automationBinding(
			f.agents.agent, &types.Tenant{ID: automationTenant}, types.TenantRoleContributor),
	}, call.standing)
	require.Equal(t, types.AssistantAccessAutoWrite, call.req.AssistantAccess)
	require.Contains(t, call.req.Query, "summarize daily at ")
	require.Contains(t, call.req.RunNote, "No user is watching")

	var session types.Session
	require.NoError(t, f.db.Where("id = ?", done.SessionID).First(&session).Error)
	require.Equal(t, types.SessionKindAutomation, session.Kind)
	require.Equal(t, types.BuiltinWeKnoraAssistantID, session.LastRequestState.AgentID)
	require.Equal(t, "u", session.UserID)
	stored := f.reload(t, a.ID)
	require.Equal(t, types.AutomationRunSucceeded, stored.LastRunStatus)
	require.NotNil(t, stored.LastSuccessAt)
}

func TestAutomationPersistentSessionCarriesAcrossRuns(t *testing.T) {
	f := newAutomationFixture(t)
	a := f.create(t, types.AutomationPatch{SessionMode: ptr(types.AutomationSessionPersistent)})
	first, err := f.svc.RunNow(f.ctx, a.ID)
	require.NoError(t, err)
	r1 := f.settleRun(t, first.ID)
	f.now = f.now.Add(time.Minute)
	second, err := f.svc.RunNow(f.ctx, a.ID)
	require.NoError(t, err)
	r2 := f.settleRun(t, second.ID)
	require.Equal(t, r1.SessionID, r2.SessionID)
	require.Equal(t, r1.SessionID, f.reload(t, a.ID).PersistentSessionID)
}

func TestAutomationPausesWhenAssistantUnavailable(t *testing.T) {
	f := newAutomationFixture(t)
	a := f.create(t, types.AutomationPatch{})
	f.agents.agent = nil
	queued, err := f.svc.RunNow(f.ctx, a.ID)
	require.NoError(t, err)
	run := f.settleRun(t, queued.ID)
	require.Equal(t, types.AutomationRunFailed, run.Status)
	require.Contains(t, run.Error, "WeKnora assistant is unavailable")
	require.False(t, f.reload(t, a.ID).Enabled)
	require.Empty(t, f.sessions.calls)
}

func TestAutomationCarriesLastResult(t *testing.T) {
	f := newAutomationFixture(t)
	a := f.create(t, types.AutomationPatch{CarryLastResult: ptr(true)})
	first, _ := f.svc.RunNow(f.ctx, a.ID)
	f.settleRun(t, first.ID)
	f.now = f.now.Add(time.Minute)
	second, _ := f.svc.RunNow(f.ctx, a.ID)
	f.settleRun(t, second.ID)
	require.Contains(t, f.sessions.last().req.Query, "<previous_run_result>\ndone\n</previous_run_result>")
}

// suspendOnApproval makes the next run raise a suspended tool approval.
func (f *automationFixture) suspendOnApproval(t *testing.T, grant string) {
	f.sessions.run = func(ctx context.Context, req *types.QARequest, bus *event.EventBus) error {
		if req.Resume != nil {
			return complete(ctx, bus, event.AgentCompleteData{FinalAnswer: "resumed"})
		}
		request, _ := json.Marshal(types.ToolApprovalPromptRequest{
			RegisteredToolName: "delete_doc", Grant: grant, GrantRisk: "destructive", GrantLabel: "documents in kb1",
		})
		prompt := &types.AgentPrompt{
			ID: uuid.NewString(), TenantID: automationTenant, UserID: automationGrantUser("u"),
			SessionID: req.Session.ID, AssistantMessageID: req.AssistantMessageID,
			Kind: types.AgentPromptKindToolApproval, Status: types.AgentPromptSuspended, Suspendable: true,
			Request: request, CreatedAt: f.now,
		}
		require.NoError(t, f.broker.Store().Create(ctx, prompt))
		require.NoError(t, f.db.Create(prompt).Error)
		resumable, _ := json.Marshal(types.ResumableAgentRequest{SummaryModelID: req.SummaryModelID})
		snap, _ := json.Marshal(agent.RunSnapshot{
			Version: agent.RunSnapshotVersion, Query: req.Query, Request: resumable,
		})
		require.NoError(t, f.broker.Suspensions().Save(ctx, &types.AgentRunSuspension{
			AssistantMessageID: req.AssistantMessageID, PromptID: prompt.ID, SessionID: req.Session.ID,
			Snapshot: snap,
		}))
		return complete(ctx, bus, event.AgentCompleteData{
			Suspended: &types.PromptSuspension{PromptID: prompt.ID, Kind: prompt.Kind},
		})
	}
}

func TestAutomationSuspendsAndResumesAfterApproval(t *testing.T) {
	f := newAutomationFixture(t)
	a := f.create(t, types.AutomationPatch{})
	f.suspendOnApproval(t, "op:knowledge.delete:kb1")
	queued, _ := f.svc.RunNow(f.ctx, a.ID)
	run := f.settleRun(t, queued.ID)
	require.Equal(t, types.AutomationRunAwaitingApproval, run.Status)
	require.NotEmpty(t, run.PendingPromptID)
	notices := NewNotificationService(f.db)
	notices.now = func() time.Time { return f.now }
	page, err := notices.List(f.ctx, 10, false, "")
	require.NoError(t, err)
	require.Len(t, page.Pending, 1)
	require.Equal(t, types.AgentPromptKindToolApproval, page.Pending[0].Kind)

	prompt, _ := f.broker.Store().Get(f.ctx, run.PendingPromptID)
	require.True(t, f.svc.HandlesPrompt(f.ctx, prompt), "an automation prompt is not resumed as a chat")
	ok, err := f.broker.Store().Transition(f.ctx, prompt.ID, types.AgentPromptTransition{
		From: []string{types.AgentPromptSuspended}, To: types.AgentPromptAnswered,
		Response: types.JSON(`{"approved":true}`),
	})
	require.NoError(t, err)
	require.True(t, ok)
	prompt, _ = f.broker.Store().Get(f.ctx, prompt.ID)
	require.True(t, f.svc.HandlesPrompt(f.ctx, prompt))

	run = f.settleRun(t, run.ID)
	require.Equal(t, types.AutomationRunSucceeded, run.Status)
	require.Equal(t, "resumed", run.Answer)
	call := f.sessions.last()
	require.NotNil(t, call.req.Resume)
	require.Equal(t, run.SessionID, call.req.Session.ID)
	require.Equal(t, types.AutomationActingVia(a.ID), call.via)

	// A prompt of an ordinary chat turn in the same conversation is not claimed.
	require.False(t, f.svc.HandlesPrompt(f.ctx, &types.AgentPrompt{
		ID: "p2", TenantID: automationTenant, SessionID: run.SessionID, AssistantMessageID: "other",
	}))
}

func TestAutomationApprovalTimesOut(t *testing.T) {
	f := newAutomationFixture(t)
	a := f.create(t, types.AutomationPatch{ApprovalTimeoutMinutes: ptr(10)})
	f.suspendOnApproval(t, "op:x")
	queued, _ := f.svc.RunNow(f.ctx, a.ID)
	run := f.settleRun(t, queued.ID)
	require.Equal(t, types.AutomationRunAwaitingApproval, run.Status)
	f.now = f.now.Add(11 * time.Minute)
	f.svc.tick(f.ctx)
	run = f.runs(t, a.ID)[0]
	require.Equal(t, types.AutomationRunFailed, run.Status)
	require.Contains(t, run.Error, "no approval within 10 minutes")
	prompt, _ := f.broker.Store().Get(f.ctx, run.PendingPromptID)
	require.Equal(t, types.AgentPromptExpired, prompt.Status)
}

func TestAutomationPausesWhenOwnerLeaves(t *testing.T) {
	f := newAutomationFixture(t)
	a := f.create(t, types.AutomationPatch{})
	f.members.active = false
	queued, err := f.svc.RunNow(f.ctx, a.ID)
	require.NoError(t, err)
	run := f.settleRun(t, queued.ID)
	require.Equal(t, types.AutomationRunFailed, run.Status)
	require.Empty(t, f.sessions.calls, "nothing runs with the old permissions")
	stored := f.reload(t, a.ID)
	require.False(t, stored.Enabled)
	require.Contains(t, stored.PausedReason, "no longer a member")
}

func TestAutomationPausesAfterRepeatedFailures(t *testing.T) {
	f := newAutomationFixture(t)
	a := f.create(t, types.AutomationPatch{})
	f.sessions.run = func(context.Context, *types.QARequest, *event.EventBus) error {
		return errors.New("model unavailable")
	}
	for i := 0; i < 3; i++ {
		f.now = f.now.Add(time.Minute)
		queued, err := f.svc.RunNow(f.ctx, a.ID)
		require.NoError(t, err)
		require.Equal(t, types.AutomationRunFailed, f.settleRun(t, queued.ID).Status)
	}
	stored := f.reload(t, a.ID)
	require.False(t, stored.Enabled)
	require.Equal(t, 3, stored.ConsecutiveFailures)
	require.Contains(t, stored.PausedReason, "3 runs in a row failed")

	resumed, err := f.svc.Resume(f.ctx, a.ID)
	require.NoError(t, err)
	require.True(t, resumed.Enabled)
	require.Zero(t, resumed.ConsecutiveFailures)
}

func TestAutomationAbandonedRunNeedsAttention(t *testing.T) {
	f := newAutomationFixture(t)
	a := f.create(t, types.AutomationPatch{})
	past := f.now.Add(-time.Minute)
	run := &types.AutomationRun{
		AutomationID: a.ID, TenantID: automationTenant, ScheduledAt: past, TriggerKind: types.AutomationRunSchedule,
		Status: types.AutomationRunRunning, Owner: "dead", LeaseUntil: &past,
	}
	require.NoError(t, f.db.Create(run).Error)
	require.NoError(t, f.svc.expireLeases(f.ctx, f.now))
	require.Equal(t, types.AutomationRunNeedsAttention, f.runs(t, a.ID)[0].Status)
	require.Equal(t, 1, f.reload(t, a.ID).ConsecutiveFailures)
}

func TestAutomationCancelQueuedAndDelete(t *testing.T) {
	f := newAutomationFixture(t)
	a := f.create(t, types.AutomationPatch{})
	queued, _ := f.svc.RunNow(f.ctx, a.ID)
	cancelled, err := f.svc.CancelRun(f.ctx, queued.ID)
	require.NoError(t, err)
	require.Equal(t, types.AutomationRunCancelled, cancelled.Status)
	require.NoError(t, f.svc.Delete(f.ctx, a.ID))
	_, err = f.svc.Get(f.ctx, a.ID)
	require.Error(t, err)
}

func TestAutomationStandingGrantFromApproval(t *testing.T) {
	f := newAutomationFixture(t)
	a := f.create(t, types.AutomationPatch{})
	f.suspendOnApproval(t, "op:knowledge.delete:kb1")
	queued, _ := f.svc.RunNow(f.ctx, a.ID)
	run := f.settleRun(t, queued.ID)

	candidates, err := f.svc.GrantCandidates(f.ctx, a.ID)
	require.NoError(t, err)
	require.Len(t, candidates, 1)
	require.Equal(t, "destructive", candidates[0].Risk)
	require.Equal(t, "documents in kb1", candidates[0].Label)

	grant, err := f.svc.AddGrant(f.ctx, a.ID, run.PendingPromptID)
	require.NoError(t, err)
	require.Equal(t, types.AgentGrantScopeAutomation, grant.Scope)
	require.Equal(t, a.ID, grant.ScopeID)
	require.Equal(t, "destructive", grant.Risk)

	grants, err := f.svc.Grants(f.ctx, a.ID)
	require.NoError(t, err)
	require.Len(t, grants, 1)
	require.Empty(t, grants[0].Lapse)
	candidates, err = f.svc.GrantCandidates(f.ctx, a.ID)
	require.NoError(t, err)
	require.Empty(t, candidates)

	// Changing the agent's tools changes the binding the grant was given under.
	f.agents.agent.Config.AllowedTools = []string{"x", "y"}
	grants, err = f.svc.Grants(f.ctx, a.ID)
	require.NoError(t, err)
	require.Equal(t, types.AgentGrantLapseChanged, grants[0].Lapse)

	_, err = f.svc.AddGrant(f.ctx, a.ID, "unknown")
	require.Error(t, err)
	b := f.create(t, types.AutomationPatch{Name: ptr("other")})
	_, err = f.svc.AddGrant(f.ctx, b.ID, run.PendingPromptID)
	require.Error(t, err, "a prompt of another automation cannot become its grant")
}

func TestAutomationValidateTrigger(t *testing.T) {
	f := newAutomationFixture(t)
	times, err := f.svc.ValidateTrigger(types.AutomationTrigger{
		Type: types.AutomationTriggerCron, Expr: "0 9 * * MON", Timezone: "Asia/Shanghai",
	}, 3)
	require.NoError(t, err)
	require.Len(t, times, 3)
	shanghai := scheduleLocation(types.AutomationTrigger{Timezone: "Asia/Shanghai"})
	for i, at := range times {
		require.Equal(t, time.Monday, at.In(shanghai).Weekday(), fmt.Sprint(i))
	}
}

func TestAutomationFindBySession(t *testing.T) {
	f := newAutomationFixture(t)
	a := f.create(t, types.AutomationPatch{})
	require.NoError(t, f.db.Create(&types.AutomationRun{
		ID: "run-1", AutomationID: a.ID, TenantID: a.TenantID, ScheduledAt: f.now,
		TriggerKind: types.AutomationRunManual, Status: types.AutomationRunSucceeded, SessionID: "sess-1",
	}).Error)

	found, err := f.svc.FindBySession(f.ctx, "sess-1")
	require.NoError(t, err)
	require.Equal(t, a.ID, found.ID)

	require.NoError(t, f.db.Model(&types.Automation{}).Where("id = ?", a.ID).
		Update("persistent_session_id", "sess-persistent").Error)
	found, err = f.svc.FindBySession(f.ctx, "sess-persistent")
	require.NoError(t, err)
	require.Equal(t, a.ID, found.ID)

	_, err = f.svc.FindBySession(f.ctx, "missing")
	var appErr *apperrors.AppError
	require.ErrorAs(t, err, &appErr)
	require.Equal(t, apperrors.ErrNotFound, appErr.Code)
}
