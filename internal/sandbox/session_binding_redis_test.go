package sandbox

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func newRedisBindingTestStore(
	t *testing.T,
) (*RedisSessionSandboxBindingStore, *redis.Client, *miniredis.Miniredis) {
	t.Helper()

	mini := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mini.Addr(), MaxRetries: -1})
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	store, err := NewRedisSessionSandboxBindingStore(client, "test")
	require.NoError(t, err)
	return store, client, mini
}

func newTwoRedisBindingTestStores(
	t *testing.T,
) (*RedisSessionSandboxBindingStore, *RedisSessionSandboxBindingStore) {
	t.Helper()

	mini := miniredis.RunT(t)
	newStore := func() *RedisSessionSandboxBindingStore {
		client := redis.NewClient(&redis.Options{Addr: mini.Addr(), MaxRetries: -1})
		t.Cleanup(func() { require.NoError(t, client.Close()) })
		store, err := NewRedisSessionSandboxBindingStore(client, "test")
		require.NoError(t, err)
		return store
	}
	return newStore(), newStore()
}

func TestRedisSessionSandboxBindingStoreContract(t *testing.T) {
	store, _, _ := newRedisBindingTestStore(t)
	testSessionSandboxBindingStore(t, store)
}

func TestRedisSessionSandboxBindingStoreTurnLease(t *testing.T) {
	store, client, _ := newRedisBindingTestStore(t)
	testSessionTurnLeaseStore(t, store)

	key := SessionSandboxKey{TenantID: 42, SessionID: "session-missing-turn"}
	require.NoError(t, store.ConsumeTurnRebuild(context.Background(), key))
	exists, err := client.Exists(context.Background(), store.turnKey(key)).Result()
	require.NoError(t, err)
	require.Zero(t, exists, "consuming a missing turn must not create a lease key")
}

func TestRedisSessionSandboxBindingStoreInvalidatesByConfig(t *testing.T) {
	store, _, _ := newRedisBindingTestStore(t)
	testSessionSandboxBindingInvalidateByConfig(t, store)
}

func TestRedisSessionSandboxBindingStoreSeparatesTenants(t *testing.T) {
	store, _, _ := newRedisBindingTestStore(t)
	testSessionSandboxBindingTenantIsolation(t, store)
}

func TestRedisSessionSandboxBindingUsesHashTagAndNoTTL(t *testing.T) {
	store, client, _ := newRedisBindingTestStore(t)
	key := SessionSandboxKey{TenantID: 42, SessionID: "session-a"}
	bindingKey := store.bindingKey(key)
	lockKey := store.lockKey(key)

	require.Equal(t, "weknora:sandbox:session:{test:42:session-a}:binding", bindingKey)
	require.Equal(t, "weknora:sandbox:session:{test:42:session-a}:create-lock", lockKey)

	created, err := store.Create(context.Background(), key, validSessionSandboxBinding(key, "sandbox-a"))
	require.NoError(t, err)
	require.True(t, created)
	require.Equal(t, time.Duration(-1), client.TTL(context.Background(), bindingKey).Val())
}

func TestRedisSessionSandboxBindingRejectsMissingProvider(t *testing.T) {
	store, client, _ := newRedisBindingTestStore(t)
	key := SessionSandboxKey{TenantID: 42, SessionID: "session-a"}
	incomplete := map[string]any{
		"version":     SessionSandboxBindingVersion,
		"tenant_id":   42,
		"session_id":  "session-a",
		"sandbox_id":  "sandbox-a",
		"template_id": "template-a",
		"created_at":  time.Unix(100, 0).UTC(),
	}
	raw, err := json.Marshal(incomplete)
	require.NoError(t, err)
	require.NoError(t, client.Set(context.Background(), store.bindingKey(key), raw, 0).Err())

	_, err = store.Get(context.Background(), key)
	require.Error(t, err)
}

