package langfuse

import (
	"context"
	"testing"
)

// TestManager_DisabledIsNoop verifies that when the manager is disabled the
// public API is safe to call and produces no side effects (no spans exported,
// no panics).
func TestManager_DisabledIsNoop(t *testing.T) {
	m, err := Init(Config{Enabled: false})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m.Enabled() {
		t.Fatal("expected disabled")
	}
	ctx, trace := m.StartTrace(context.Background(), TraceOptions{Name: "x"})
	trace.Finish(nil, nil)

	_, gen := m.StartGeneration(ctx, GenerationOptions{Name: "g", Model: "m"})
	gen.Finish(nil, nil, nil)

	if err := m.Shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
}
