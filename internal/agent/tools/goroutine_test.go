package tools

import (
	"testing"
	"time"
)

// GoRecovered must contain a panic raised on the goroutine it starts and still
// run the deferred calls fn installed. Without the barrier this test binary
// dies with "test: adapter panicked on a helper goroutine".
func TestGoRecoveredContainsPanicAndRunsDefers(t *testing.T) {
	cleaned := make(chan struct{})
	GoRecovered(catalogTestContext(), func() {
		defer close(cleaned)
		panic("test: adapter panicked on a helper goroutine")
	})
	select {
	case <-cleaned:
	case <-time.After(5 * time.Second):
		t.Fatal("GoRecovered did not run fn")
	}
}

// RecoverGoroutine is the deferred form: it reports the failure through onPanic
// on the panicking goroutine, before the caller's own deferred hooks run.
func TestRecoverGoroutineReportsPanicToCaller(t *testing.T) {
	ctx := catalogTestContext()
	var marked bool
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer RecoverGoroutine(ctx, "test retrieval", func() { marked = true })
		panic("test: adapter panicked on a caller-owned goroutine")
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("RecoverGoroutine did not contain the panic")
	}
	if !marked {
		t.Fatal("RecoverGoroutine did not report the panic to onPanic")
	}
}
