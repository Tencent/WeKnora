package vlm

import (
	"context"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
)

// newTestRuntime builds a standalone per-model runtime with a unique id so the
// shared registry stays isolated between tests. The scheduler goroutine it
// spawns is harmless for the brief test-binary lifetime.
func newTestRuntime(t *testing.T, limit, rpm int) *modelRuntime {
	t.Helper()
	id := "test-" + t.Name()
	return defaultRegistry.runtimeFor(id, limit, rpm)
}

func TestAdaptiveScaleOn429(t *testing.T) {
	rt := newTestRuntime(t, 10, 0)
	rt.onRateLimited(0)
	if got := rt.effectiveLimit.Load(); got != 5 {
		t.Errorf("effectiveLimit = %d, want 5", got)
	}
	if rt.available.Load() {
		t.Error("available should be false after 429")
	}
	if got := rt.rateLimitedCount.Load(); got != 1 {
		t.Errorf("rateLimitedCount = %d, want 1", got)
	}
	cd := time.Until(time.Unix(0, rt.cooldownUntil.Load()))
	if cd <= 0 || cd > cooldown429+time.Second {
		t.Errorf("cooldownRemaining = %v, want within (0, cooldown429]", cd)
	}
}

func TestAdaptiveScaleOn5xx(t *testing.T) {
	rt := newTestRuntime(t, 10, 0)
	rt.onServerError(true)
	if got := rt.effectiveLimit.Load(); got != 7 {
		t.Errorf("effectiveLimit = %d, want 7 (10*0.7)", got)
	}
	if rt.available.Load() {
		t.Error("available should be false after 5xx")
	}
	if got := rt.failedCount.Load(); got != 1 {
		t.Errorf("failedCount = %d, want 1", got)
	}
}

func TestAdaptiveScaleFloor(t *testing.T) {
	rt := newTestRuntime(t, 10, 0)
	rt.scaleLimit(0.5) // 10->5
	rt.scaleLimit(0.5) // 5->2
	rt.scaleLimit(0.5) // 2->1
	rt.scaleLimit(0.5) // 1->1 floor
	if got := rt.effectiveLimit.Load(); got != 1 {
		t.Errorf("effectiveLimit = %d, want 1 (floor)", got)
	}
}

func TestAIMDRecovery(t *testing.T) {
	rt := newTestRuntime(t, 10, 0)
	rt.effectiveLimit.Store(1) // simulate a throttled state
	for i := 0; i < recoveryThreshold; i++ {
		rt.onSuccess(10, time.Second)
	}
	if got := rt.effectiveLimit.Load(); got != 2 {
		t.Errorf("effectiveLimit after one recovery step = %d, want 2", got)
	}
	if got := rt.consecSuccess.Load(); got != 0 {
		t.Errorf("consecSuccess should reset after a recovery step, got %d", got)
	}
	// Recover all the way back to the configured limit and confirm available flips true.
	for rt.effectiveLimit.Load() < int64(rt.configuredLimit) {
		for i := 0; i < recoveryThreshold; i++ {
			rt.onSuccess(10, time.Second)
		}
	}
	if got := rt.effectiveLimit.Load(); got != int64(rt.configuredLimit) {
		t.Errorf("effectiveLimit = %d, want %d", got, rt.configuredLimit)
	}
	if !rt.available.Load() {
		t.Error("available should be true once back at the configured limit")
	}
}

func TestStallCounting(t *testing.T) {
	rt := newTestRuntime(t, 10, 0)
	rt.markPrefillStall()
	rt.markGenStall()
	if got := rt.prefillStallCount.Load(); got != 1 {
		t.Errorf("prefillStallCount = %d, want 1", got)
	}
	if got := rt.genStallCount.Load(); got != 1 {
		t.Errorf("genStallCount = %d, want 1", got)
	}
}

func TestRPMGatingBlocksSecondRequest(t *testing.T) {
	// configuredRPM=1 seeds the bucket with exactly one token.
	rt := newTestRuntime(t, 32, 1)
	if rt.effectiveRPM.Load() != 1 {
		t.Fatalf("effectiveRPM = %d, want 1", rt.effectiveRPM.Load())
	}

	// First request takes the only token (hard admit, held).
	rel1, err := rt.admit(context.Background(), prioLow)
	if err != nil {
		t.Fatalf("first admit: %v", err)
	}
	if rt.inFlight != 1 {
		t.Fatalf("inFlight = %d, want 1", rt.inFlight)
	}

	// Second request must block on the RPM budget until its context is cancelled.
	ctx2, cancel2 := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, e := rt.admit(ctx2, prioLow)
		done <- e
	}()
	time.Sleep(300 * time.Millisecond)
	cancel2()
	select {
	case e := <-done:
		if e != context.Canceled {
			t.Errorf("second admit err = %v, want context.Canceled (RPM-blocked, not admitted)", e)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("second admit did not return after context cancel")
	}
	if rt.inFlight != 1 {
		t.Errorf("inFlight = %d, want 1 (second request must not consume a permit)", rt.inFlight)
	}
	rel1()
}

