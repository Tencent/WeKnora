package limiter

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

// Pause a heartbeat before it reaches Redis so release can remove its lease
// first. This reproduces the command ordering without relying on a race.
type heartbeatHook struct {
	started  chan struct{}
	resume   chan struct{}
	finished chan struct{}
	once     sync.Once
}

func (h *heartbeatHook) DialHook(next redis.DialHook) redis.DialHook { return next }

func (h *heartbeatHook) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return next
}

func (h *heartbeatHook) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		paused := false
		if cmd.Name() == "zadd" {
			h.once.Do(func() {
				paused = true
				close(h.started)
				<-h.resume
			})
		}
		err := next(ctx, cmd)
		if paused {
			close(h.finished)
		}
		return err
	}
}

func TestRedisLimiterHeartbeatDoesNotRestoreReleasedLease(t *testing.T) {
	lim, _, rdb := newTestLimiter(t, 300*time.Millisecond, 10*time.Millisecond)
	hook := &heartbeatHook{
		started: make(chan struct{}), resume: make(chan struct{}), finished: make(chan struct{}),
	}
	rdb.AddHook(hook)
	var resumeOnce sync.Once
	resume := func() { resumeOnce.Do(func() { close(hook.resume) }) }
	t.Cleanup(resume)

	const key = "released-heartbeat"
	release, err := lim.Acquire(context.Background(), key, 1)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(release)
	select {
	case <-hook.started:
	case <-time.After(2 * time.Second):
		t.Fatal("heartbeat did not start")
	}

	release()
	if got := zcard(t, rdb, key); got != 0 {
		t.Fatalf("holders after release = %d, want 0", got)
	}
	resume()
	select {
	case <-hook.finished:
	case <-time.After(2 * time.Second):
		t.Fatal("heartbeat did not finish")
	}
	if got := zcard(t, rdb, key); got != 0 {
		t.Fatalf("late heartbeat restored a released lease: holders = %d, want 0", got)
	}
}

func TestRedisLimiterHeartbeatRenewsLiveLease(t *testing.T) {
	lim, server, rdb := newTestLimiter(t, 300*time.Millisecond, 10*time.Millisecond)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	const key = "live-heartbeat"
	release, err := lim.Acquire(ctx, key, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	initial, err := rdb.ZRangeWithScores(ctx, keyPrefix+key, 0, -1).Result()
	if err != nil || len(initial) != 1 {
		t.Fatalf("initial leases = %v, err = %v", initial, err)
	}
	// Advance the Redis key TTL without changing the wall clock used for lease
	// scores. A live heartbeat must renew both independently.
	server.FastForward(lim.ttl)
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			t.Fatal("heartbeat did not renew the live lease and key TTL")
		case <-tick.C:
			score, err := rdb.ZScore(ctx, keyPrefix+key, initial[0].Member.(string)).Result()
			if err != nil {
				t.Fatal(err)
			}
			if score > initial[0].Score && server.TTL(keyPrefix+key) > lim.ttl {
				return
			}
		}
	}
}
