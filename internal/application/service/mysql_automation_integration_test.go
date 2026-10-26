//go:build mysqlintegration

package service

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/testutil"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestMySQLMainAutomationClaimsAndSerializesRuns(t *testing.T) {
	db := testutil.MySQL(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)
	due := now.Add(-time.Second)
	a := &types.Automation{ID: "automation", TenantID: 1, OwnerUserID: "user", Name: "once", Instruction: "run", Trigger: types.AutomationTrigger{Type: types.AutomationTriggerOnce, At: due.Format(time.RFC3339Nano)}, NextRunAt: &due}
	require.NoError(t, db.Create(a).Error)
	s := NewAutomationService(db, nil, nil, nil, nil, nil, nil, NewNotificationService(db), nil)
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for range 2 {
		copy := *a
		wg.Add(1)
		go func() { defer wg.Done(); errs <- s.claim(ctx, &copy, now) }()
	}
	wg.Wait()
	for range 2 {
		require.NoError(t, <-errs)
	}
	var runs []*types.AutomationRun
	require.NoError(t, db.Where("automation_id = ?", a.ID).Find(&runs).Error)
	require.Len(t, runs, 1)
	second := &types.AutomationRun{AutomationID: a.ID, TenantID: 1, ScheduledAt: now, TriggerKind: types.AutomationRunManual, Status: types.AutomationRunQueued}
	require.NoError(t, db.Create(second).Error)
	results := make(chan bool, 2)
	for _, run := range []*types.AutomationRun{runs[0], second} {
		wg.Add(1)
		go func() { defer wg.Done(); ok, err := s.startRun(ctx, run, now); results <- ok; errs <- err }()
	}
	wg.Wait()
	winners := 0
	for range 2 {
		require.NoError(t, <-errs)
		if <-results {
			winners++
		}
	}
	require.Equal(t, 1, winners, "different queued runs of one automation cannot execute together")
	var active int64
	require.NoError(t, db.Model(&types.AutomationRun{}).Where("automation_id = ? AND status = ?", a.ID, types.AutomationRunRunning).Count(&active).Error)
	require.EqualValues(t, 1, active)
}

func TestMySQLMainNotificationPendingAndHistory(t *testing.T) {
	db := testutil.MySQL(t)
	ctx := types.WithCaller(context.Background(), types.Caller{TenantID: 1, UserID: "user"})
	now := time.Now().UTC().Truncate(time.Microsecond)
	s := NewNotificationService(db)
	s.now = func() time.Time { return now }
	a := &types.Automation{ID: "automation", TenantID: 1, OwnerUserID: "user", Name: "scheduled", Instruction: "run", ApprovalTimeoutMinutes: 60}
	require.NoError(t, db.Create(a).Error)
	p := &types.AgentPrompt{ID: "prompt", TenantID: 1, UserID: automationGrantUser("user"), SessionID: "session", Kind: types.AgentPromptKindToolApproval, Status: types.AgentPromptSuspended, CreatedAt: now}
	require.NoError(t, db.Create(p).Error)
	run := &types.AutomationRun{ID: "run", AutomationID: a.ID, TenantID: 1, SessionID: p.SessionID, PendingPromptID: p.ID, Status: types.AutomationRunAwaitingApproval, TriggerKind: types.AutomationRunManual, ScheduledAt: now, UpdatedAt: now}
	require.NoError(t, db.Create(run).Error)
	for _, id := range []string{"n1", "n2", "n3"} {
		s.Notify(ctx, &types.Notification{ID: id, TenantID: 1, UserID: "user", Kind: types.NotificationAutomationSucceeded, Title: "done", CreatedAt: now})
	}
	s.Notify(ctx, &types.Notification{TenantID: 2, UserID: "user", Kind: types.NotificationAutomationSucceeded, Title: "private"})
	page, err := s.List(ctx, 2, false, "")
	require.NoError(t, err)
	require.Len(t, page.Items, 2)
	require.EqualValues(t, 3, page.Unread)
	require.Len(t, page.Pending, 1)
	require.Equal(t, p.ID, page.Pending[0].ID)
	next, err := s.List(ctx, 2, false, page.NextCursor)
	require.NoError(t, err)
	require.Len(t, next.Items, 1)
	require.Equal(t, "n1", next.Items[0].ID)
	require.NoError(t, s.MarkRead(ctx, nil))
	require.NoError(t, s.ClearRead(ctx))
	page, err = s.List(ctx, 2, false, "")
	require.NoError(t, err)
	require.Empty(t, page.Items)
	require.Len(t, page.Pending, 1)
	other, err := s.List(types.WithCaller(ctx, types.Caller{TenantID: 2, UserID: "user"}), 2, false, "")
	require.NoError(t, err)
	require.Empty(t, other.Pending)
	require.Len(t, other.Items, 1)
}
