package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/agent"
	"github.com/Tencent/WeKnora/internal/agent/collaboration"
	"github.com/Tencent/WeKnora/internal/event"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestAutomationDelegatedExecutionKeepsIdentity(t *testing.T) {
	for _, inherited := range []bool{false, true} {
		t.Run(fmt.Sprintf("inherited=%t", inherited), func(t *testing.T) {
			svc, agents, _, ctx := collaborationService(t)
			via := types.AutomationActingVia("scheduled-task")
			parent := types.WithPrincipal(ctx, types.Principal{Type: types.PrincipalWebUser, ID: "u", ActingVia: via})
			parent = types.WithRunMode(parent, types.RunModeDeferred)
			var snapshot string
			var err error
			if inherited {
				parent = context.WithValue(parent, childSourceKey{}, childSource{Agent: agents.agent, ModelID: "model"})
				snapshot, err = inheritedChildSnapshot(
					parent,
					&types.AgentConfig{AssistantAccess: types.AssistantAccessAutoWrite},
					nil,
				)
			} else {
				snapshot, err = svc.resolve(parent, "a", types.AssistantAccessAutoWrite)
			}
			require.NoError(t, err)
			scope := collaboration.Scope{TenantID: automationTenant, UserID: "u", SessionID: "parent", ID: "answer"}
			require.NoError(t, svc.Runtime.Store.Ensure(ctx, scope))
			_, err = svc.Runtime.Spawn(ctx, scope, "spawn", "child", "a", snapshot, "work")
			require.NoError(t, err)
			require.NoError(t, svc.Runtime.Store.Change(ctx, scope, func(g *collaboration.Group) error {
				r := g.Members[0].Runs[0]
				r.State = "running"
				r.Owner = "worker"
				r.Lease = time.Now().Add(time.Minute)
				return nil
			}))
			called := false
			svc.Bind(
				&collaborationSessions{
					run: func(child context.Context, req *types.QARequest, bus *event.EventBus) error {
						called = true
						require.Equal(t, via, types.ActingViaFromContext(child))
						require.Equal(t, types.RunModeDeferred, req.Mode)
						require.False(t, req.Mode.Attended(), "ask_user must stay unavailable")
						_, _, err := automationOwner(child)
						require.Error(t, err, "delegation must not bypass the automation management guard")
						return complete(child, bus, event.AgentCompleteData{FinalAnswer: "done"})
					},
				},
			)
			g, err := svc.Runtime.Store.Read(ctx, scope)
			require.NoError(t, err)
			// A scheduler starts from its own context, not the parent's request context.
			_, err = svc.run(
				context.Background(),
				collaboration.Work{Scope: scope, Member: *g.Members[0], Run: *g.Members[0].Runs[0]},
			)
			require.NoError(t, err)
			require.True(t, called)
		})
	}
}

func TestAutomationCancelStopsDelegationAndRecovery(t *testing.T) {
	f := newAutomationFixture(t)
	require.NoError(t, f.db.AutoMigrate(&collaboration.Record{}))
	delegated := NewAgentCollaboration(f.db, f.agents, f.messages, f.users, f.members, f.svc.tenants)
	f.svc.StopDelegation = delegated.StopOwned
	a := f.create(t, types.AutomationPatch{})
	queued, err := f.svc.RunNow(f.ctx, a.ID)
	require.NoError(t, err)
	entered := make(chan collaboration.Scope, 1)
	f.sessions.run = func(ctx context.Context, req *types.QARequest, _ *event.EventBus) error {
		scope := collaboration.Scope{
			TenantID:  automationTenant,
			UserID:    "u",
			SessionID: req.Session.ID,
			ID:        req.AssistantMessageID,
		}
		b, err := delegated.Runtime.AcquireParent(ctx, scope)
		if err != nil {
			return err
		}
		_, err = delegated.Runtime.Spawn(b.Context(ctx), scope, "spawn", "child", "a", "{}", "work")
		if err != nil {
			return err
		}
		_, err = delegated.Runtime.Spawn(b.Context(ctx), scope, "spawn-running", "running_child", "a", "{}", "work")
		if err != nil {
			return err
		}
		if err = delegated.Runtime.Store.Change(b.Context(ctx), scope, func(g *collaboration.Group) error {
			r := g.Members[1].Runs[0]
			r.State, r.Owner, r.Lease = "running", "remote-worker", time.Now().Add(time.Minute)
			return nil
		}); err != nil {
			return err
		}
		saved := resumableAgentRequest(ctx, req)
		data, err := json.Marshal(
			agent.RunSnapshot{Version: agent.RunSnapshotVersion, Request: mustJSON(t, saved), CollaborationWait: true},
		)
		if err != nil {
			return err
		}
		if err = b.SaveWait(ctx, data, time.Now().Add(time.Minute)); err != nil {
			return err
		}
		entered <- scope
		<-ctx.Done()
		// Exercise the same cleanup as an engine cancelled while in wait_agent.
		b.End(true)
		return ctx.Err()
	}
	require.NoError(t, f.svc.startQueued(f.ctx, f.now))
	var scope collaboration.Scope
	select {
	case scope = <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("run did not start")
	}
	_, err = f.svc.CancelRun(f.ctx, queued.ID)
	require.NoError(t, err)
	done := f.settleRun(t, queued.ID)
	require.Equal(t, types.AutomationRunCancelled, done.Status)
	g, err := delegated.Runtime.Store.Read(f.ctx, scope)
	require.NoError(t, err)
	require.NotEqual(t, "active", g.Status)
	require.Nil(t, g.ParentWait, "cancelled checkpoints must not be recovered")
	require.True(t, g.Members[0].Runs[0].CancelRequested)
	require.Equal(t, "cancelled", g.Members[0].Runs[0].State)
	require.True(t, g.Members[1].Runs[0].CancelRequested)
	require.ErrorIs(t, delegated.Runtime.CheckOwner(f.ctx, collaboration.Work{
		Scope: scope, Member: *g.Members[1], Run: *g.Members[1].Runs[0],
	}), collaboration.ErrConflict, "a remote child must lose permission to execute tools immediately")
}

func mustJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	data, err := json.Marshal(v)
	require.NoError(t, err)
	return data
}

func TestAutomationParentRecoveryDoesNotEscapeScheduler(t *testing.T) {
	svc, _, messages, ctx := collaborationService(t)
	scope := collaboration.Scope{TenantID: automationTenant, UserID: "u", SessionID: "parent", ID: "answer"}
	b, err := svc.Runtime.AcquireParent(ctx, scope)
	require.NoError(t, err)
	t.Cleanup(func() { b.End(false) })
	require.NoError(t, messages.UpdateMessage(ctx, &types.Message{ID: scope.ID, SessionID: scope.SessionID}))
	svc.streams = &collaborationStreams{}
	svc.ParentEvents = func(
		context.Context, *types.Session, *types.Message, *event.EventBus,
	) func(context.Context) error {
		return func(context.Context) error { return nil }
	}
	called := false
	svc.Bind(
		&collaborationSessions{
			run: func(context.Context, *types.QARequest, *event.EventBus) error { called = true; return nil },
		},
	)
	ctx = types.WithPrincipal(
		ctx,
		types.Principal{Type: types.PrincipalWebUser, ID: "u", ActingVia: types.AutomationActingVia("a")},
	)
	req := resumableAgentRequest(
		ctx,
		&types.QARequest{CustomAgent: &types.CustomAgent{ID: "a"}, Mode: types.RunModeDeferred},
	)
	snapshot := mustJSON(
		t,
		agent.RunSnapshot{Version: agent.RunSnapshotVersion, Request: mustJSON(t, req), CollaborationWait: true},
	)
	err = svc.resumeParent(context.Background(), b, snapshot)
	require.ErrorContains(t, err, "scheduler-owned parent")
	require.False(t, called)
}

