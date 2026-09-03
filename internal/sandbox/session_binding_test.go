package sandbox

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func validSessionSandboxBinding(key SessionSandboxKey, sandboxID string) SessionSandboxBinding {
	return SessionSandboxBinding{
		Version:    SessionSandboxBindingVersion,
		Provider:   SandboxTypeCube,
		TenantID:   key.TenantID,
		SessionID:  key.SessionID,
		SandboxID:  sandboxID,
		TemplateID: "template-a",
		CreatedAt:  time.Unix(100, 0).UTC(),
	}
}

func testSessionSandboxBindingStore(t *testing.T, store SessionSandboxBindingStore) {
	t.Helper()

	ctx := context.Background()
	key := SessionSandboxKey{TenantID: 42, SessionID: "session-a"}
	first := validSessionSandboxBinding(key, "sandbox-a")

	got, err := store.Get(ctx, key)
	require.NoError(t, err)
	require.Nil(t, got)

	created, err := store.Create(ctx, key, first)
	require.NoError(t, err)
	require.True(t, created)

	got, err = store.Get(ctx, key)
	require.NoError(t, err)
	require.Equal(t, &first, got)

	created, err = store.Create(ctx, key, validSessionSandboxBinding(key, "sandbox-b"))
	require.NoError(t, err)
	require.False(t, created)

	deleted, err := store.DeleteIfMatch(ctx, key, SandboxTypeE2B, "sandbox-a")
	require.NoError(t, err)
	require.False(t, deleted)
	deleted, err = store.DeleteIfMatch(ctx, key, SandboxTypeCube, "sandbox-b")
	require.NoError(t, err)
	require.False(t, deleted)
	deleted, err = store.DeleteIfMatch(ctx, key, SandboxTypeCube, "sandbox-a")
	require.NoError(t, err)
	require.True(t, deleted)
}

func TestMemorySessionSandboxBindingStoreReplacesTrafficToken(t *testing.T) {
	store := NewMemorySessionSandboxBindingStore()
	testSessionSandboxBindingReplacesTrafficToken(t, store)
}

func testSessionSandboxBindingReplacesTrafficToken(t *testing.T, store SessionSandboxBindingStore) {
	t.Helper()

	ctx := context.Background()
	key := SessionSandboxKey{TenantID: 42, SessionID: "session-token"}
	binding := validSessionSandboxBinding(key, "sandbox-a")
	binding.TrafficAccessToken = "old-token"
	binding.ConfigID = "cfg-token"
	created, err := store.Create(ctx, key, binding)
	require.NoError(t, err)
	require.True(t, created)

	marked, err := store.InvalidateByConfig(ctx, key.TenantID, binding.ConfigID)
	require.NoError(t, err)
	require.Equal(t, 1, marked)

	wrote, err := store.ReplaceTrafficTokenIfMatch(ctx, key, binding, "new-token")
	require.NoError(t, err)
	require.True(t, wrote)

	got, err := store.Get(ctx, key)
	require.NoError(t, err)
	require.Equal(t, "new-token", got.TrafficAccessToken)
	require.Equal(t, binding.SandboxID, got.SandboxID)
	require.NotNil(t, got.StaleAt, "patching the token must not wipe a concurrent stale mark")

	mismatch := binding
	mismatch.SandboxID = "sandbox-other"
	wrote, err = store.ReplaceTrafficTokenIfMatch(ctx, key, mismatch, "ignored")
	require.NoError(t, err)
	require.False(t, wrote)
	got, err = store.Get(ctx, key)
	require.NoError(t, err)
	require.Equal(t, "new-token", got.TrafficAccessToken)

	wrote, err = store.ReplaceTrafficTokenIfMatch(ctx, key, binding, "")
	require.NoError(t, err)
	require.False(t, wrote)
}

func testSessionSandboxBindingTenantIsolation(t *testing.T, store SessionSandboxBindingStore) {
	t.Helper()

	ctx := context.Background()
	firstKey := SessionSandboxKey{TenantID: 42, SessionID: "shared-session"}
	secondKey := SessionSandboxKey{TenantID: 43, SessionID: "shared-session"}

	created, err := store.Create(ctx, firstKey, validSessionSandboxBinding(firstKey, "sandbox-a"))
	require.NoError(t, err)
	require.True(t, created)
	created, err = store.Create(ctx, secondKey, validSessionSandboxBinding(secondKey, "sandbox-b"))
	require.NoError(t, err)
	require.True(t, created)

	first, err := store.Get(ctx, firstKey)
	require.NoError(t, err)
	second, err := store.Get(ctx, secondKey)
	require.NoError(t, err)
	require.Equal(t, "sandbox-a", first.SandboxID)
	require.Equal(t, "sandbox-b", second.SandboxID)
}

