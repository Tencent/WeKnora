package background

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

type countingKeepAliver struct{ calls int }

func (k *countingKeepAliver) KeepAlive(context.Context, interfaces.SessionRef) error {
	k.calls++
	return nil
}

type recordingEmitter struct{ finished []interfaces.WorkResult }

func (e *recordingEmitter) EmitWorkFinished(
	_ context.Context, _ interfaces.SessionRef, _ string, r interfaces.WorkResult,
) error {
	e.finished = append(e.finished, r)
	return nil
}

type lifecycleFixture struct {
	l        *Lifecycle
	registry *MemoryActivityRegistry
	inbox    *MemoryNoticeInbox
	keep     *countingKeepAliver
	events   *recordingEmitter
}

func newLifecycleFixture() lifecycleFixture {
	f := lifecycleFixture{
		registry: NewMemoryActivityRegistry(nil),
		inbox:    NewMemoryNoticeInbox(nil),
		keep:     &countingKeepAliver{},
		events:   &recordingEmitter{},
	}
	f.l = &Lifecycle{Registry: f.registry, Inbox: f.inbox, KeepAlive: f.keep, Events: f.events}
	return f
}

var lcSess = interfaces.SessionRef{TenantID: 1, SessionID: "s"}

func lcActivity(id string) interfaces.BackgroundActivity {
	return interfaces.BackgroundActivity{ID: id, Kind: "shell_job", Title: "make build", StartedAt: time.Now()}
}

func liveIDs(t *testing.T, r interfaces.ActivityRegistry) []string {
	t.Helper()
	items, err := r.List(context.Background(), lcSess)
	require.NoError(t, err)
	ids := make([]string, 0, len(items))
	for _, it := range items {
		ids = append(ids, it.ID)
	}
	return ids
}

func TestBeginRegistersBeforeStartAndKeepsAlive(t *testing.T) {
	f := newLifecycleFixture()
	var registeredAtStart []string
	err := f.l.Begin(context.Background(), lcSess, lcActivity("sj_1"), time.Minute, 0,
		func(context.Context) error {
			registeredAtStart = liveIDs(t, f.registry)
			return nil
		})
	require.NoError(t, err)
	require.Equal(t, []string{"sj_1"}, registeredAtStart)
	require.Equal(t, []string{"sj_1"}, liveIDs(t, f.registry))
	require.Equal(t, 1, f.keep.calls)
}

func TestBeginRemovesRegistrationWhenStartFails(t *testing.T) {
	f := newLifecycleFixture()
	boom := errors.New("start failed")
	err := f.l.Begin(context.Background(), lcSess, lcActivity("sj_1"), time.Minute, 0,
		func(context.Context) error { return boom })
	require.ErrorIs(t, err, boom)
	require.Empty(t, liveIDs(t, f.registry))
	require.Zero(t, f.keep.calls)
}

func TestBeginReturnsLimitWithoutStarting(t *testing.T) {
	f := newLifecycleFixture()
	require.NoError(t, f.l.Begin(context.Background(), lcSess, lcActivity("sj_1"), time.Minute, 1,
		func(context.Context) error { return nil }))
	started := false
	err := f.l.Begin(context.Background(), lcSess, lcActivity("sj_2"), time.Minute, 1,
		func(context.Context) error { started = true; return nil })
	require.ErrorIs(t, err, interfaces.ErrActivityLimit)
	require.False(t, started)
}

func TestAliveRenewsAndKeepsAliveOnlyWhenFound(t *testing.T) {
	f := newLifecycleFixture()
	require.NoError(t, f.registry.Add(context.Background(), lcSess, lcActivity("sj_1"), time.Minute, 0))

	found, err := f.l.Alive(context.Background(), lcSess, "sj_1", time.Minute)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, 1, f.keep.calls)

	found, err = f.l.Alive(context.Background(), lcSess, "missing", time.Minute)
	require.NoError(t, err)
	require.False(t, found)
	require.Equal(t, 1, f.keep.calls)
}

func TestFinishPushesEmitsOnceAndRemoves(t *testing.T) {
	f := newLifecycleFixture()
	ctx := context.Background()
	require.NoError(t, f.registry.Add(ctx, lcSess, lcActivity("sj_1"), time.Minute, 0))
	n := interfaces.BackgroundNotice{ID: "sj_1", Kind: "shell_job", Text: "done", FinishedAt: time.Now()}
	r := interfaces.WorkResult{ID: "sj_1", State: interfaces.WorkStateSucceeded, Terminal: true}

	require.NoError(t, f.l.Finish(ctx, lcSess, n, r, "msg-1"))
	require.NoError(t, f.l.Finish(ctx, lcSess, n, r, "msg-1")) // second reporter

	require.Len(t, f.events.finished, 1)
	require.Empty(t, liveIDs(t, f.registry))
	pending, err := f.inbox.Pending(ctx, lcSess)
	require.NoError(t, err)
	require.Len(t, pending, 1)
}

func TestFinishKeepsRegistrationWhenPushFails(t *testing.T) {
	f := newLifecycleFixture()
	ctx := context.Background()
	require.NoError(t, f.registry.Add(ctx, lcSess, lcActivity("sj_1"), time.Minute, 0))
	f.l.Inbox = failingInbox{}

	err := f.l.Finish(ctx, lcSess, interfaces.BackgroundNotice{ID: "sj_1"}, interfaces.WorkResult{ID: "sj_1"}, "")
	require.Error(t, err)
	require.Equal(t, []string{"sj_1"}, liveIDs(t, f.registry),
		"without a notice the item must stay visible, or queries would find it nowhere")
	require.Empty(t, f.events.finished)
}

func TestDiscardRemovesWithoutNoticeOrEvent(t *testing.T) {
	f := newLifecycleFixture()
	ctx := context.Background()
	require.NoError(t, f.registry.Add(ctx, lcSess, lcActivity("sj_1"), time.Minute, 0))

	require.NoError(t, f.l.Discard(ctx, lcSess, "sj_1"))

	require.Empty(t, liveIDs(t, f.registry))
	pending, err := f.inbox.Pending(ctx, lcSess)
	require.NoError(t, err)
	require.Empty(t, pending)
	require.Empty(t, f.events.finished)
}

func TestLifecycleToleratesNilOptionalParts(t *testing.T) {
	l := &Lifecycle{Registry: NewMemoryActivityRegistry(nil), Inbox: NewMemoryNoticeInbox(nil)}
	ctx := context.Background()
	require.NoError(t, l.Begin(ctx, lcSess, lcActivity("sj_1"), time.Minute, 0,
		func(context.Context) error { return nil }))
	_, err := l.Alive(ctx, lcSess, "sj_1", time.Minute)
	require.NoError(t, err)
	require.NoError(t, l.Finish(ctx, lcSess, interfaces.BackgroundNotice{ID: "sj_1", FinishedAt: time.Now()},
		interfaces.WorkResult{ID: "sj_1"}, ""))
}

type failingInbox struct{}

func (failingInbox) Push(context.Context, interfaces.SessionRef, interfaces.BackgroundNotice) (bool, error) {
	return false, errors.New("redis down")
}

func (failingInbox) Pending(context.Context, interfaces.SessionRef) ([]interfaces.BackgroundNotice, error) {
	return nil, nil
}

func (failingInbox) Ack(context.Context, interfaces.SessionRef, []string) error { return nil }

func (failingInbox) Clear(context.Context, interfaces.SessionRef) error { return nil }
