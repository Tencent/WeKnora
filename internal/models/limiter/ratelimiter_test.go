package limiter

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func newTestRateLimiter(t *testing.T) (*redisRateLimiter, *miniredis.Miniredis) {
	t.Helper()
	s, err := miniredis.Run()
	if err != nil {
		t.Fatalf("start miniredis: %v", err)
	}
	t.Cleanup(s.Close)
	rdb := redis.NewClient(&redis.Options{Addr: s.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	return &redisRateLimiter{rdb: rdb}, s
}

// TestRedisRateLimiterConsumesBudget verifies a fresh bucket admits up to
// `capacity` immediately, then throttles the next request.
func TestRedisRateLimiterConsumesBudget(t *testing.T) {
	l, _ := newTestRateLimiter(t)
	const key = "tpm:m1"
	// capacity = rpm = 3; drain all three quickly.
	for i := range 3 {
		ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
		if err := l.Wait(ctx, key, 3, 3, 1); err != nil {
			t.Fatalf("wait %d: %v", i, err)
		}
		cancel()
	}
	// Fourth within the same window would have to wait ~20s at rpm=3; a short
	// deadline forces the fail-open (cancelled) path to return promptly.
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	start := time.Now()
	if err := l.Wait(ctx, key, 3, 3, 1); err != nil {
		t.Fatalf("throttled wait should fail open, got %v", err)
	}
	if time.Since(start) < 100*time.Millisecond {
		t.Fatalf("fourth request should have blocked until ctx timeout, returned too fast")
	}
}

// TestRedisRateLimiterOversizedRequestCapped verifies a single request larger
// than capacity is capped rather than deadlocking forever.
func TestRedisRateLimiterOversizedRequestCapped(t *testing.T) {
	l, _ := newTestRateLimiter(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	// want (1_000_000) >> capacity (100); must still return (capped to cap).
	if err := l.Wait(ctx, "tpm:big", 100, 100, 1_000_000); err != nil {
		t.Fatalf("oversized request should be capped and admitted, got %v", err)
	}
}

// TestRedisRateLimiterAdjustRefunds verifies Adjust returns unused tokens so a
// subsequent request that would otherwise throttle is admitted.
func TestRedisRateLimiterAdjustRefunds(t *testing.T) {
	l, _ := newTestRateLimiter(t)
	const key = "tpm:m2"
	ctx := context.Background()
	// Drain the whole bucket (capacity 5) with one reservation of 5.
	if err := l.Wait(ctx, key, 5, 5, 5); err != nil {
		t.Fatalf("initial drain: %v", err)
	}
	// Refund 5 (the reservation was fully unused).
	l.Adjust(ctx, key, 5, 5, 5)
	// A follow-up request of 5 should now be immediately admitted.
	c, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
	defer cancel()
	start := time.Now()
	if err := l.Wait(c, key, 5, 5, 5); err != nil {
		t.Fatalf("post-refund wait: %v", err)
	}
	if time.Since(start) > 100*time.Millisecond {
		t.Fatalf("refunded bucket should admit immediately, took %v", time.Since(start))
	}
}

// TestRedisRateLimiterFailsOpen verifies degraded inputs and a downed backend
// never block traffic.
func TestRedisRateLimiterFailsOpen(t *testing.T) {
	ctx := context.Background()

	nilL := &redisRateLimiter{rdb: nil}
	if err := nilL.Wait(ctx, "k", 10, 10, 1); err != nil {
		t.Fatalf("nil client should fail open: %v", err)
	}

	l, s := newTestRateLimiter(t)
	if err := l.Wait(ctx, "k", 0, 10, 1); err != nil {
		t.Fatalf("ratePerMin<=0 should fail open: %v", err)
	}
	s.Close()
	if err := l.Wait(ctx, "k", 10, 10, 1); err != nil {
		t.Fatalf("backend down should fail open: %v", err)
	}
}

// TestLocalRateLimiterConsumesAndRefunds mirrors the Redis behaviour for the
// in-process bucket used in Lite mode.
func TestLocalRateLimiterConsumesAndRefunds(t *testing.T) {
	l := NewLocalRateLimiter()
	const key = "tpm:local"
	ctx := context.Background()
	if err := l.Wait(ctx, key, 4, 4, 4); err != nil {
		t.Fatalf("drain: %v", err)
	}
	// Bucket empty: a same-window request throttles until ctx timeout.
	c, cancel := context.WithTimeout(ctx, 120*time.Millisecond)
	start := time.Now()
	if err := l.Wait(c, key, 4, 4, 4); err != nil {
		t.Fatalf("throttled should fail open: %v", err)
	}
	cancel()
	if time.Since(start) < 80*time.Millisecond {
		t.Fatal("empty local bucket should block until ctx timeout")
	}
	// Refund and confirm immediate admission.
	l.Adjust(ctx, key, 4, 4, 4)
	c2, cancel2 := context.WithTimeout(ctx, 200*time.Millisecond)
	defer cancel2()
	start = time.Now()
	if err := l.Wait(c2, key, 4, 4, 4); err != nil {
		t.Fatalf("post-refund: %v", err)
	}
	if time.Since(start) > 100*time.Millisecond {
		t.Fatalf("refunded local bucket should admit immediately, took %v", time.Since(start))
	}
}

// TestAdmitRPMGovernsBackground verifies the multi-dimension Admit throttles a
// background call on the RPM dimension alone (no concurrency/tpm configured).
func TestAdmitRPMGovernsBackground(t *testing.T) {
	t.Cleanup(func() { SetGovernor(nil, nil, Limits{}) })
	SetGovernor(nil, NewLocalRateLimiter(), Limits{RPM: 2})

	ctx := withBackground()
	// Two requests fit the RPM=2 budget instantly.
	for range 2 {
		res := Admit(ctx, "m", Limits{}, 0)
		res.Release(-1)
	}
	// Third would throttle ~30s; a short deadline forces fail-open return.
	c, cancel := context.WithTimeout(ctx, 120*time.Millisecond)
	defer cancel()
	start := time.Now()
	res := Admit(c, "m", Limits{}, 0)
	res.Release(-1)
	if time.Since(start) < 80*time.Millisecond {
		t.Fatal("third background request should have blocked on RPM until ctx timeout")
	}
}

// TestAdmitInteractiveBypassesRate verifies interactive (non-background) calls
// are never rate-gated.
func TestAdmitInteractiveBypassesRate(t *testing.T) {
	t.Cleanup(func() { SetGovernor(nil, nil, Limits{}) })
	SetGovernor(nil, NewLocalRateLimiter(), Limits{RPM: 1})

	for range 5 {
		res := Admit(context.Background(), "m", Limits{}, 0) // no background marker
		res.Release(-1)
	}
}
