package vlm

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/models/api"
)

// hardDown returns a connection-level transport error in the "send request"
// phase — exactly the signature that trips the circuit breaker: the endpoint is
// unreachable (connection refused / DNS / TLS / first-byte timeout).
func hardDown() error {
	return &api.TransportError{Op: "send request", Err: errors.New("connection refused")}
}

func TestCircuitBreakerTripsAfterThreshold(t *testing.T) {
	rt := newModelRuntime("cb-trip", 32, 0)
	defer close(rt.stopCh)

	if rt.cbState.Load() != cbServerUp {
		t.Fatalf("initial state = %d, want up", rt.cbState.Load())
	}
	for i := 0; i < cbTripThreshold; i++ {
		rt.cbRecordFailure()
	}
	if rt.cbState.Load() != cbServerDown {
		t.Fatalf("state after %d hard-downs = %d, want down", cbTripThreshold, rt.cbState.Load())
	}
	// While already down, further hard-downs must be a no-op (the cooldown
	// already covers the outage; re-arming would let a blackout extend itself).
	rt.cbRecordFailure()
	if rt.cbState.Load() != cbServerDown {
		t.Fatalf("state after extra hard-down = %d, want down", rt.cbState.Load())
	}
	// A 200 (even an empty one) closes the breaker.
	rt.cbOnSuccess()
	if rt.cbState.Load() != cbServerUp {
		t.Fatalf("state after success = %d, want up", rt.cbState.Load())
	}
}

// TestCircuitBreakerTripsOnSpacedFailures guards the concurrency=1 regression:
// a dead endpoint configured with low concurrency may only produce one failing
// attempt every several seconds. A time-windowed trip (e.g. "3 within 10s")
// would never accumulate three and the breaker would never open. We count
// CONSECUTIVE hard-downs instead, so spacing does not matter. This test records
// three failures ~6s apart (the 3rd lands >10s after the 1st) and asserts the
// breaker still trips.
func TestCircuitBreakerTripsOnSpacedFailures(t *testing.T) {
	rt := newModelRuntime("cb-spaced", 32, 0)
	defer close(rt.stopCh)

	for i := 0; i < cbTripThreshold; i++ {
		rt.cbRecordFailure()
		if i < cbTripThreshold-1 {
			time.Sleep(6 * time.Second) // gaps that defeat a tight time window
		}
	}
	if rt.cbState.Load() != cbServerDown {
		t.Fatalf("state after %d spaced hard-downs = %d, want down", cbTripThreshold, rt.cbState.Load())
	}
}

func TestCircuitBreakerGuardFailFast(t *testing.T) {
	rt := newModelRuntime("cb-guard", 32, 0)
	defer close(rt.stopCh)

	// Up: every request flows.
	if err := rt.guardCircuit(context.Background()); err != nil {
		t.Fatalf("guard when up: %v", err)
	}
	// Trip it.
	for i := 0; i < cbTripThreshold; i++ {
		rt.cbRecordFailure()
	}
	// Down (cooldown not elapsed): fail fast with ErrServerDown.
	if err := rt.guardCircuit(context.Background()); !errors.Is(err, ErrServerDown) {
		t.Fatalf("guard when down: got %v, want ErrServerDown", err)
	}
	// After the cooldown elapses it moves to probing and issues exactly one
	// probe permit; the second request during probing is blocked.
	rt.cbProbeAt.Store(time.Now().Add(-time.Second).UnixNano())
	if err := rt.guardCircuit(context.Background()); err != nil {
		t.Fatalf("first probe should pass: %v", err)
	}
	if err := rt.guardCircuit(context.Background()); !errors.Is(err, ErrServerDown) {
		t.Fatalf("second probe should be blocked: got %v, want ErrServerDown", err)
	}
}

// TestCircuitBreakerFailFastUnderLoad is the core e2e-style check the feature
// is for: many requests pile onto one dead model. Only the first
// cbTripThreshold requests actually "hit" the (slow) endpoint; the rest must be
// turned away instantly by the breaker instead of each burning a full retry
// budget. Serialised via limit 1 so the wall-clock delta is unambiguous.
func TestCircuitBreakerFailFastUnderLoad(t *testing.T) {
	const id = "cb-load"
	const n = 100
	delay := 500 * time.Millisecond // simulated connect timeout per real hit

	inner := &fakeVLM{id: id, respErr: hardDown(), errDelay: delay}
	rt := defaultRegistry.runtimeFor(id, 1, 0)
	m := &managerVLM{
		inner:            inner,
		modelID:          id,
		modelName:        "fake",
		configuredLimit:  1,
		configuredRPM:    0,
		innerSupportsPWO: true,
	}

	errs := make([]error, n)
	var wg sync.WaitGroup
	start := time.Now()
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, errs[i] = m.call(context.Background(), [][]byte{testPNG}, "prompt", nil)
		}(i)
	}
	wg.Wait()
	elapsed := time.Since(start)

	down := 0
	for _, e := range errs {
		if errors.Is(e, ErrServerDown) {
			down++
		}
	}
	if down != n {
		t.Errorf("fast-fail (ErrServerDown) count = %d, want %d", down, n)
	}
	if got := rt.cbState.Load(); got != cbServerDown {
		t.Errorf("breaker state = %d, want down", got)
	}
	// Only the first cbTripThreshold requests actually waited on the dead
	// endpoint; everything else failed instantly. The baseline (no breaker, and
	// before the in-place-retry removal) would be ~n hits × delay.
	maxExpected := time.Duration(cbTripThreshold)*delay + 5*time.Second
	if elapsed > maxExpected {
		t.Errorf("elapsed = %v, want <= %v (breaker should fail the rest fast)", elapsed, maxExpected)
	}
	t.Logf("elapsed=%v, fast-fail=%d/%d, breaker=%d", elapsed, down, n, rt.cbState.Load())
}

// TestParseFailureVerdictIsWrapped checks that a dead endpoint surfaces a typed
// verdict the caller can act on (errors.Is(ErrServerDown)) while the original
// transport error is still inspectable (errors.Is of the inner HTTPError /
// TransportError).
func TestParseFailureVerdictIsWrapped(t *testing.T) {
	const id = "cb-verdict"
	inner := &fakeVLM{id: id, respErr: hardDown()}
	m := &managerVLM{
		inner:            inner,
		modelID:          id,
		modelName:        "fake",
		configuredLimit:  32,
		configuredRPM:    0,
		innerSupportsPWO: true,
	}
	_, err := m.call(context.Background(), [][]byte{testPNG}, "prompt", nil)
	if err == nil {
		t.Fatal("err = nil, want a verdict")
	}
	if !errors.Is(err, ErrServerDown) {
		t.Errorf("err = %v, want it to carry ErrServerDown", err)
	}
	var te *api.TransportError
	if !errors.As(err, &te) {
		t.Errorf("err = %v, want the original TransportError still inspectable", err)
	}
}
