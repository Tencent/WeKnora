package session

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/application/service"
	"github.com/Tencent/WeKnora/internal/background"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

type recordingSessionListener struct {
	stopped []interfaces.SessionRef
}

func (l *recordingSessionListener) OnStop(_ context.Context, s interfaces.SessionRef) error {
	l.stopped = append(l.stopped, s)
	return nil
}

func (l *recordingSessionListener) OnDelete(context.Context, interfaces.SessionRef) error { return nil }

func newAckFixture(t *testing.T) (*service.BackgroundWork, interfaces.SessionRef) {
	t.Helper()
	work := service.NewBackgroundWork(background.NewMemoryActivityRegistry(nil), background.NewMemoryNoticeInbox(nil))
	ref := interfaces.SessionRef{TenantID: 1, SessionID: "s"}
	for _, id := range []string{"sj_1", "sj_2", "sj_3"} {
		_, err := work.Inbox.Push(context.Background(), ref, interfaces.BackgroundNotice{
			ID: id, Kind: "shell_job", Text: id, FinishedAt: time.Now(),
		})
		require.NoError(t, err)
	}
	return work, ref
}

func messageCarryingNotices() *types.Message {
	return &types.Message{
		ID: "m", SessionID: "s", Role: "assistant", Content: "done",
		AgentSteps: []types.AgentStep{
			{BackgroundInjections: []types.BackgroundInjection{{Content: "x", AckIDs: []string{"sj_1"}}}},
			{BackgroundSeenIDs: []string{"sj_2", "sj_1"}},
		},
	}
}

func pendingIDs(t *testing.T, work *service.BackgroundWork, ref interfaces.SessionRef) []string {
	t.Helper()
	notices, err := work.Inbox.Pending(context.Background(), ref)
	require.NoError(t, err)
	ids := make([]string, 0, len(notices))
	for _, n := range notices {
		ids = append(ids, n.ID)
	}
	return ids
}

func TestPersistedMessageAcknowledgesTheNoticesItCarries(t *testing.T) {
	work, ref := newAckFixture(t)
	h := &Handler{messageService: &imageCompletionMessages{}, background: work}
	ctx := types.WithExecutionTenant(context.Background(), 1)

	require.NoError(t, h.completeAssistantMessage(ctx, messageCarryingNotices(), "", ""))

	require.ElementsMatch(t, []string{"sj_3"}, pendingIDs(t, work, ref))
}

func TestFailedPersistLeavesNoticesForTheNextRun(t *testing.T) {
	work, ref := newAckFixture(t)
	h := &Handler{messageService: &imageCompletionMessages{err: errors.New("db down")}, background: work}
	ctx := types.WithExecutionTenant(context.Background(), 1)

	require.Error(t, h.completeAssistantMessage(ctx, messageCarryingNotices(), "", ""))

	require.ElementsMatch(t, []string{"sj_1", "sj_2", "sj_3"}, pendingIDs(t, work, ref))
}

func TestBackgroundNoticeIDsAreDeduplicatedInOrder(t *testing.T) {
	require.Equal(t, []string{"sj_1", "sj_2"}, backgroundNoticeIDs(messageCarryingNotices().AgentSteps))
	require.Empty(t, backgroundNoticeIDs(nil))
}

func TestStopNotifiesBackgroundListeners(t *testing.T) {
	work := service.NewBackgroundWork(nil, nil)
	listener := &recordingSessionListener{}
	work.Listeners.Register(listener)
	h := &Handler{background: work}

	h.notifyBackgroundStop(context.Background(), &types.Session{ID: "s", TenantID: 3})
	(&Handler{}).notifyBackgroundStop(context.Background(), &types.Session{ID: "s", TenantID: 3})

	require.Equal(t, []interfaces.SessionRef{{TenantID: 3, SessionID: "s"}}, listener.stopped)
}
