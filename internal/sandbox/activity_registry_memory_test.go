package sandbox

import (
	"sync"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/background"
	"github.com/Tencent/WeKnora/internal/background/backgroundtest"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

type activityTestClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *activityTestClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *activityTestClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

func TestBindingMemoryActivityRegistryContract(t *testing.T) {
	backgroundtest.RunActivityRegistryContract(t,
		func(_ *testing.T) (interfaces.ActivityRegistry, backgroundtest.Advance) {
			store := NewMemorySessionSandboxBindingStore()
			clock := &activityTestClock{now: time.Now()}
			store.activity = background.NewMemoryActivityRegistry(clock)
			return NewMemoryActivityRegistry(store), clock.advance
		})
}

func TestMemoryActivityProtectsStaleSandboxAcrossTurns(t *testing.T) {
	for _, release := range []string{"remove", "expire"} {
		t.Run(release, func(t *testing.T) {
			ctx := t.Context()
			fx := newLifecycleFixture(t)
			clock := &activityTestClock{now: time.Now()}
			fx.bindings.activity = background.NewMemoryActivityRegistry(clock)
			registry := NewMemoryActivityRegistry(fx.bindings)
			ref := interfaces.SessionRef{TenantID: fx.key.TenantID, SessionID: fx.key.SessionID}
			first, err := fx.lifecycle.Resolve(ctx, fx.key)
			require.NoError(t, err)
			require.NoError(t, registry.Add(ctx, ref, interfaces.BackgroundActivity{ID: "sj_1"}, time.Minute, 0))
			_, err = fx.bindings.InvalidateByConfig(ctx, fx.key.TenantID, "cfg-1")
			require.NoError(t, err)

			active, rebuild, err := fx.bindings.TurnState(ctx, fx.key)
			require.NoError(t, err)
			require.True(t, active, "background work stays active after the submitting turn")
			require.False(t, rebuild)
			require.NoError(t, fx.bindings.BeginTurn(ctx, fx.key))
			kept, err := fx.lifecycle.Resolve(ctx, fx.key)
			require.NoError(t, err)
			require.Equal(t, first.ID(), kept.ID())
			require.Empty(t, fx.client.deleteIDs)
			require.NoError(t, fx.bindings.EndTurn(ctx, fx.key))

			if release == "remove" {
				require.NoError(t, registry.Remove(ctx, ref, "sj_1"))
			} else {
				clock.advance(time.Minute)
			}
			require.NoError(t, fx.bindings.BeginTurn(ctx, fx.key))
			replaced, err := fx.lifecycle.Resolve(ctx, fx.key)
			require.NoError(t, err)
			require.NotEqual(t, first.ID(), replaced.ID(), "completed or expired work must release the rebuild guard")
			require.Equal(t, []string{first.ID()}, fx.client.deleteIDs)
			require.NoError(t, fx.bindings.EndTurn(ctx, fx.key))
		})
	}
}

func TestMemoryActivityAndRewindAreExclusive(t *testing.T) {
	ctx := t.Context()
	store := NewMemorySessionSandboxBindingStore()
	registry := NewMemoryActivityRegistry(store)
	key := SessionSandboxKey{TenantID: 42, SessionID: "session-a"}
	ref := interfaces.SessionRef{TenantID: key.TenantID, SessionID: key.SessionID}
	item := interfaces.BackgroundActivity{ID: "sj_1"}
	require.NoError(t, registry.Add(ctx, ref, item, time.Minute, 0))
	_, err := store.TryLockRewind(ctx, key)
	require.ErrorIs(t, err, ErrSessionWorkActive)
	require.NoError(t, registry.Remove(ctx, ref, item.ID))
	unlock, err := store.TryLockRewind(ctx, key)
	require.NoError(t, err)
	defer unlock()
	require.ErrorIs(t, registry.Add(ctx, ref, item, time.Minute, 0), ErrSessionRewindLocked)
	items, err := registry.List(ctx, ref)
	require.NoError(t, err)
	require.Empty(t, items)
}
