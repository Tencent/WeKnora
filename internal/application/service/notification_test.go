package service

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestNotificationHistoryCursorAndOwnerIsolation(t *testing.T) {
	f := newAutomationFixture(t)
	svc := NewNotificationService(f.db)
	svc.now = func() time.Time { return f.now }
	for i := 0; i < 25; i++ {
		svc.Notify(f.ctx, &types.Notification{
			ID: fmt.Sprintf("n-%02d", i), TenantID: automationTenant, UserID: "u",
			Kind: types.NotificationAutomationSucceeded, Title: "done", CreatedAt: f.now,
		})
	}
	svc.Notify(f.ctx, &types.Notification{
		TenantID: automationTenant, UserID: "other",
		Kind: types.NotificationAutomationFailed, Title: "private", CreatedAt: f.now,
	})
	svc.Notify(f.ctx, &types.Notification{
		TenantID: automationTenant + 1, UserID: "u",
		Kind: types.NotificationAutomationFailed, Title: "other tenant", CreatedAt: f.now,
	})
	page, err := svc.List(f.ctx, 20, false, "")
	require.NoError(t, err)
	require.Len(t, page.Items, 20)
	require.EqualValues(t, 25, page.Unread)
	require.Equal(t, "n-24", page.Items[0].ID)
	require.NotEmpty(t, page.NextCursor)
	// A new arrival and a deleted boundary row must not shift the second page.
	svc.Notify(f.ctx, &types.Notification{
		TenantID: automationTenant, UserID: "u",
		Kind: types.NotificationAutomationSucceeded, Title: "new", CreatedAt: f.now.Add(time.Minute),
	})
	require.NoError(t, f.db.Delete(&types.Notification{}, "id = ?", page.Items[19].ID).Error)
	next, err := svc.List(f.ctx, 20, false, page.NextCursor)
	require.NoError(t, err)
	require.Len(t, next.Items, 5)
	require.Equal(t, "n-04", next.Items[0].ID)
	require.Equal(t, "n-00", next.Items[4].ID)
	require.Empty(t, next.NextCursor)
	_, err = svc.List(f.ctx, 20, false, "broken")
	require.Error(t, err)
}