func TestAutomationStartSerializesDifferentRunsAcrossInstances(t *testing.T) {
	f := newAutomationFixture(t)
	a := f.create(t, types.AutomationPatch{OverlapPolicy: ptr(types.AutomationPolicyQueue)})
	rows := []*types.AutomationRun{
		{
			AutomationID: a.ID,
			TenantID:     automationTenant,
			ScheduledAt:  f.now.Add(-time.Hour),
			TriggerKind:  types.AutomationRunSchedule,
			Status:       types.AutomationRunQueued,
			Resume:       true,
		},
		{
			AutomationID: a.ID,
			TenantID:     automationTenant,
			ScheduledAt:  f.now,
			TriggerKind:  types.AutomationRunSchedule,
			Status:       types.AutomationRunQueued,
		},
	}
	for _, r := range rows {
		require.NoError(t, f.db.Create(r).Error)
	}
	// A separate connection pool represents another scheduler process.
	db, err := gorm.Open(
		sqlite.Open(f.db.Dialector.(*sqlite.Dialector).DSN),
		&gorm.Config{Logger: logger.Default.LogMode(logger.Silent)},
	)
	require.NoError(t, err)
	raw, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = raw.Close() })
	other := &AutomationService{db: db, instance: "second-instance"}
	arrived := make(chan struct{}, 2)
	release := make(chan struct{})
	for _, db := range []*gorm.DB{f.db, db} {
		require.NoError(t, db.Callback().Query().After("gorm:query").Register("admission_barrier", func(db *gorm.DB) {
			if n, ok := db.Statement.Dest.(*int64); ok && *n == 0 && db.Statement.Table == "automation_runs" &&
				strings.Contains(db.Statement.SQL.String(), "count(*)") {
				arrived <- struct{}{}
				<-release
			}
		}))
	}
	var wg sync.WaitGroup
	wg.Add(2)
	started := make([]bool, 2)
	errs := make([]error, 2)
	go func() { defer wg.Done(); started[0], errs[0] = f.svc.startRun(f.ctx, rows[0], f.now) }()
	select {
	case <-arrived:
	case <-time.After(5 * time.Second):
		close(release)
		t.Fatal("first admission did not reach check")
	}
	go func() { defer wg.Done(); started[1], errs[1] = other.startRun(f.ctx, rows[1], f.now) }()
	// The old implementation lets both count queries observe zero. With the
	// automation lock the second query cannot run until the first commits.
	select {
	case <-arrived:
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	wg.Wait()
	require.NoError(t, errs[0])
	require.NoError(t, errs[1])
	require.True(t, started[0])
	require.False(t, started[1])
	var active int64
	require.NoError(
		t,
		f.db.Model(&types.AutomationRun{}).
			Where("automation_id = ? AND status = ?", a.ID, types.AutomationRunRunning).
			Count(&active).
			Error,
	)
	require.EqualValues(t, 1, active)
}

func TestAutomationQueuedRunsSkipBlockedHead(t *testing.T) {
	f := newAutomationFixture(t)
	for i := 0; i < 16; i++ {
		a := f.create(
			t,
			types.AutomationPatch{
				Name:          ptr(fmt.Sprintf("blocked%d", i)),
				OverlapPolicy: ptr(types.AutomationPolicyQueue),
			},
		)
		for j, status := range []string{types.AutomationRunAwaitingApproval, types.AutomationRunQueued} {
			require.NoError(
				t,
				f.db.Create(
					&types.AutomationRun{
						AutomationID: a.ID,
						TenantID:     automationTenant,
						ScheduledAt:  f.now.Add(time.Duration(j-2) * time.Hour),
						TriggerKind:  types.AutomationRunSchedule,
						Status:       status,
					},
				).Error,
			)
		}
	}
	a := f.create(t, types.AutomationPatch{Name: ptr("runnable")})
	r, err := f.svc.RunNow(f.ctx, a.ID)
	require.NoError(t, err)
	require.NoError(t, f.svc.startQueued(context.Background(), f.now))
	require.Eventually(t, func() bool {
		var row types.AutomationRun
		return f.db.First(&row, "id = ?", r.ID).Error == nil && row.Status == types.AutomationRunSucceeded &&
			len(f.svc.slots) == 0
	}, 5*time.Second, 10*time.Millisecond)
}

func TestAutomationApprovalReconciliationVisitsEveryBatch(t *testing.T) {
	f := newAutomationFixture(t)
	f.svc.cfg.MaxPerTenant = 200
	var expired, answered *types.AutomationRun
	for i := 0; i < 102; i++ {
		timeout := 10080
		updated := f.now.Add(-time.Hour)
		if i == 100 {
			timeout = 5
			updated = f.now.Add(-10 * time.Minute)
		}
		a := f.create(
			t,
			types.AutomationPatch{Name: ptr(fmt.Sprintf("approval%d", i)), ApprovalTimeoutMinutes: ptr(timeout)},
		)
		status := types.AgentPromptSuspended
		if i == 0 || i == 101 {
			status = types.AgentPromptAnswered
		}
		p := &types.AgentPrompt{ID: fmt.Sprintf("p%d", i), Status: status}
		require.NoError(t, f.broker.Store().Create(f.ctx, p))
		r := &types.AutomationRun{
			ID:              fmt.Sprintf("run-%03d", i),
			AutomationID:    a.ID,
			TenantID:        automationTenant,
			ScheduledAt:     updated,
			TriggerKind:     types.AutomationRunSchedule,
			Status:          types.AutomationRunAwaitingApproval,
			PendingPromptID: p.ID,
			UpdatedAt:       updated,
		}
		require.NoError(t, f.db.Create(r).Error)
		if i == 100 {
			expired = r
		}
		if i == 101 {
			answered = r
		}
	}
	require.NoError(t, f.svc.reconcileAwaiting(f.ctx, f.now))
	require.NoError(t, f.db.First(expired, "id = ?", expired.ID).Error)
	require.Equal(t, types.AutomationRunFailed, expired.Status)
	require.NoError(t, f.db.First(answered, "id = ?", answered.ID).Error)
	require.Equal(t, types.AutomationRunQueued, answered.Status)
	require.True(t, answered.Resume)
}

