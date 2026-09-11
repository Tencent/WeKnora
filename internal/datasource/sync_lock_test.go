package datasource

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func TestSyncLocksMutualExclusion(t *testing.T) {
	for _, distributed := range []bool{false, true} {
		t.Run(map[bool]string{false: "lite", true: "redis"}[distributed], func(t *testing.T) {
			var rdb *redis.Client
			if distributed {
				server := miniredis.RunT(t)
				rdb = redis.NewClient(&redis.Options{Addr: server.Addr()})
				defer rdb.Close()
			}
			locks := NewSyncLocks(rdb)
			ctx, release, err := locks.Acquire(context.Background(), 1, "ds")
			if err != nil {
				t.Fatal(err)
			}
			if err := CheckSyncLease(ctx); err != nil {
				t.Fatal(err)
			}
			if _, _, err := locks.Acquire(context.Background(), 1, "ds"); !errors.Is(err, ErrSyncRunning) {
				t.Fatal(err)
			}
			_, otherRelease, err := locks.Acquire(context.Background(), 2, "ds")
			if err != nil {
				t.Fatal("tenants must not share execution locks")
			}
			otherRelease()
			release()
			_, release, err = locks.Acquire(context.Background(), 1, "ds")
			if err != nil {
				t.Fatal(err)
			}
			release()
		})
	}
}

func TestLostRedisLeaseDoesNotReleaseNewOwner(t *testing.T) {
	server := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: server.Addr()})
	defer rdb.Close()
	locks := NewSyncLocks(rdb)
	ctx, release, err := locks.Acquire(context.Background(), 1, "ds")
	if err != nil {
		t.Fatal(err)
	}
	server.FastForward(121 * time.Second)
	next, nextRelease, err := locks.Acquire(context.Background(), 1, "ds")
	if err != nil {
		t.Fatal(err)
	}
	defer nextRelease()
	if CheckSyncLease(ctx) == nil {
		t.Fatal("lost ownership was accepted")
	}
	release()
	if err := CheckSyncLease(next); err != nil {
		t.Fatalf("old release removed new owner: %v", err)
	}
}
