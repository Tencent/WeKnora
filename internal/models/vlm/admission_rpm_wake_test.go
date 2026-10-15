package vlm

import (
	"context"
	"testing"
	"time"
)

// TestRPMRefillWakesLowQueue is the regression for the sleeping scheduler: a
// LOW-only queue behind a dry token bucket has no fail-open deadline and no
// incoming events, so without an armed wakeup the loop slept through the
// refill and every queued request died on its own context deadline — even
// though a token became available long before.
//
// Determinism: RPM=1 seeds the bucket with exactly one token; consuming it
// leaves the bucket dry regardless of test-machine speed (the refill during a
// few microseconds of admit/release is far below one token). Raising the rate
// to 600 RPM afterwards makes the next full token due in ~100ms, well inside
// the 400ms context deadline — but ONLY if the loop actually wakes for it.
func TestRPMRefillWakesLowQueue(t *testing.T) {
	rt := newTestRuntime(t, 1, 1)

	// Burn the only token, then release the slot.
	rel, err := rt.admit(context.Background(), prioLow)
	if err != nil {
		t.Fatalf("first admit: %v", err)
	}
	rel()

	// Refill at 600 RPM (one token per 100ms). The next token is due ~100ms
	// from now; nothing else will ever touch this runtime.
	rt.refreshConfig(1, 600)

	ctx, cancel := context.WithTimeout(context.Background(), 400*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		rel2, e := rt.admit(ctx, prioLow)
		if e == nil {
			rel2()
		}
		done <- e
	}()

	select {
	case e := <-done:
		if e != nil {
			t.Fatalf("LOW request starved until its context deadline (%v); "+
				"the scheduler never woke for the refilled token", e)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("LOW request neither admitted nor cancelled within 2s")
	}
}
