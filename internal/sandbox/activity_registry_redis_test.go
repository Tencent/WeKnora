package sandbox

import (
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/background/backgroundtest"
	"github.com/Tencent/WeKnora/internal/sandbox/admission"
	"github.com/Tencent/WeKnora/internal/sandbox/allocation"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/alicebob/miniredis/v2"
	"github.com/stretchr/testify/require"
)

func TestRedisActivityRegistryContract(t *testing.T) {
	backgroundtest.RunActivityRegistryContract(t,
		func(t *testing.T) (interfaces.ActivityRegistry, backgroundtest.Advance) {
			store, _, mini := newRedisBindingTestStore(t)
			now := time.Date(2026, 10, 14, 10, 0, 0, 0, time.UTC)
			mini.SetTime(now)
			advance := func(d time.Duration) {
				now = now.Add(d)
				mini.SetTime(now)
				mini.FastForward(d) // also expires key TTLs
			}
			return NewRedisActivityRegistry(store), advance
		})
}

func TestRedisActivityRegistryUsesBindingHashTag(t *testing.T) {
	store, _, _ := newRedisBindingTestStore(t)
	key := SessionSandboxKey{TenantID: 7, SessionID: "s1"}
	require.Equal(t, "weknora:sandbox:session:{test:7:s1}:activity", store.activityKey(key))
	require.Equal(t, "weknora:sandbox:session:{test:7:s1}:turn", store.turnKey(key))
}

func TestRedisActivityRegistryRejectsInvalidSession(t *testing.T) {
	store, _, _ := newRedisBindingTestStore(t)
	r := NewRedisActivityRegistry(store)
	err := r.Add(t.Context(), interfaces.SessionRef{}, interfaces.BackgroundActivity{ID: "x"}, time.Minute, 0)
	require.Error(t, err)
}

// These managers use separate stores on the same Redis, as two replicas do.
func newSharedActivityManagers(
	t *testing.T,
) (*userHarness, *userHarness, *RedisActivityRegistry, *miniredis.Miniredis) {
	t.Helper()
	store, client, mini := newRedisBindingTestStore(t)
	other, err := NewRedisSessionSandboxBindingStore(client, store.namespace)
	require.NoError(t, err)
	allocs := newFakeAllocations(userAssignmentFor(userSessionA), userAssignmentFor(userSessionB))
	remote := newFakeRemoteClient(SandboxTypeCube)
	h1 := newUserManagerHarnessWithDirectory(t, remote, store,
		admission.NewRedis(client, "weknora:sandbox:user:"), allocs, store.namespace)
	h2 := newUserManagerHarnessWithDirectory(t, remote, other,
		admission.NewRedis(client, "weknora:sandbox:user:"), allocs, store.namespace)
	return h1, h2, NewRedisActivityRegistry(store).WithAllocations(allocs), mini
}

func useActivityTestSandbox(t *testing.T, mgr *SessionBoundManager, sessionID string) string {
	t.Helper()
	ctx, end, err := mgr.BeginSessionTurn(ownerCtx(), sessionID)
	require.NoError(t, err)
	defer end()
	_, err = mgr.ExecShellCommand(ctx, sessionID, "true", "", time.Second, nil)
	require.NoError(t, err)
	id, bound := mgr.BoundSandboxID(ctx, sessionID)
	require.True(t, bound)
	return id
}

func TestSharedMachineKeepsSandboxAfterBackgroundActivityEnds(t *testing.T) {
	for _, release := range []string{"remove", "expire"} {
		t.Run(release, func(t *testing.T) {
			h1, h2, registry, mini := newSharedActivityManagers(t)
			now := time.Now()
			mini.SetTime(now)
			ctx := ownerCtx()
			first := useActivityTestSandbox(t, h1.mgr, userSessionA)
			ref := interfaces.SessionRef{TenantID: 42, SessionID: userSessionA}
			require.NoError(t, registry.Add(ctx, ref, interfaces.BackgroundActivity{ID: "sj_1"}, time.Minute, 0))
			n, err := h1.mgr.InvalidateConfigSandboxesForMachines(ctx, "cfg-1",
				[]allocation.UserMachine{userAssignmentFor(userSessionA).Machine})
			require.NoError(t, err)
			require.Equal(t, 1, n)

			require.Equal(t, first, useActivityTestSandbox(t, h2.mgr, userSessionB),
				"another replica's sibling turn must not destroy the background job's machine")
			require.Equal(t, first, useActivityTestSandbox(t, h2.mgr, userSessionA),
				"the submitting session's next turn must also keep its machine")
			require.Empty(t, h1.client.deleteIDs)

			if release == "remove" {
				require.NoError(t, registry.Remove(ctx, ref, "sj_1"))
			} else {
				mini.SetTime(now.Add(time.Minute))
				mini.FastForward(time.Minute)
			}
			require.Equal(t, first, useActivityTestSandbox(t, h2.mgr, userSessionB),
				"ending background work must not trigger a deferred rebuild")
			require.Empty(t, h1.client.deleteIDs)
		})
	}
}