func TestAutomationApprovalResumeUsesRemainingExecutionBudget(t *testing.T) {
	f := newAutomationFixture(t)
	f.svc.cfg.DefaultMaxDuration = time.Second
	a := f.create(t, types.AutomationPatch{})
	f.suspendOnApproval(t, "op:x")
	original := f.sessions.run
	f.sessions.run = func(ctx context.Context, req *types.QARequest, bus *event.EventBus) error {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(600 * time.Millisecond):
		}
		return original(ctx, req, bus)
	}
	queued, err := f.svc.RunNow(f.ctx, a.ID)
	require.NoError(t, err)
	suspended := f.settleRun(t, queued.ID)
	require.Equal(t, types.AutomationRunAwaitingApproval, suspended.Status)
	// Approval waiting is free, even across service restarts.
	f.now = f.now.Add(12 * time.Hour)
	restarted := NewAutomationService(
		f.db,
		f.agents,
		f.users,
		f.members,
		f.svc.tenants,
		f.messages,
		f.svc.grants,
		f.svc.notices,
		f.svc.models,
	)
	restarted.Configure(AutomationConfig{DefaultMaxDuration: time.Second})
	restarted.now = func() time.Time { return f.now }
	restarted.Bind(f.sessions, f.broker, nil, nil)
	f.svc = restarted
	ok, err := f.broker.Store().Transition(f.ctx, suspended.PendingPromptID, types.AgentPromptTransition{
		From:     []string{types.AgentPromptSuspended},
		To:       types.AgentPromptAnswered,
		Response: types.JSON(`{"approved":true}`),
	})
	require.NoError(t, err)
	require.True(t, ok)
	resumed := f.settleRun(t, queued.ID)
	require.Equal(t, types.AutomationRunFailed, resumed.Status)
	require.Contains(t, resumed.Error, "longer than 1s")
	require.NotNil(t, f.sessions.last().req.Resume, "waiting time must not exhaust the execution budget")
}

func TestAutomationStaleFinalizerDoesNotCancelResumedDelegation(t *testing.T) {
	f := newAutomationFixture(t)
	a := f.create(t, types.AutomationPatch{})
	r, err := f.svc.RunNow(f.ctx, a.ID)
	require.NoError(t, err)
	require.NoError(t, f.db.Model(r).Updates(map[string]any{
		"status": types.AutomationRunRunning, "session_id": "session", "assistant_message_id": "answer",
	}).Error)
	stopped := false
	f.svc.StopDelegation = func(context.Context, uint64, string, string, string) error { stopped = true; return nil }
	ok, err := f.svc.finishRun(
		f.ctx,
		r,
		[]string{types.AutomationRunAwaitingApproval},
		types.AutomationRunFailed,
		"stale timeout",
		nil,
	)
	require.NoError(t, err)
	require.False(t, ok)
	require.False(t, stopped)
	ok, err = f.svc.finishRun(
		f.ctx,
		r,
		[]string{types.AutomationRunRunning},
		types.AutomationRunNeedsAttention,
		"executor lost",
		nil,
	)
	require.NoError(t, err)
	require.True(t, ok)
	require.True(t, stopped)
}

func TestAutomationCancellationWinsApprovalRace(t *testing.T) {
	f := newAutomationFixture(t)
	a := f.create(t, types.AutomationPatch{})
	f.suspendOnApproval(t, "op:x")
	queued, err := f.svc.RunNow(f.ctx, a.ID)
	require.NoError(t, err)
	r := f.settleRun(t, queued.ID)
	require.Equal(t, types.AutomationRunAwaitingApproval, r.Status)
	// A remote cancellation can land after execute checked its context but
	// before it records the approval suspension. Do not resume that run.
	require.NoError(t, f.db.Model(r).Update("cancel_requested", true).Error)
	ok, err := f.broker.Store().Transition(f.ctx, r.PendingPromptID, types.AgentPromptTransition{
		From:     []string{types.AgentPromptSuspended},
		To:       types.AgentPromptAnswered,
		Response: types.JSON(`{"approved":true}`),
	})
	require.NoError(t, err)
	require.True(t, ok)
	require.NoError(t, f.svc.queueResume(f.ctx, r.ID, r.PendingPromptID))
	require.NoError(t, f.svc.reconcileAwaiting(f.ctx, f.now))
	require.NoError(t, f.db.First(&r, "id = ?", r.ID).Error)
	require.Equal(t, types.AutomationRunCancelled, r.Status)
}
