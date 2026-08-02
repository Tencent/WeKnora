package service

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func TestKnowledgeReconcileLocalLockSerializesSameKnowledge(t *testing.T) {
	_, releaseFirst, err := acquireKnowledgeReconcileLock(
		context.Background(),
		nil,
		"local-serialized",
	)
	require.NoError(t, err)

	acquiredSecond := make(chan func(), 1)
	go func() {
		_, release, acquireErr := acquireKnowledgeReconcileLock(
			context.Background(),
			nil,
			"local-serialized",
		)
		if acquireErr == nil {
			acquiredSecond <- release
		}
	}()

	select {
	case <-acquiredSecond:
		t.Fatal("second reconciler acquired the same knowledge lock early")
	case <-time.After(25 * time.Millisecond):
	}
	releaseFirst()

	select {
	case releaseSecond := <-acquiredSecond:
		releaseSecond()
	case <-time.After(time.Second):
		t.Fatal("second reconciler did not acquire after release")
	}
}

func TestKnowledgeReconcileRedisLockIsOwnedAndContextAware(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })

	_, releaseFirst, err := acquireKnowledgeReconcileLock(
		context.Background(),
		client,
		"redis-serialized",
	)
	require.NoError(t, err)

	waitCtx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	_, _, err = acquireKnowledgeReconcileLock(waitCtx, client, "redis-serialized")
	require.ErrorIs(t, err, context.DeadlineExceeded)

	releaseFirst()
	_, releaseSecond, err := acquireKnowledgeReconcileLock(
		context.Background(),
		client,
		"redis-serialized",
	)
	require.NoError(t, err)
	releaseSecond()
}

func TestKnowledgeReconcileRedisLockCancelsWorkWhenLeaseIsLost(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })

	lockCtx, release, err := acquireKnowledgeReconcileLockWithRenewInterval(
		context.Background(),
		client,
		"redis-lost",
		10*time.Millisecond,
	)
	require.NoError(t, err)
	defer release()
	require.NoError(t, client.Del(context.Background(), "weknora:knowledge:reconcile:redis-lost").Err())

	select {
	case <-lockCtx.Done():
		require.ErrorIs(t, lockCtx.Err(), context.Canceled)
	case <-time.After(time.Second):
		t.Fatal("lost Redis ownership did not cancel reconciliation work")
	}
}
