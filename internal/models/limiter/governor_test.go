package limiter

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
)

func withBackground() context.Context {
	return types.WithBackgroundTask(context.Background())
}

// TestGateOnlyGovernsBackground verifies interactive calls always pass through
// (no-op release) and only background calls consult the installed limiter.
func TestGateOnlyGovernsBackground(t *testing.T) {
	t.Cleanup(func() { SetGovernor(nil, 0) })
	SetGovernor(NewLocalLimiter(), 1)

	// Interactive (non-background) ctx: never gated, even at limit 1.
	rel1 := Gate(context.Background(), "m")
	rel2 := Gate(context.Background(), "m")
	rel1()
	rel2()

	// Background ctx: gated. First slot taken; second would block, so a
	// cancelled second acquire must fail open (return a usable release).
	held := Gate(withBackground(), "m")
	defer held()

	ctx, cancel := context.WithTimeout(withBackground(), 30*time.Millisecond)
	defer cancel()
	rel := Gate(ctx, "m")
	if rel == nil {
		t.Fatal("cancelled background gate must fail open with a usable release")
	}
	rel()
}

// TestGateDisabledWhenNoGovernor verifies Gate is a passthrough when no
// governor is installed.
func TestGateDisabledWhenNoGovernor(t *testing.T) {
	SetGovernor(nil, 0)
	if rel := Gate(withBackground(), "m"); rel == nil {
		t.Fatal("gate with no governor must return a usable release")
	} else {
		rel()
	}
}

// TestLocalLimiterCaps verifies the in-process limiter enforces the cap and
// releases slots.
func TestLocalLimiterCaps(t *testing.T) {
	l := NewLocalLimiter()
	ctx := context.Background()

	r1, _ := l.Acquire(ctx, "k", 2)
	r2, _ := l.Acquire(ctx, "k", 2)

	// Third acquire must block while full.
	done := make(chan func(), 1)
	go func() {
		r3, _ := l.Acquire(ctx, "k", 2)
		done <- r3
	}()
	select {
	case <-done:
		t.Fatal("third acquire should block while at limit")
	case <-time.After(50 * time.Millisecond):
	}

	r1() // free a slot; the waiter proceeds
	select {
	case r3 := <-done:
		r3()
	case <-time.After(time.Second):
		t.Fatal("waiter should proceed after a slot frees")
	}
	r2()
}

// TestLocalLimiterIndependentKeys verifies per-key budgets are independent.
func TestLocalLimiterIndependentKeys(t *testing.T) {
	l := NewLocalLimiter()
	ctx := context.Background()
	ra, _ := l.Acquire(ctx, "a", 1)
	rb, _ := l.Acquire(ctx, "b", 1) // different key: must not block
	ra()
	rb()
}

// TestLocalLimiterFailOpen verifies degraded inputs pass through.
func TestLocalLimiterFailOpen(t *testing.T) {
	l := NewLocalLimiter()
	ctx := context.Background()
	if r, err := l.Acquire(ctx, "", 4); err != nil || r == nil {
		t.Fatal("empty key should fail open")
	} else {
		r()
	}
	if r, err := l.Acquire(ctx, "k", 0); err != nil || r == nil {
		t.Fatal("limit<=0 should fail open")
	} else {
		r()
	}
}

// TestLocalLimiterReleaseIdempotent verifies double release doesn't
// over-free a slot.
func TestLocalLimiterReleaseIdempotent(t *testing.T) {
	l := NewLocalLimiter()
	ctx := context.Background()
	r1, _ := l.Acquire(ctx, "k", 1)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); r1() }()
	go func() { defer wg.Done(); r1() }()
	wg.Wait()

	// After idempotent release the single slot is free again.
	r2, _ := l.Acquire(ctx, "k", 1)
	done := make(chan struct{})
	go func() { r3, _ := l.Acquire(ctx, "k", 1); r3(); close(done) }()
	select {
	case <-done:
		t.Fatal("a double-release must not leave the slot permanently free")
	case <-time.After(50 * time.Millisecond):
	}
	r2()
	<-done
}