func TestNotificationPendingUsesLivePromptsNotReadHistory(t *testing.T) {
	f := newAutomationFixture(t)
	svc := NewNotificationService(f.db)
	svc.now = func() time.Time { return f.now }
	a := f.create(t, types.AutomationPatch{})
	prompt := &types.AgentPrompt{
		ID: "prompt", TenantID: automationTenant, UserID: automationGrantUser("u"),
		SessionID: "chat", Kind: types.AgentPromptKindToolApproval,
		Status: types.AgentPromptSuspended, CreatedAt: f.now,
	}
	require.NoError(t, f.db.Create(prompt).Error)
	run := &types.AutomationRun{
		ID: "run", AutomationID: a.ID, TenantID: automationTenant, SessionID: "chat", PendingPromptID: prompt.ID,
		Status: types.AutomationRunAwaitingApproval, ScheduledAt: f.now, UpdatedAt: f.now,
	}
	require.NoError(t, f.db.Create(run).Error)
	// Pending remains visible even without a notification, and beyond 20 results.
	for i := 0; i < 30; i++ {
		svc.Notify(f.ctx, &types.Notification{
			TenantID: automationTenant, UserID: "u",
			Kind: types.NotificationAutomationSucceeded, Title: "done", CreatedAt: f.now.Add(time.Minute),
		})
	}
	page, err := svc.List(f.ctx, 20, false, "")
	require.NoError(t, err)
	require.Len(t, page.Pending, 1)
	require.Equal(t, prompt.ID, page.Pending[0].ID)
	require.Equal(t, "/platform/chat/chat", page.Pending[0].Link)
	require.EqualValues(t, 30, page.Unread)
	approval := &types.Notification{
		TenantID: automationTenant, UserID: "u",
		Kind: types.NotificationAutomationAwaitingApproval, Title: "approval", ReadAt: &f.now,
	}
	svc.Notify(f.ctx, approval)
	require.NoError(t, svc.MarkRead(f.ctx, nil))
	require.NoError(t, svc.ClearRead(f.ctx))
	page, err = svc.List(f.ctx, 20, false, "")
	require.NoError(t, err)
	require.Empty(t, page.Items)
	require.Zero(t, page.Unread)
	require.Len(t, page.Pending, 1, "mark all read and clear history must never dismiss a pending prompt")
	require.NoError(t, f.db.First(&types.Notification{}, "id = ?", approval.ID).Error)

	other := types.WithCaller(context.Background(), types.Caller{TenantID: automationTenant, UserID: "other"})
	otherPage, err := svc.List(other, 20, false, "")
	require.NoError(t, err)
	require.Empty(t, otherPage.Pending)
	other = types.WithCaller(context.Background(), types.Caller{TenantID: automationTenant + 1, UserID: "u"})
	otherPage, err = svc.List(other, 20, false, "")
	require.NoError(t, err)
	require.Empty(t, otherPage.Pending)

	assertPending := func(want int) {
		t.Helper()
		page, err := svc.List(f.ctx, 20, false, "")
		require.NoError(t, err)
		require.Len(t, page.Pending, want)
	}
	for _, status := range []string{
		types.AgentPromptAnswered, types.AgentPromptExecuting, types.AgentPromptDone,
		types.AgentPromptCanceled, types.AgentPromptExpired, types.AgentPromptSuperseded,
	} {
		require.NoError(t, f.db.Model(prompt).Update("status", status).Error)
		assertPending(0)
	}
	require.NoError(t, f.db.Model(prompt).Update("status", types.AgentPromptSuspended).Error)
	assertPending(1)
	require.NoError(t, f.db.Model(prompt).Update("expires_at", f.now).Error)
	assertPending(0)
	require.NoError(t, f.db.Model(prompt).Update("expires_at", nil).Error)
	require.NoError(t, f.db.Model(run).Update("cancel_requested", true).Error)
	assertPending(0)
	require.NoError(t, f.db.Model(run).Update("cancel_requested", false).Error)
	require.NoError(t, f.db.Model(run).Update("status", types.AutomationRunSucceeded).Error)
	assertPending(0)
	require.NoError(t, f.db.Model(run).Update("status", types.AutomationRunAwaitingApproval).Error)
	timedOutAt := f.now.Add(-time.Duration(a.ApprovalTimeoutMinutes) * time.Minute)
	require.NoError(t, f.db.Model(run).Update("updated_at", timedOutAt).Error)
	assertPending(0)
	require.NoError(t, f.db.Model(run).Update("updated_at", f.now).Error)
	assertPending(1)
	require.NoError(t, f.db.Delete(a).Error)
	assertPending(0)
}

func TestNotificationClearReadOnlyTouchesCallersReadHistory(t *testing.T) {
	f := newAutomationFixture(t)
	svc := NewNotificationService(f.db)
	svc.now = func() time.Time { return f.now }
	rows := []*types.Notification{
		{ID: "read", TenantID: automationTenant, UserID: "u", ReadAt: &f.now},
		{ID: "unread", TenantID: automationTenant, UserID: "u"},
		{ID: "other-user", TenantID: automationTenant, UserID: "other", ReadAt: &f.now},
		{ID: "other-tenant", TenantID: automationTenant + 1, UserID: "u", ReadAt: &f.now},
	}
	for _, row := range rows {
		row.Kind = types.NotificationAutomationSucceeded
		svc.Notify(f.ctx, row)
	}
	require.NoError(t, svc.ClearRead(f.ctx))
	var remaining []types.Notification
	require.NoError(t, f.db.Order("id").Find(&remaining).Error)
	require.Len(t, remaining, 3)
	require.Equal(t, "other-tenant", remaining[0].ID)
	require.Equal(t, "other-user", remaining[1].ID)
	require.Equal(t, "unread", remaining[2].ID)
	require.NoError(t, svc.MarkRead(f.ctx, []string{"unread", "other-user"}))
	page, err := svc.List(f.ctx, 20, true, "")
	require.NoError(t, err)
	require.Empty(t, page.Items)
	require.EqualValues(t, 1, page.ReadCount)
	require.Zero(t, page.Unread)
}
