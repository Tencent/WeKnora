package limiter

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func newTestLimiter(t *testing.T, ttl, poll time.Duration) (*redisLimiter, *miniredis.Miniredis, *redis.Client) {
	t.Helper()
	s, err := miniredis.Run()
	if err != nil {
		t.Fatalf("start miniredis: %v", err)
	}
	t.Cleanup(s.Close)
	rdb := redis.NewClient(&redis.Options{Addr: s.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	return &redisLimiter{rdb: rdb, ttl: ttl, pollInterval: poll}, s, rdb
}

func zcard(t *testing.T, rdb *redis.Client, key string) int64 {
	t.Helper()
	n, err := rdb.ZCard(context.Background(), keyPrefix+key).Result()
	if err != nil {
		t.Fatalf("zcard: %v", err)
	}
	return n
}

// TestRedisLimiterCapsConcurrency verifies the live-holder count never exceeds
// the limit and that a freed slot lets a waiting acquirer through.
func TestRedisLimiterCapsConcurrency(t *testing.T) {
	lim, _, rdb := newTestLimiter(t, time.Minute, 10*time.Millisecond)
	ctx := context.Background()
	const key = "m1"

	r1, err := lim.Acquire(ctx, key, 2)
	if err != nil {
		t.Fatalf("acquire 1: %v", err)
	}
	r2, err := lim.Acquire(ctx, key, 2)
	if err != nil {
		t.Fatalf("acquire 2: %v", err)
	}
	if got := zcard(t, rdb, key); got != 2 {
		t.Fatalf("expected 2 live holders, got %d", got)
	}

	// The third acquire must block while the semaphore is full.
	done := make(chan func(), 1)
	go func() {
		r3, _ := lim.Acquire(ctx, key, 2)
		done <- r3
	}()

	select {
	case <-done:
		t.Fatal("third acquire should block while at limit")
	case <-time.After(100 * time.Millisecond):
	}
	if got := zcard(t, rdb, key); got != 2 {
		t.Fatalf("count must not exceed limit while blocked, got %d", got)
	}

	// Freeing a slot lets the waiter in.
	r1()
	var r3 func()
	select {
	case r3 = <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("third acquire should proceed after a slot frees")
	}
	if got := zcard(t, rdb, key); got != 2 {
		t.Fatalf("expected 2 live holders after handoff, got %d", got)
	}

	r2()
	r3()
	if got := zcard(t, rdb, key); got != 0 {
		t.Fatalf("expected 0 live holders after release, got %d", got)
	}
}

// TestRedisLimiterReclaimsExpiredLease verifies a crashed holder's stale lease
// (no heartbeat) is pruned so its slot is reclaimed by a new acquirer.
func TestRedisLimiterReclaimsExpiredLease(t *testing.T) {
	lim, _, rdb := newTestLimiter(t, time.Minute, 10*time.Millisecond)
	ctx := context.Background()
	const key = "m2"

	// Simulate a dead holder: a member whose lease already expired.
	expired := float64(time.Now().Add(-time.Second).UnixMilli())
	if err := rdb.ZAdd(ctx, keyPrefix+key, redis.Z{Score: expired, Member: "dead"}).Err(); err != nil {
		t.Fatalf("seed dead holder: %v", err)
	}

	// limit=1 would be full if the dead lease still counted; pruning reclaims it.
	release, err := lim.Acquire(ctx, key, 1)
	if err != nil {
		t.Fatalf("acquire after expiry: %v", err)
	}
	if got := zcard(t, rdb, key); got != 1 {
		t.Fatalf("expected only the live holder, got %d", got)
	}
	if exists, _ := rdb.ZScore(ctx, keyPrefix+key, "dead").Result(); exists != 0 {
		t.Fatal("expired lease should have been pruned")
	}
	release()
}

// TestRedisLimiterIndependentKeys verifies budgets are per key.
func TestRedisLimiterIndependentKeys(t *testing.T) {
	lim, _, _ := newTestLimiter(t, time.Minute, 10*time.Millisecond)
	ctx := context.Background()

	ra, err := lim.Acquire(ctx, "a", 1)
	if err != nil {
		t.Fatalf("acquire a: %v", err)
	}
	// A different key has its own budget, so this must not block.
	rb, err := lim.Acquire(ctx, "b", 1)
	if err != nil {
		t.Fatalf("acquire b: %v", err)
	}
	ra()
	rb()
}

// TestRedisLimiterFailsOpen verifies every degraded path allows the call
// instead of blocking model traffic.
func TestRedisLimiterFailsOpen(t *testing.T) {
	ctx := context.Background()

	// Nil client.
	lim := &redisLimiter{rdb: nil, ttl: time.Minute, pollInterval: 10 * time.Millisecond}
	if release, err := lim.Acquire(ctx, "k", 4); err != nil || release == nil {
		t.Fatalf("nil client should fail open, got release==nil=%v err=%v", release == nil, err)
	}

	// Non-positive limit is a no-op governor.
	lim2, _, _ := newTestLimiter(t, time.Minute, 10*time.Millisecond)
	if release, err := lim2.Acquire(ctx, "k", 0); err != nil || release == nil {
		t.Fatalf("limit<=0 should fail open, got release==nil=%v err=%v", release == nil, err)
	}

	// Backend error (server down) must fail open too.
	lim3, s, _ := newTestLimiter(t, time.Minute, 10*time.Millisecond)
	s.Close()
	if release, err := lim3.Acquire(ctx, "k", 4); err != nil || release == nil {
		t.Fatalf("backend error should fail open, got release==nil=%v err=%v", release == nil, err)
	}
}

// TestRedisLimiterCancelledContextFailsOpen verifies a waiter whose context is
// cancelled while blocked returns a usable (no-op) release rather than erroring.
func TestRedisLimiterCancelledContextFailsOpen(t *testing.T) {
	lim, _, _ := newTestLimiter(t, time.Minute, 10*time.Millisecond)

	r1, err := lim.Acquire(context.Background(), "m3", 1)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	defer r1()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	release, err := lim.Acquire(ctx, "m3", 1)
	if err != nil || release == nil {
		t.Fatalf("cancelled wait should fail open, got release==nil=%v err=%v", release == nil, err)
	}
	release()
}