// testSessionSandboxBindingInvalidateByConfig is a store-level contract: both
// implementations have to find every binding of one workspace's config without
// enumerating another workspace's, and mark it in place.
func testSessionSandboxBindingInvalidateByConfig(t *testing.T, store SessionSandboxBindingStore) {
	t.Helper()

	ctx := context.Background()
	target := SessionSandboxKey{TenantID: 42, SessionID: "session-target"}
	otherConfig := SessionSandboxKey{TenantID: 42, SessionID: "session-other-config"}
	otherTenant := SessionSandboxKey{TenantID: 43, SessionID: "session-other-tenant"}
	// A binding written before ConfigID existed - the one case a rolling
	// upgrade actually produces. It must match no config at all, rather than
	// every config, or the first install after an upgrade rebuilds the whole
	// deployment's sandboxes.
	legacy := SessionSandboxKey{TenantID: 42, SessionID: "session-pre-upgrade"}
	create := func(key SessionSandboxKey, configID string) {
		binding := validSessionSandboxBinding(key, "sandbox-"+key.SessionID)
		binding.ConfigID = configID
		created, err := store.Create(ctx, key, binding)
		require.NoError(t, err)
		require.True(t, created)
	}
	create(target, "cfg-1")
	create(otherConfig, "cfg-2")
	create(otherTenant, "cfg-1")
	create(legacy, "")

	marked, err := store.InvalidateByConfig(ctx, 42, "cfg-1")
	require.NoError(t, err)
	require.Equal(t, 1, marked)

	got, err := store.Get(ctx, target)
	require.NoError(t, err)
	require.NotNil(t, got.StaleAt)
	require.Equal(t, "sandbox-"+target.SessionID, got.SandboxID,
		"marking must not disturb the rest of the binding")

	legacyMarked, err := store.InvalidateByConfig(ctx, 42, "")
	require.NoError(t, err)
	require.Zero(t, legacyMarked,
		"an empty ConfigID means 'unknown', not 'the deployment default config'")
	got, err = store.Get(ctx, legacy)
	require.NoError(t, err)
	require.Nil(t, got.StaleAt, "a pre-upgrade binding must survive an install untouched")

	for _, key := range []SessionSandboxKey{otherConfig, otherTenant, legacy} {
		got, err = store.Get(ctx, key)
		require.NoError(t, err)
		require.Nil(t, got.StaleAt, "binding %s must be untouched", key.SessionID)
	}

	marked, err = store.InvalidateByConfig(ctx, 42, "cfg-1")
	require.NoError(t, err)
	require.Zero(t, marked, "an already marked binding is not marked again")
}

// bindingMarkBudgetSpy reports the budget the marking work is given, once the
// session's lifecycle lock is held.
type bindingMarkBudgetSpy struct {
	*MemorySessionSandboxBindingStore
	markBudget    time.Duration
	markHadBudget bool
}

func (s *bindingMarkBudgetSpy) WithLifecycleLock(
	ctx context.Context, key SessionSandboxKey, fn func(context.Context) error,
) error {
	return s.MemorySessionSandboxBindingStore.WithLifecycleLock(
		ctx, key, func(lockCtx context.Context) error {
			return fn(lockCtx)
		},
	)
}

func (s *bindingMarkBudgetSpy) markBindingStale(
	ctx context.Context,
	key SessionSandboxKey,
	expected SessionSandboxBinding,
	staleAt time.Time,
) (bool, error) {
	if deadline, ok := ctx.Deadline(); ok {
		s.markHadBudget = true
		s.markBudget = time.Until(deadline)
	}
	return s.MemorySessionSandboxBindingStore.markBindingStale(ctx, key, expected, staleAt)
}

// The lock-wait cap must not double as the work budget: a mark that waited most
// of the cap for the lock would otherwise be left with a few milliseconds for
// its read plus compare-and-set, and fail with a deadline error that reads like
// a Redis fault.
func TestInvalidateGivesTheMarkItsOwnBudget(t *testing.T) {
	t.Parallel()

	spy := &bindingMarkBudgetSpy{MemorySessionSandboxBindingStore: NewMemorySessionSandboxBindingStore()}
	ctx := context.Background()
	key := SessionSandboxKey{TenantID: 42, SessionID: "session-a"}
	binding := validSessionSandboxBinding(key, "sandbox-a")
	binding.ConfigID = "cfg-1"
	created, err := spy.Create(ctx, key, binding)
	require.NoError(t, err)
	require.True(t, created)

	marked, err := invalidateBindingsByConfig(ctx, spy, 42, "cfg-1")
	require.NoError(t, err)
	require.Equal(t, 1, marked)

	require.True(t, spy.markHadBudget, "the mark still has to be bounded")
	require.Greater(t, spy.markBudget, bindingInvalidateLockTimeout,
		"the mark must get its own budget, not what is left of the lock-wait cap")
}

