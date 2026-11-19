package sandbox

import (
	"context"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

// addLiveActivity registers one background item for key through the real
// Redis registry, so the turn-lease scripts read exactly what features write.
func addLiveActivity(t *testing.T, store *RedisSessionSandboxBindingStore, key SessionSandboxKey, lease time.Duration) {
	t.Helper()
	reg := NewRedisActivityRegistry(store)
	ref := interfaces.SessionRef{TenantID: key.TenantID, SessionID: key.SessionID}
	require.NoError(t, reg.Add(context.Background(), ref, interfaces.BackgroundActivity{
		ID: "sj_1", Kind: "shell_job", Title: "build", StartedAt: time.Now(),
	}, lease, 0))
}

func TestRedisTurnStateCountsLiveBackgroundWorkAsActiveWithoutRebuild(t *testing.T) {
	store, _, mini := newRedisBindingTestStore(t)
	ctx := context.Background()
	key := SessionSandboxKey{TenantID: 42, SessionID: "session-bg"}
	now := time.Date(2026, 10, 15, 10, 0, 0, 0, time.UTC)
	mini.SetTime(now)

	addLiveActivity(t, store, key, 30*time.Second)

	active, rebuildOnce, err := store.TurnState(ctx, key)
	require.NoError(t, err)
	require.True(t, active, "live background work must keep a turn-less resolve from rebuilding")
	require.False(t, rebuildOnce)

	mini.SetTime(now.Add(31 * time.Second))
	active, rebuildOnce, err = store.TurnState(ctx, key)
	require.NoError(t, err)
	require.False(t, active, "an expired lease no longer protects the sandbox")
	require.False(t, rebuildOnce)
}

func TestRedisBeginTurnDoesNotArmRebuildWhileBackgroundWorkIsLive(t *testing.T) {
	store, _, mini := newRedisBindingTestStore(t)
	ctx := context.Background()
	key := SessionSandboxKey{TenantID: 42, SessionID: "session-bg-turn"}
	now := time.Date(2026, 10, 15, 10, 0, 0, 0, time.UTC)
	mini.SetTime(now)

	addLiveActivity(t, store, key, 30*time.Second)
	require.NoError(t, store.BeginTurn(ctx, key))

	active, rebuildOnce, err := store.TurnState(ctx, key)
	require.NoError(t, err)
	require.True(t, active)
	require.False(t, rebuildOnce)

	// The work ends mid-turn. The turn still must not rebuild: its first
	// resolve already ran against this sandbox.
	mini.SetTime(now.Add(31 * time.Second))
	active, rebuildOnce, err = store.TurnState(ctx, key)
	require.NoError(t, err)
	require.True(t, active)
	require.False(t, rebuildOnce)
	require.NoError(t, store.EndTurn(ctx, key))
}

func TestRedisTryLockRewindFailsWhileBackgroundWorkIsLive(t *testing.T) {
	store, _, mini := newRedisBindingTestStore(t)
	ctx := context.Background()
	key := SessionSandboxKey{TenantID: 42, SessionID: "session-bg-rewind"}
	now := time.Date(2026, 10, 15, 10, 0, 0, 0, time.UTC)
	mini.SetTime(now)

	addLiveActivity(t, store, key, 30*time.Second)
	_, err := store.TryLockRewind(ctx, key)
	require.ErrorIs(t, err, ErrSessionWorkActive)
	held, err := store.HasRewindLock(ctx, key)
	require.NoError(t, err)
	require.False(t, held, "a refused rewind must not leave its lock behind")

	mini.SetTime(now.Add(31 * time.Second))
	unlock, err := store.TryLockRewind(ctx, key)
	require.NoError(t, err)
	unlock()
}
