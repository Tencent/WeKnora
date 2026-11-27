package service

import (
	"context"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

// The scheduler runs on context.Background() (see container.go), so closing
// an abandoned run's message must not rely on a tenant already being in ctx.
func TestAutomationAbandonedRunClosesMessageWithoutTenantContext(t *testing.T) {
	f := newAutomationFixture(t)
	require.NoError(t, f.db.AutoMigrate(
		&types.Message{}, &types.MessageArtifactRecord{}, &testListSessionsIMChannelSession{},
	))
	f.svc.messages = NewMessageService(
		repository.NewMessageRepository(f.db), repository.NewSessionRepository(f.db),
		nil, nil, nil, nil, nil, nil,
	)

	a := f.create(t, types.AutomationPatch{})
	session := &types.Session{ID: "s-abandoned", TenantID: automationTenant, UserID: "u"}
	require.NoError(t, f.db.Create(session).Error)
	msg := &types.Message{
		ID: "m-abandoned", SessionID: session.ID, Role: "assistant", RequestID: "r",
		Content: "partial answer",
	}
	require.NoError(t, f.db.Create(msg).Error)

	past := f.now.Add(-time.Minute)
	run := &types.AutomationRun{
		AutomationID: a.ID, TenantID: automationTenant, ScheduledAt: past, TriggerKind: types.AutomationRunSchedule,
		Status: types.AutomationRunRunning, Owner: "dead", LeaseUntil: &past,
		SessionID: session.ID, AssistantMessageID: msg.ID,
	}
	require.NoError(t, f.db.Create(run).Error)

	require.NotPanics(t, func() {
		require.NoError(t, f.svc.expireLeases(context.Background(), f.now))
	})
	require.Equal(t, types.AutomationRunNeedsAttention, f.runs(t, a.ID)[0].Status)

	var closed types.Message
	require.NoError(t, f.db.First(&closed, "id = ?", msg.ID).Error)
	require.True(t, closed.IsCompleted)
	require.Equal(t, "partial answer", closed.Content)
}