func TestRedisSessionSandboxBindingRejectsMalformedOrMismatchedData(t *testing.T) {
	store, client, _ := newRedisBindingTestStore(t)
	key := SessionSandboxKey{TenantID: 42, SessionID: "session-a"}

	require.NoError(t, client.Set(context.Background(), store.bindingKey(key), "{", 0).Err())
	_, err := store.Get(context.Background(), key)
	require.Error(t, err)

	mismatch := validSessionSandboxBinding(
		SessionSandboxKey{TenantID: 43, SessionID: "session-a"},
		"sandbox-a",
	)
	raw, err := json.Marshal(mismatch)
	require.NoError(t, err)
	require.NoError(t, client.Set(context.Background(), store.bindingKey(key), raw, 0).Err())
	_, err = store.Get(context.Background(), key)
	require.Error(t, err)
}

func TestRedisSessionSandboxBindingFailsClosedWhenRedisStops(t *testing.T) {
	store, _, mini := newRedisBindingTestStore(t)
	key := SessionSandboxKey{TenantID: 42, SessionID: "session-a"}
	binding := validSessionSandboxBinding(key, "sandbox-a")
	mini.Close()

	_, err := store.Get(context.Background(), key)
	require.Error(t, err)
	_, err = store.Create(context.Background(), key, binding)
	require.Error(t, err)
	_, err = store.DeleteIfMatch(context.Background(), key, SandboxTypeCube, "sandbox-a")
	require.Error(t, err)

	called := false
	err = store.WithLifecycleLock(context.Background(), key, func(context.Context) error {
		called = true
		return nil
	})
	require.Error(t, err)
	require.False(t, called)
}

func TestRedisSessionSandboxBindingValidatesConstructor(t *testing.T) {
	_, err := NewRedisSessionSandboxBindingStore(nil, "test")
	require.Error(t, err)

	client := redis.NewClient(&redis.Options{Addr: "unused"})
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	for _, namespace := range []string{"bad{namespace", "bad}namespace", "bad\nnamespace"} {
		_, err = NewRedisSessionSandboxBindingStore(client, namespace)
		require.Error(t, err)
	}
	_, err = NewRedisSessionSandboxBindingStore(client, "namespace:with-punctuation")
	require.NoError(t, err)
}

func TestRedisLifecycleLockSerializesAcrossStores(t *testing.T) {
	firstStore, secondStore := newTwoRedisBindingTestStores(t)
	key := SessionSandboxKey{TenantID: 42, SessionID: "session-a"}
	firstEntered := make(chan struct{})
	releaseFirst := make(chan struct{})
	firstDone := make(chan error, 1)

	go func() {
		firstDone <- firstStore.WithLifecycleLock(context.Background(), key, func(context.Context) error {
			close(firstEntered)
			<-releaseFirst
			return nil
		})
	}()
	<-firstEntered

	secondEntered := make(chan struct{})
	secondDone := make(chan error, 1)
	go func() {
		secondDone <- secondStore.WithLifecycleLock(context.Background(), key, func(context.Context) error {
			close(secondEntered)
			return nil
		})
	}()

	select {
	case <-secondEntered:
		t.Fatal("contender entered before the first owner released")
	case <-time.After(50 * time.Millisecond):
	}
	close(releaseFirst)
	require.NoError(t, <-firstDone)
	select {
	case <-secondEntered:
	case <-time.After(time.Second):
		t.Fatal("contender did not acquire after release")
	}
	require.NoError(t, <-secondDone)
}

func TestRedisLifecycleLockAllowsDifferentKeys(t *testing.T) {
	firstStore, secondStore := newTwoRedisBindingTestStores(t)
	firstKey := SessionSandboxKey{TenantID: 42, SessionID: "session-a"}
	secondKey := SessionSandboxKey{TenantID: 43, SessionID: "session-a"}
	firstEntered := make(chan struct{})
	releaseFirst := make(chan struct{})
	firstDone := make(chan error, 1)

	go func() {
		firstDone <- firstStore.WithLifecycleLock(context.Background(), firstKey, func(context.Context) error {
			close(firstEntered)
			<-releaseFirst
			return nil
		})
	}()
	<-firstEntered

	err := secondStore.WithLifecycleLock(context.Background(), secondKey, func(context.Context) error {
		return nil
	})
	require.NoError(t, err)
	close(releaseFirst)
	require.NoError(t, <-firstDone)
}
