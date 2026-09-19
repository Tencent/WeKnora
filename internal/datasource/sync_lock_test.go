package datasource

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func TestLocalSyncExecutionLockerIsFailFast(t *testing.T) {
	locker := NewSyncExecutionLocker(nil)
	first, err := locker.TryAcquire(context.Background(), "ds-1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := locker.TryAcquire(context.Background(), "ds-1"); !errors.Is(err, ErrSyncAlreadyRunning) {
		t.Fatalf("second acquire error = %v", err)
	}
	if err := first.Release(); err != nil {
		t.Fatal(err)
	}
	second, err := locker.TryAcquire(context.Background(), "ds-1")
	if err != nil {
		t.Fatal(err)
	}
	_ = second.Release()
}

func TestSyncTaskIDIsUniquePerRun(t *testing.T) {
	first := SyncTaskID("ds-1", "log-1")
	second := SyncTaskID("ds-1", "log-2")
	if first == second {
		t.Fatalf("task IDs must be unique per sync run: %q", first)
	}
}

func TestLocalSyncExecutionLockerDoesNotBlockOtherDataSources(t *testing.T) {
	locker := NewSyncExecutionLocker(nil)
	first, err := locker.TryAcquire(context.Background(), "ds-1")
	if err != nil {
		t.Fatal(err)
	}
	defer first.Release()
	second, err := locker.TryAcquire(context.Background(), "ds-2")
	if err != nil {
		t.Fatalf("different datasource was blocked: %v", err)
	}
	_ = second.Release()
}

func TestRedisSyncExecutionLockerCoordinatesInstances(t *testing.T) {
	mini := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mini.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	firstLocker := NewSyncExecutionLocker(client)
	secondLocker := NewSyncExecutionLocker(client)
	first, err := firstLocker.TryAcquire(context.Background(), "ds-1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := secondLocker.TryAcquire(context.Background(), "ds-1"); !errors.Is(err, ErrSyncAlreadyRunning) {
		t.Fatalf("cross-instance acquire error = %v", err)
	}
	if err := first.Release(); err != nil {
		t.Fatal(err)
	}
	second, err := secondLocker.TryAcquire(context.Background(), "ds-1")
	if err != nil {
		t.Fatal(err)
	}
	_ = second.Release()
}

func TestRedisSyncLockExpiresAfterAbandonedWorker(t *testing.T) {
	mini := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mini.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	locker := &redisSyncLocker{client: client, suffix: "sync-lock", lease: 100 * time.Millisecond, renew: time.Hour}
	abandoned, err := locker.TryAcquire(context.Background(), "ds-1")
	if err != nil {
		t.Fatal(err)
	}
	mini.FastForward(200 * time.Millisecond)
	replacement, err := locker.TryAcquire(context.Background(), "ds-1")
	if err != nil {
		t.Fatalf("replacement did not acquire expired lease: %v", err)
	}
	if err := abandoned.Release(); err == nil {
		t.Fatal("abandoned owner release should report ownership loss")
	}
	if err := replacement.Release(); err != nil {
		t.Fatal(err)
	}
}

func TestRedisSyncLockRenewsLongExecution(t *testing.T) {
	mini := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mini.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	locker := &redisSyncLocker{client: client, suffix: "sync-lock", lease: 100 * time.Millisecond, renew: 20 * time.Millisecond}
	lock, err := locker.TryAcquire(context.Background(), "ds-1")
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(60 * time.Millisecond)
	mini.FastForward(70 * time.Millisecond)
	if _, err := locker.TryAcquire(context.Background(), "ds-1"); !errors.Is(err, ErrSyncAlreadyRunning) {
		t.Fatalf("renewed lock was unexpectedly acquirable: %v", err)
	}
	if err := lock.Release(); err != nil {
		t.Fatal(err)
	}
}

func TestRedisTriggerLockCoordinatesInstances(t *testing.T) {
	mini := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mini.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	first := NewSyncCoordinator(client)
	second := NewSyncCoordinator(client)
	lock, err := first.TryAcquireTrigger(context.Background(), "ds-1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := second.TryAcquireTrigger(context.Background(), "ds-1"); !errors.Is(err, ErrSyncAlreadyRunning) {
		t.Fatalf("second trigger acquired same datasource: %v", err)
	}
	_ = lock.Release()
}

func TestRedisSyncLockLossCancelsExecutionContext(t *testing.T) {
	mini := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mini.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	locker := &redisSyncLocker{client: client, suffix: "sync-lock", lease: 200 * time.Millisecond, renew: 20 * time.Millisecond}
	lock, err := locker.TryAcquire(context.Background(), "ds-1")
	if err != nil {
		t.Fatal(err)
	}
	mini.Del("weknora:datasource:{ds-1}:sync-lock")
	select {
	case <-lock.Context().Done():
		if !errors.Is(context.Cause(lock.Context()), context.Canceled) && context.Cause(lock.Context()) == nil {
			t.Fatal("lock context canceled without a cause")
		}
	case <-time.After(time.Second):
		t.Fatal("lock loss did not cancel execution context")
	}
	_ = lock.Release()
}

func TestRedisSyncLockFailsClosedWhenRedisUnavailable(t *testing.T) {
	mini := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mini.Addr()})
	mini.Close()
	locker := NewSyncExecutionLocker(client)
	if _, err := locker.TryAcquire(context.Background(), "ds-1"); err == nil {
		t.Fatal("Redis failure must not fall back to an unlocked execution")
	}
}
