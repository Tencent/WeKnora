package sandbox

import (
	"context"
	"errors"
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

func TestMemorySessionSandboxBindingStoreContract(t *testing.T) {
	t.Parallel()
	testSessionSandboxBindingStore(t, NewMemorySessionSandboxBindingStore())
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
