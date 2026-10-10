package vlm

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/api"
)

// TestCancellationIsNotEndpointDowntime is the regression for caller-side
// cancellation accounting: the HTTP client wraps a cancelled request's context
// error in api.TransportError{Op: "send request"}, and classifying that purely
// by phase counted every user cancellation as endpoint downtime — enough
// consecutive cancellations tripped the shared breaker against a healthy
// endpoint.
func TestCancellationIsNotEndpointDowntime(t *testing.T) {
	rt := newTestRuntime(t, 10, 0)
	m := &managerVLM{modelID: rt.modelID}

	cancelled := fmt.Errorf("Post %q: %w", "http://ep/v1/chat/completions",
		&api.TransportError{Op: "send request", Err: context.Canceled})

	kind, _, _ := classifyError(cancelled)
	if kind != KindCancelled {
		t.Fatalf("classifyError(canceled transport) = %v, want KindCancelled", kind)
	}

	// Trip-threshold many cancellations in a row must leave the breaker closed
	// and the hard-down counter untouched.
	for i := 0; i < cbTripThreshold; i++ {
		m.handleFailure(rt, cancelled)
	}
	if state := rt.cbState.Load(); state != cbServerUp {
		t.Errorf("circuit state = %d, want cbServerUp: caller cancellations tripped the breaker", state)
	}
	if n := rt.cbConsecHardDown.Load(); n != 0 {
		t.Errorf("cbConsecHardDown = %d, want 0 (cancellations are not hard-downs)", n)
	}
	if !rt.available.Load() {
		t.Error("available should stay true: a cancellation says nothing about provider health")
	}
}

// TestOwnDeadlineIsTimeoutNotHardDown pins the sibling case: OUR request
// deadline (vlmHTTPTimeout) expiring is a slow-endpoint signal that sheds
// load, not a dead-endpoint signal that trips the breaker. The deadline error
// arrives wrapped in the same "send request" transport shell.
func TestOwnDeadlineIsTimeoutNotHardDown(t *testing.T) {
	rt := newTestRuntime(t, 10, 0)
	m := &managerVLM{modelID: rt.modelID}

	deadline := fmt.Errorf("Post %q: %w", "http://ep/v1/chat/completions",
		&api.TransportError{Op: "send request", Err: context.DeadlineExceeded})

	kind, _, _ := classifyError(deadline)
	if kind != KindTimeout {
		t.Fatalf("classifyError(deadline transport) = %v, want KindTimeout", kind)
	}

	for i := 0; i < cbTripThreshold; i++ {
		m.handleFailure(rt, deadline)
	}
	if state := rt.cbState.Load(); state != cbServerUp {
		t.Errorf("circuit state = %d, want cbServerUp: our own request deadline must not trip the breaker", state)
	}
	if rt.effectiveLimit.Load() >= 10 {
		t.Error("a repeated post-admission timeout should shed load (effectiveLimit < configured)")
	}
}

// TestDialTimeoutStaysHardDown guards the flip side: a dial timeout (net's own
// os.ErrDeadlineExceeded, NOT context.DeadlineExceeded) is a genuine
// connection-level failure and must keep tripping the breaker — the black-hole
// endpoint detection depends on it.
func TestDialTimeoutStaysHardDown(t *testing.T) {
	// errors.New with the same TEXT the net package produces: unwrapping it
	// yields a plain error, not context.DeadlineExceeded.
	dialTimeout := errors.New(`Post "http://ep/v1/chat/completions": dial tcp 10.0.0.1:8080: i/o timeout`)

	kind, _, _ := classifyError(dialTimeout)
	if kind != KindHardDown {
		t.Fatalf("classifyError(dial i/o timeout) = %v, want KindHardDown", kind)
	}
	if errors.Is(dialTimeout, context.DeadlineExceeded) {
		t.Fatal("test premise broken: a plain dial-timeout error must not satisfy errors.Is(context.DeadlineExceeded)")
	}
}