// The wait for one session's lock stays capped: the caller holds the per-config
// install lock, so a session that happens to be creating a sandbox right now
// must cost only its own mark.
func TestInvalidateStopsWaitingForALockedSession(t *testing.T) {
	t.Parallel()

	store := NewMemorySessionSandboxBindingStore()
	ctx := context.Background()
	key := SessionSandboxKey{TenantID: 42, SessionID: "session-busy"}
	binding := validSessionSandboxBinding(key, "sandbox-a")
	binding.ConfigID = "cfg-1"
	created, err := store.Create(ctx, key, binding)
	require.NoError(t, err)
	require.True(t, created)

	held := make(chan struct{})
	release := make(chan struct{})
	go func() {
		_ = store.WithLifecycleLock(ctx, key, func(context.Context) error {
			close(held)
			<-release
			return nil
		})
	}()
	<-held
	defer close(release)

	start := time.Now()
	marked, err := store.InvalidateByConfig(ctx, 42, "cfg-1")
	elapsed := time.Since(start)

	require.Error(t, err, "the session that could not be marked is reported")
	require.Zero(t, marked)
	require.Less(t, elapsed, bindingInvalidateLockTimeout+time.Second,
		"waiting on one busy session must not hold up the rest of the workspace")
	require.GreaterOrEqual(t, elapsed, bindingInvalidateLockTimeout)
}

func TestMemorySessionSandboxBindingStoreInvalidatesByConfig(t *testing.T) {
	t.Parallel()
	testSessionSandboxBindingInvalidateByConfig(t, NewMemorySessionSandboxBindingStore())
}

func TestMemorySessionSandboxBindingStoreContract(t *testing.T) {
	t.Parallel()
	testSessionSandboxBindingStore(t, NewMemorySessionSandboxBindingStore())
}

func TestMemorySessionSandboxBindingStoreTurnLease(t *testing.T) {
	t.Parallel()
	testSessionTurnLeaseStore(t, NewMemorySessionSandboxBindingStore())
}

func testSessionTurnLeaseStore(t *testing.T, store sessionTurnLeaseStore) {
	t.Helper()

	ctx := context.Background()
	key := SessionSandboxKey{TenantID: 42, SessionID: "session-turn"}

	active, rebuildOnce, err := store.TurnState(ctx, key)
	require.NoError(t, err)
	require.False(t, active)
	require.False(t, rebuildOnce)

	require.NoError(t, store.BeginTurn(ctx, key))
	active, rebuildOnce, err = store.TurnState(ctx, key)
	require.NoError(t, err)
	require.True(t, active)
	require.True(t, rebuildOnce)

	require.NoError(t, store.BeginTurn(ctx, key))
	active, rebuildOnce, err = store.TurnState(ctx, key)
	require.NoError(t, err)
	require.True(t, active)
	require.True(t, rebuildOnce, "a nested BeginTurn must not reset rebuildOnce")

	require.NoError(t, store.ConsumeTurnRebuild(ctx, key))
	active, rebuildOnce, err = store.TurnState(ctx, key)
	require.NoError(t, err)
	require.True(t, active)
	require.False(t, rebuildOnce)

	require.NoError(t, store.EndTurn(ctx, key))
	active, rebuildOnce, err = store.TurnState(ctx, key)
	require.NoError(t, err)
	require.True(t, active)
	require.False(t, rebuildOnce)

	require.NoError(t, store.EndTurn(ctx, key))
	active, rebuildOnce, err = store.TurnState(ctx, key)
	require.NoError(t, err)
	require.False(t, active)
	require.False(t, rebuildOnce)

	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	require.Error(t, store.BeginTurn(cancelled, key))
}

func TestMemorySessionSandboxBindingStoreSeparatesTenants(t *testing.T) {
	t.Parallel()
	testSessionSandboxBindingTenantIsolation(t, NewMemorySessionSandboxBindingStore())
}

