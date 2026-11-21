package limiter

import (
	"context"
	"testing"
	"time"
)

// TestRateLimitDisabledByDefault pins the compatibility contract: a model
// without max_rps keeps the old behaviour and never waits.
func TestRateLimitDisabledByDefault(t *testing.T) {
	t.Cleanup(resetRateGovernor)
	resetRateGovernor()

	start := time.Now()
	for i := 0; i < 500; i++ {
		WaitRate(context.Background(), "model-off", "off", RateLimit{})
	}
	if elapsed := time.Since(start); elapsed > 100*time.Millisecond {
		t.Fatalf("disabled rate limit waited %s, want no pacing", elapsed)
	}
	if stats := RateStats(); len(stats) != 0 {
		t.Fatalf("disabled rate limit registered a bucket: %+v", stats)
	}
}

// TestRateLimitPacesToBudget verifies a one-token burst paces subsequent
// calls at roughly 1/RPS.
func TestRateLimitPacesToBudget(t *testing.T) {
	t.Cleanup(resetRateGovernor)
	resetRateGovernor()

	rl := RateLimit{RPS: 50, Burst: 1} // 20ms per token
	start := time.Now()
	for i := 0; i < 3; i++ {
		WaitRate(context.Background(), "model-paced", "paced", rl)
	}
	elapsed := time.Since(start)

	// Two gaps of 20ms after the first immediate call.
	if elapsed < 30*time.Millisecond {
		t.Fatalf("paced 3 calls in %s, want at least two 20ms intervals", elapsed)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("paced 3 calls in %s, far above the configured budget", elapsed)
	}
	stats := RateStats()
	if len(stats) != 1 || stats[0].Delayed < 2 {
		t.Fatalf("rate stats = %+v, want delayed >= 2", stats)
	}
}

// TestRateLimitHonoursBurst verifies a burst larger than one starts that many
// calls back-to-back before pacing kicks in.
func TestRateLimitHonoursBurst(t *testing.T) {
	t.Cleanup(resetRateGovernor)
	resetRateGovernor()

	rl := RateLimit{RPS: 20, Burst: 3} // 50ms per token, 3 free
	start := time.Now()
	for i := 0; i < 4; i++ {
		WaitRate(context.Background(), "model-burst", "burst", rl)
	}
	elapsed := time.Since(start)

	// The fourth call must pay one token interval; the first three do not.
	if elapsed < 40*time.Millisecond {
		t.Fatalf("four calls with burst 3 took %s, want one 50ms interval", elapsed)
	}
	if elapsed > 3*time.Second {
		t.Fatalf("four calls with burst 3 took %s, far above the configured budget", elapsed)
	}
}

// TestRateLimitIsPerModel verifies one saturated model does not delay another.
func TestRateLimitIsPerModel(t *testing.T) {
	t.Cleanup(resetRateGovernor)
	resetRateGovernor()

	rl := RateLimit{RPS: 5, Burst: 1}
	WaitRate(context.Background(), "model-a", "a", rl) // consume A's token
	start := time.Now()
	WaitRate(context.Background(), "model-b", "b", rl)
	if elapsed := time.Since(start); elapsed > 100*time.Millisecond {
		t.Fatalf("model B waited %s behind model A, want independent budgets", elapsed)
	}
}

// TestRateLimitCancelledContextFailsOpen verifies a cancelled caller is not
// blocked, mirroring the fail-open contract of the concurrency limiter.
func TestRateLimitCancelledContextFailsOpen(t *testing.T) {
	t.Cleanup(resetRateGovernor)
	resetRateGovernor()

	rl := RateLimit{RPS: 1, Burst: 1}
	WaitRate(context.Background(), "model-cancel", "cancel", rl) // exhaust the burst

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	start := time.Now()
	WaitRate(ctx, "model-cancel", "cancel", rl)
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Fatalf("cancelled rate wait blocked for %s, want fail-open", elapsed)
	}
}

// TestRateLimitDeadlineShorterThanWaitFailsOpen verifies a caller whose own
// deadline cannot accommodate the wait is not parked behind the bucket.
func TestRateLimitDeadlineShorterThanWaitFailsOpen(t *testing.T) {
	t.Cleanup(resetRateGovernor)
	resetRateGovernor()

	rl := RateLimit{RPS: 1, Burst: 1}                         // next token in ~1s
	WaitRate(context.Background(), "model-deadline", "d", rl) // exhaust the burst

	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	start := time.Now()
	WaitRate(ctx, "model-deadline", "d", rl)
	if elapsed := time.Since(start); elapsed > 300*time.Millisecond {
		t.Fatalf("short-deadline rate wait blocked for %s, want fail-open", elapsed)
	}
}

// TestRateLimitRetunesWithoutRestart verifies a config change applies to an
// existing bucket, so operators can raise max_rps at runtime.
func TestRateLimitRetunesWithoutRestart(t *testing.T) {
	t.Cleanup(resetRateGovernor)
	resetRateGovernor()

	WaitRate(context.Background(), "model-retune", "r", RateLimit{RPS: 1, Burst: 1})
	WaitRate(context.Background(), "model-retune", "r", RateLimit{RPS: 1000, Burst: 1})

	stats := RateStats()
	if len(stats) != 1 || stats[0].RPS != 1000 {
		t.Fatalf("rate stats = %+v, want RPS retuned to 1000", stats)
	}
	start := time.Now()
	WaitRate(context.Background(), "model-retune", "r", RateLimit{RPS: 1000, Burst: 1})
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Fatalf("retuned bucket still paced at the old rate: %s", elapsed)
	}
}

// TestRateLimitZeroBurstCoerced verifies a misconfigured burst does not panic
// and degrades to strict one-at-a-time pacing.
func TestRateLimitZeroBurstCoerced(t *testing.T) {
	t.Cleanup(resetRateGovernor)
	resetRateGovernor()

	WaitRate(context.Background(), "model-zero-burst", "z", RateLimit{RPS: 1000})
	if stats := RateStats(); len(stats) != 1 || stats[0].Burst != 1 {
		t.Fatalf("rate stats = %+v, want burst coerced to 1", stats)
	}
}
