package browserskill

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestStopCancelsSessionCreation(t *testing.T) {
	m, ctx := sharedTestManager(t)
	s := Scope{1, "creating"}
	started := make(chan struct{})
	var cancelled atomic.Bool
	connectSharedFixture(ctx, t, m, s, "", "browser", func(_ *sharedFixture, _, method string, _ map[string]any) bool {
		if method == "tool.session_start" {
			close(started)
			return true
		}
		if method == "cancel" {
			cancelled.Store(true)
		}
		return false
	})
	done := make(chan error, 1)
	go func() { done <- m.Control(ctx, s, "chat", "start") }()
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	require.NoError(t, m.Control(ctx, s, "chat", "stop"))
	require.Error(t, <-done)
	require.True(t, cancelled.Load())
	require.False(t, m.Status(s, "chat").Selected)
}

func TestFailedStopRetainsPausedTaskForRetry(t *testing.T) {
	m, ctx := sharedTestManager(t)
	s := Scope{1, "returning"}
	var failed atomic.Bool
	connectSharedFixture(ctx, t, m, s, "", "browser", func(f *sharedFixture, id, method string, _ map[string]any) bool {
		if method != "tool.session_stop" || failed.Swap(true) {
			return false
		}
		_ = f.send(map[string]any{"id": id, "result": map[string]any{
			"returned_tab_ids": []int{},
			"return_failures":  []any{map[string]any{"tab_id": 1, "code": "cdp_failed", "message": "return failed"}},
		}})
		return true
	})
	require.NoError(t, m.Control(ctx, s, "chat", "start"))
	id := m.Status(s, "chat").SessionID
	require.Error(t, m.Control(ctx, s, "chat", "stop"))
	status := m.Status(s, "chat")
	require.True(t, status.Paused)
	require.False(t, status.Stopping)
	require.Equal(t, id, status.SessionID)
	require.NoError(t, m.Control(ctx, s, "chat", "stop"))
	require.False(t, m.Status(s, "chat").Selected)
}

func TestStopTimeoutLeavesTaskPausedAndRejectsResumeWhileStopping(t *testing.T) {
	m, ctx := sharedTestManager(t)
	s := Scope{1, "stopping"}
	started := make(chan struct{})
	connectSharedFixture(ctx, t, m, s, "", "browser", func(_ *sharedFixture, _, method string, _ map[string]any) bool {
		if method == "tool.session_stop" {
			close(started)
			return true
		}
		return false
	})
	require.NoError(t, m.Control(ctx, s, "chat", "start"))
	short, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- m.Control(short, s, "chat", "stop") }()
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	require.True(t, m.Status(s, "chat").Stopping)
	require.Error(t, m.Control(ctx, s, "chat", "resume"))
	_, err := m.Call(ctx, s, "chat", "snapshot", nil)
	require.Error(t, err)
	require.Error(t, <-done)
	require.True(t, m.Status(s, "chat").Paused)
	require.False(t, m.Status(s, "chat").Stopping)
}

func TestStopRecoversAfterDaemonAlreadyRemovedSession(t *testing.T) {
	m, ctx := sharedTestManager(t)
	s := Scope{1, "already-stopped"}
	connectSharedFixture(ctx, t, m, s, "", "browser")
	require.NoError(t, m.Control(ctx, s, "chat", "start"))
	id := m.Status(s, "chat").SessionID
	_, err := rpc(ctx, m.get(s), "session.stop", map[string]any{"session_id": id, "all": false})
	require.NoError(t, err)
	require.NoError(t, m.Control(ctx, s, "chat", "stop"))
	require.False(t, m.Status(s, "chat").Selected)
}
