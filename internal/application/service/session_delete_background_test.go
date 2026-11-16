package service

import (
	"context"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/background"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type recordingDeleteListener struct {
	deleted []interfaces.SessionRef
}

func (l *recordingDeleteListener) OnStop(context.Context, interfaces.SessionRef) error { return nil }

func (l *recordingDeleteListener) OnDelete(_ context.Context, s interfaces.SessionRef) error {
	l.deleted = append(l.deleted, s)
	return nil
}

func newDeleteBackgroundFixture(t *testing.T, ids ...string) (*sessionService, *recordingDeleteListener) {
	t.Helper()
	svc, db, _ := newSessionServiceForForkDeleteTest(t)
	work := NewBackgroundWork(background.NewMemoryActivityRegistry(nil), background.NewMemoryNoticeInbox(nil))
	listener := &recordingDeleteListener{}
	work.Listeners.Register(listener)
	svc.background = work
	for _, id := range ids {
		require.NoError(t, db.Session(&gorm.Session{SkipHooks: true}).Create(&types.Session{
			ID: id, TenantID: 1, UserID: "u1", Title: id,
		}).Error)
		ref := interfaces.SessionRef{TenantID: 1, SessionID: id}
		require.NoError(t, work.Registry.Add(context.Background(), ref, interfaces.BackgroundActivity{
			ID: "sj_" + id, Kind: "shell_job", StartedAt: time.Now(),
		}, time.Hour, 0))
	}
	return svc, listener
}

func requireNoBackgroundLeft(t *testing.T, svc *sessionService, id string) {
	t.Helper()
	live, err := svc.background.Registry.List(context.Background(), interfaces.SessionRef{TenantID: 1, SessionID: id})
	require.NoError(t, err)
	require.Empty(t, live)
}

func TestDeleteSessionNotifiesBackgroundListeners(t *testing.T) {
	svc, listener := newDeleteBackgroundFixture(t, "s1")

	require.NoError(t, svc.DeleteSession(testSessionScopeContext(1, "u1"), "s1"))

	require.Equal(t, []interfaces.SessionRef{{TenantID: 1, SessionID: "s1"}}, listener.deleted)
	requireNoBackgroundLeft(t, svc, "s1")
}

func TestBatchDeleteSessionsNotifiesBackgroundListeners(t *testing.T) {
	svc, listener := newDeleteBackgroundFixture(t, "s1", "s2")

	require.NoError(t, svc.BatchDeleteSessions(testSessionScopeContext(1, "u1"), []string{"s1", "s2"}))

	require.ElementsMatch(t, []interfaces.SessionRef{
		{TenantID: 1, SessionID: "s1"}, {TenantID: 1, SessionID: "s2"},
	}, listener.deleted)
	requireNoBackgroundLeft(t, svc, "s2")
}

func TestDeleteAllSessionsNotifiesBackgroundListeners(t *testing.T) {
	svc, listener := newDeleteBackgroundFixture(t, "s1", "s2")

	require.NoError(t, svc.DeleteAllSessions(testSessionScopeContext(1, "u1")))

	require.Len(t, listener.deleted, 2)
	requireNoBackgroundLeft(t, svc, "s1")
}