func TestFailOpenIsHighOnly(t *testing.T) {
	rt := newTestRuntime(t, 32, 0)
	rt.effectiveLimit.Store(0) // fully throttled: no hard permit available

	// HIGH should fail open (~500ms).
	highDone := make(chan struct{})
	go func() {
		_, _ = rt.admit(context.Background(), prioHigh)
		close(highDone)
	}()
	select {
	case <-highDone:
	case <-time.After(2 * time.Second):
		t.Fatal("HIGH did not fail open within 2s")
	}

	// LOW should stay blocked under the same throttle.
	lowCtx, lowCancel := context.WithCancel(context.Background())
	lowDone := make(chan struct{})
	go func() {
		_, _ = rt.admit(lowCtx, prioLow)
		close(lowDone)
	}()
	select {
	case <-lowDone:
		t.Fatal("LOW was admitted despite a fully-throttled runtime (should block)")
	case <-time.After(1500 * time.Millisecond):
		// expected: still queued
	}
	lowCancel()
	select {
	case <-lowDone:
	case <-time.After(2 * time.Second):
		t.Fatal("LOW did not return after its context was cancelled")
	}
}

func TestPriorityOrderingHighBeforeLow(t *testing.T) {
	rt := newTestRuntime(t, 1, 0) // only one hard permit
	holdRel, err := rt.admit(context.Background(), prioHigh)
	if err != nil {
		t.Fatalf("hold admit: %v", err)
	}
	if rt.inFlight != 1 {
		t.Fatalf("inFlight = %d, want 1", rt.inFlight)
	}

	// Enqueue LOW first, then HIGH. With the permit held neither gets a hard
	// permit, but HIGH must fail open before LOW (LOW blocks indefinitely).
	lowCtx, lowCancel := context.WithCancel(context.Background())
	lowDone := make(chan struct{})
	go func() {
		_, _ = rt.admit(lowCtx, prioLow)
		close(lowDone)
	}()
	time.Sleep(20 * time.Millisecond)
	highDone := make(chan struct{})
	go func() {
		_, _ = rt.admit(context.Background(), prioHigh)
		close(highDone)
	}()

	select {
	case <-highDone:
	case <-time.After(2 * time.Second):
		t.Fatal("HIGH did not fail open within 2s")
	}
	select {
	case <-lowDone:
		t.Fatal("LOW was admitted before HIGH despite being queued first")
	case <-time.After(1500 * time.Millisecond):
		// expected: LOW still blocked
	}

	lowCancel()
	holdRel()
	select {
	case <-lowDone:
	case <-time.After(2 * time.Second):
		t.Fatal("LOW did not return after cancel")
	}
}

func TestRuntimeSnapshotAndStats(t *testing.T) {
	rt := newTestRuntime(t, 16, 10)
	rt.markQueued(types.CallerKBBackground)
	rt.markAdmitted(types.CallerKBBackground)
	rt.onSuccess(42, 2*time.Second)

	snap := rt.Snapshot()
	if snap.ModelID == "" {
		t.Error("Snapshot ModelID empty")
	}
	if snap.ConfiguredLimit != 16 {
		t.Errorf("ConfiguredLimit = %d, want 16", snap.ConfiguredLimit)
	}
	if snap.ConfiguredRPM != 10 {
		t.Errorf("ConfiguredRPM = %d, want 10", snap.ConfiguredRPM)
	}
	if snap.LastTokens != 42 {
		t.Errorf("LastTokens = %d, want 42", snap.LastTokens)
	}
	if snap.LastGenSpeed <= 0 {
		t.Errorf("LastGenSpeed = %v, want > 0", snap.LastGenSpeed)
	}
	if _, ok := snap.Callers[string(types.CallerKBBackground)]; !ok {
		t.Error("Callers missing kb_background snapshot")
	}

	all := RuntimeStats()
	if all == nil {
		t.Fatal("RuntimeStats returned nil")
	}
	found := false
	for _, s := range all {
		if s.ModelID == snap.ModelID {
			found = true
		}
	}
	if !found {
		t.Error("RuntimeStats missing the test runtime")
	}
}

// TestOnClientErrorDoesNotShed pins the KindPermanent contract: a permanent
// client-side fault (bad key, oversized image, bad request) must be recorded
// but must NOT shed concurrency or start a cooldown — otherwise one bad request
// would degrade the model for everyone.
func TestOnClientErrorDoesNotShed(t *testing.T) {
	rt := newTestRuntime(t, 10, 0)
	rt.onClientError()
	if got := rt.failedCount.Load(); got != 1 {
		t.Errorf("failedCount = %d, want 1", got)
	}
	if !rt.available.Load() {
		t.Error("available should stay true: a client error is not a provider failure")
	}
	if got := rt.effectiveLimit.Load(); got != 10 {
		t.Errorf("effectiveLimit = %d, want 10 (no shed)", got)
	}
}