func TestSessionSandboxBindingValidation(t *testing.T) {
	t.Parallel()

	key := SessionSandboxKey{TenantID: 42, SessionID: "session-a"}
	require.NoError(t, key.Validate())
	require.Error(t, (SessionSandboxKey{}).Validate())
	require.Error(t, (SessionSandboxKey{TenantID: 42, SessionID: " \t"}).Validate())
	require.Error(t, (SessionSandboxKey{TenantID: 42, SessionID: "bad{session"}).Validate())
	require.Error(t, (SessionSandboxKey{TenantID: 42, SessionID: "bad\nsession"}).Validate())

	valid := validSessionSandboxBinding(key, "sandbox-a")
	require.NoError(t, valid.Validate(key))

	tests := []SessionSandboxBinding{
		{Version: SessionSandboxBindingVersion + 1, Provider: SandboxTypeCube, TenantID: 42, SessionID: "session-a", SandboxID: "sandbox-a"},
		{Version: SessionSandboxBindingVersion, TenantID: 42, SessionID: "session-a", SandboxID: "sandbox-a"},
		{Version: SessionSandboxBindingVersion, Provider: "unknown", TenantID: 42, SessionID: "session-a", SandboxID: "sandbox-a"},
		{Version: SessionSandboxBindingVersion, Provider: SandboxTypeCube, TenantID: 43, SessionID: "session-a", SandboxID: "sandbox-a"},
		{Version: SessionSandboxBindingVersion, Provider: SandboxTypeCube, TenantID: 42, SessionID: "other", SandboxID: "sandbox-a"},
		{Version: SessionSandboxBindingVersion, Provider: SandboxTypeCube, TenantID: 42, SessionID: "session-a"},
	}
	for _, binding := range tests {
		require.Error(t, binding.Validate(key), "binding must be rejected: %+v", binding)
	}
}

// Bindings written before this field existed must stay usable: their sandboxes
// were created while inbound access was still open, so an empty token is the
// correct value rather than a corrupt one.
func TestSessionSandboxBindingWithoutTrafficTokenStaysValid(t *testing.T) {
	key := SessionSandboxKey{TenantID: 1, SessionID: "session-1"}
	raw := []byte(`{
		"version": ` + strconv.Itoa(SessionSandboxBindingVersion) + `,
		"provider": "cube",
		"tenant_id": 1,
		"session_id": "session-1",
		"sandbox_id": "sandbox-1",
		"template_id": "tpl-1",
		"created_at": "2026-01-01T00:00:00Z"
	}`)

	var binding SessionSandboxBinding
	require.NoError(t, json.Unmarshal(raw, &binding))
	require.NoError(t, binding.Validate(key))
	require.Empty(t, binding.TrafficAccessToken)
}

func TestSessionSandboxBindingOmitsEmptyTrafficToken(t *testing.T) {
	encoded, err := json.Marshal(SessionSandboxBinding{
		Version: SessionSandboxBindingVersion, Provider: SandboxTypeCube,
		TenantID: 1, SessionID: "s", SandboxID: "sb", TemplateID: "tpl",
		CreatedAt: time.Unix(0, 0).UTC(),
	})
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "traffic_access_token")
}

func TestSessionSandboxBindingRoundTripsTrafficToken(t *testing.T) {
	const token = "traffic-token"
	encoded, err := json.Marshal(SessionSandboxBinding{
		Version: SessionSandboxBindingVersion, Provider: SandboxTypeCube,
		TenantID: 1, SessionID: "s", SandboxID: "sb", TemplateID: "tpl",
		TrafficAccessToken: token, CreatedAt: time.Unix(0, 0).UTC(),
	})
	require.NoError(t, err)
	require.Contains(t, string(encoded), `"traffic_access_token"`)

	var decoded SessionSandboxBinding
	require.NoError(t, json.Unmarshal(encoded, &decoded))
	require.Equal(t, token, decoded.TrafficAccessToken)
}

func TestMemoryLifecycleLockSerializesSameKey(t *testing.T) {
	t.Parallel()

	store := NewMemorySessionSandboxBindingStore()
	key := SessionSandboxKey{TenantID: 42, SessionID: "session-a"}
	var active atomic.Int32
	var overlapped atomic.Bool
	start := make(chan struct{})
	done := make(chan error, 2)

	for range 2 {
		go func() {
			<-start
			done <- store.WithLifecycleLock(context.Background(), key, func(context.Context) error {
				if active.Add(1) != 1 {
					overlapped.Store(true)
				}
				time.Sleep(10 * time.Millisecond)
				active.Add(-1)
				return nil
			})
		}()
	}
	close(start)

	require.NoError(t, <-done)
	require.NoError(t, <-done)
	require.False(t, overlapped.Load())
}

func TestMemoryLifecycleLockHonorsContextAndCallbackError(t *testing.T) {
	t.Parallel()

	store := NewMemorySessionSandboxBindingStore()
	key := SessionSandboxKey{TenantID: 42, SessionID: "session-a"}
	entered := make(chan struct{})
	release := make(chan struct{})
	firstDone := make(chan error, 1)
	go func() {
		firstDone <- store.WithLifecycleLock(context.Background(), key, func(context.Context) error {
			close(entered)
			<-release
			return nil
		})
	}()
	<-entered

	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	called := false
	err := store.WithLifecycleLock(ctx, key, func(context.Context) error {
		called = true
		return nil
	})
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.False(t, called)

	close(release)
	require.NoError(t, <-firstDone)

	want := errors.New("callback failed")
	err = store.WithLifecycleLock(context.Background(), key, func(context.Context) error {
		return want
	})
	require.ErrorIs(t, err, want)
}
