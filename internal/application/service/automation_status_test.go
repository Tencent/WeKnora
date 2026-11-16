package service

import (
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestAutomationActiveRunStatus(t *testing.T) {
	f := newAutomationFixture(t)
	a := f.create(t, types.AutomationPatch{})
	require.NoError(t, f.db.Model(a).Update("last_run_status", types.AutomationRunSucceeded).Error)
	run, err := f.svc.RunNow(f.ctx, a.ID)
	require.NoError(t, err)
	// A newer queued occurrence must not hide work already executing or waiting.
	queued := &types.AutomationRun{
		AutomationID: a.ID, TenantID: a.TenantID, ScheduledAt: f.now.Add(time.Hour),
		TriggerKind: types.AutomationRunSchedule, Status: types.AutomationRunQueued,
	}
	require.NoError(t, f.db.Create(queued).Error)
	for _, status := range []string{
		types.AutomationRunQueued, types.AutomationRunRunning, types.AutomationRunAwaitingApproval,
	} {
		require.NoError(t, f.db.Model(run).Update("status", status).Error)
		list, err := f.svc.List(f.ctx)
		require.NoError(t, err)
		require.Len(t, list, 1)
		require.Equal(t, status, list[0].ActiveRunStatus)
		require.Equal(t, types.AutomationRunSucceeded, list[0].LastRunStatus)
		got, err := f.svc.Get(f.ctx, a.ID)
		require.NoError(t, err)
		require.Equal(t, status, got.ActiveRunStatus)
	}
	// Pausing the schedule does not mean its current execution has stopped.
	_, err = f.svc.Pause(f.ctx, a.ID)
	require.NoError(t, err)
	got, err := f.svc.Get(f.ctx, a.ID)
	require.NoError(t, err)
	require.Equal(t, types.AutomationRunAwaitingApproval, got.ActiveRunStatus)
	require.Empty(t, f.reload(t, a.ID).ActiveRunStatus, "the projection must not be persisted")

	require.NoError(t, f.db.Model(&types.AutomationRun{}).Where("automation_id = ?", a.ID).
		Update("status", types.AutomationRunSucceeded).Error)
	list, err := f.svc.List(f.ctx)
	require.NoError(t, err)
	require.Empty(t, list[0].ActiveRunStatus)
	require.Equal(t, types.AutomationRunSucceeded, list[0].LastRunStatus)

	other := types.WithCaller(f.ctx, types.Caller{TenantID: automationTenant, UserID: "other"})
	other = types.WithPrincipal(other, types.Principal{Type: types.PrincipalWebUser, ID: "other"})
	list, err = f.svc.List(other)
	require.NoError(t, err)
	require.Empty(t, list)
	_, err = f.svc.Get(other, a.ID)
	require.Error(t, err)
}