func TestRedisActivityCannotRegisterThroughClosedAdmission(t *testing.T) {
	for _, scopeKind := range []string{"machine", "session", "rewind"} {
		t.Run(scopeKind, func(t *testing.T) {
			h, _, registry, _ := newSharedActivityManagers(t)
			ctx := ownerCtx()
			ref := interfaces.SessionRef{TenantID: 42, SessionID: userSessionA}
			key, err := registry.key(ctx, ref)
			require.NoError(t, err)
			want := ErrSessionWorkspaceBusy
			if scopeKind == "rewind" {
				unlock, err := registry.store.TryLockRewind(ctx, key)
				require.NoError(t, err)
				defer unlock()
				want = ErrSessionRewindLocked
			} else {
				digest := key.User.Digest(registry.store.namespace)
				scope := admission.MachineScope(digest)
				if scopeKind == "session" {
					scope = admission.SessionScope(digest, ref.SessionID)
				}
				barrier, err := h.mgr.admission.Close(ctx, scope, time.Minute)
				require.NoError(t, err)
				defer barrier.Release()
			}
			require.ErrorIs(t, registry.Add(ctx, ref, interfaces.BackgroundActivity{ID: "sj_1"}, time.Minute, 0), want)
			items, err := registry.List(ctx, ref)
			require.NoError(t, err)
			require.Empty(t, items)
			active, err := registry.store.hasBackgroundActivity(ctx, key)
			require.NoError(t, err)
			require.False(t, active)
		})
	}
}

func TestRedisSharedActivityLeasesRemainSessionScoped(t *testing.T) {
	_, _, registry, mini := newSharedActivityManagers(t)
	ctx := ownerCtx()
	now := time.Now()
	mini.SetTime(now)
	a := interfaces.SessionRef{TenantID: 42, SessionID: userSessionA}
	b := interfaces.SessionRef{TenantID: 42, SessionID: userSessionB}
	key, err := registry.key(ctx, a)
	require.NoError(t, err)
	item := interfaces.BackgroundActivity{ID: "sj_1", Kind: "shell_job"}
	require.NoError(t, registry.Add(ctx, a, item, 10*time.Second, 1))
	items, err := registry.List(ctx, b)
	require.NoError(t, err)
	require.Empty(t, items)
	// Caps and IDs are session scoped even on the same machine.
	require.NoError(t, registry.Add(ctx, b, item, 10*time.Second, 1))
	_, err = registry.store.TryLockRewind(ctx, key)
	require.ErrorIs(t, err, ErrSessionWorkActive)
	require.NoError(t, registry.Remove(ctx, a, item.ID))
	unlock, err := registry.store.TryLockRewind(ctx, key)
	require.NoError(t, err, "a sibling's work must not block this session's rewind")
	unlock()
	mini.SetTime(now.Add(8 * time.Second))
	mini.FastForward(8 * time.Second)
	found, err := registry.Renew(ctx, b, item.ID, time.Minute)
	require.NoError(t, err)
	require.True(t, found)
	mini.SetTime(now.Add(20 * time.Second))
	mini.FastForward(12 * time.Second)
	active, err := registry.store.hasBackgroundActivity(ctx, key)
	require.NoError(t, err)
	require.True(t, active, "renew must extend the shared machine guard too")
	require.NoError(t, registry.Remove(ctx, b, item.ID))
	active, err = registry.store.hasBackgroundActivity(ctx, key)
	require.NoError(t, err)
	require.False(t, active)
}
