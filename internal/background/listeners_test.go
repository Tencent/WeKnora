package background

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

type recordingListener struct {
	name string
	log  *[]string
	err  error
}

func (l recordingListener) OnStop(context.Context, interfaces.SessionRef) error {
	*l.log = append(*l.log, l.name+":stop")
	return l.err
}

func (l recordingListener) OnDelete(context.Context, interfaces.SessionRef) error {
	*l.log = append(*l.log, l.name+":delete")
	return l.err
}

func TestListenersRunInOrderAndSurviveErrors(t *testing.T) {
	var log []string
	var warnings []string
	logf := func(_ context.Context, format string, args ...any) {
		warnings = append(warnings, fmt.Sprintf(format, args...))
	}
	l := NewListeners(NewMemoryActivityRegistry(nil), NewMemoryNoticeInbox(nil), logf)
	l.Register(recordingListener{name: "a", log: &log, err: errors.New("a failed")})
	l.Register(recordingListener{name: "b", log: &log})

	s := interfaces.SessionRef{TenantID: 1, SessionID: "s"}
	l.NotifyStop(context.Background(), s)
	require.ErrorContains(t, l.NotifyDelete(context.Background(), s), "a failed")

	require.Equal(t, []string{"a:stop", "b:stop", "a:delete", "b:delete"}, log)
	require.Len(t, warnings, 2)
}

func TestNotifyDeleteClearsSharedStateLast(t *testing.T) {
	ctx := context.Background()
	s := interfaces.SessionRef{TenantID: 1, SessionID: "s"}
	registry := NewMemoryActivityRegistry(nil)
	inbox := NewMemoryNoticeInbox(nil)
	require.NoError(t, registry.Add(ctx, s, interfaces.BackgroundActivity{ID: "sj_1", Kind: "k"}, time.Hour, 0))
	_, err := inbox.Push(ctx, s, interfaces.BackgroundNotice{ID: "sj_0", FinishedAt: time.Now()})
	require.NoError(t, err)

	var sawDuringListener int
	l := NewListeners(registry, inbox, nil)
	l.Register(listenerFunc(func(ctx context.Context, s interfaces.SessionRef) {
		items, _ := registry.List(ctx, s)
		sawDuringListener = len(items)
	}))

	require.NoError(t, l.NotifyDelete(ctx, s))

	require.Equal(t, 1, sawDuringListener, "feature listeners must run before shared state is cleared")
	items, err := registry.List(ctx, s)
	require.NoError(t, err)
	require.Empty(t, items)
	pending, err := inbox.Pending(ctx, s)
	require.NoError(t, err)
	require.Empty(t, pending)
}

func TestNotifyStopKeepsSharedState(t *testing.T) {
	ctx := context.Background()
	s := interfaces.SessionRef{TenantID: 1, SessionID: "s"}
	registry := NewMemoryActivityRegistry(nil)
	require.NoError(t, registry.Add(ctx, s, interfaces.BackgroundActivity{ID: "sj_1", Kind: "k"}, time.Hour, 0))

	NewListeners(registry, NewMemoryNoticeInbox(nil), nil).NotifyStop(ctx, s)

	items, err := registry.List(ctx, s)
	require.NoError(t, err)
	require.Len(t, items, 1)
}

type listenerFunc func(ctx context.Context, s interfaces.SessionRef)

func (f listenerFunc) OnStop(context.Context, interfaces.SessionRef) error { return nil }

func (f listenerFunc) OnDelete(ctx context.Context, s interfaces.SessionRef) error {
	f(ctx, s)
	return nil
}
