package vlm

import "testing"

// TestModelRuntimeRefreshConfigNoChange verifies that refreshing with the same
// configured caps is a no-op: it must not churn state or clobber any adaptive
// downscaling that is legitimately in effect.
func TestModelRuntimeRefreshConfigNoChange(t *testing.T) {
	rt := newModelRuntime("refresh-nochange", 2, 0)
	if got := rt.configuredLimit.Load(); got != 2 {
		t.Fatalf("configuredLimit = %d, want 2", got)
	}
	if got := rt.effectiveLimit.Load(); got != 2 {
		t.Fatalf("effectiveLimit = %d, want 2", got)
	}
	rt.refreshConfig(2, 0)
	if rt.configuredLimit.Load() != 2 || rt.effectiveLimit.Load() != 2 {
		t.Fatalf("no-change refresh mutated state: cfg=%d eff=%d",
			rt.configuredLimit.Load(), rt.effectiveLimit.Load())
	}
}

// TestModelRuntimeRefreshConfigIncrease verifies a raised configured limit lifts
// the effective ceiling immediately and clears a stuck "unavailable" cooldown,
// so a capacity bump applies on the very next request.
func TestModelRuntimeRefreshConfigIncrease(t *testing.T) {
	rt := newModelRuntime("refresh-up", 1, 0)
	rt.effectiveLimit.Store(1)
	rt.available.Store(false)
	rt.refreshConfig(4, 0)
	if rt.configuredLimit.Load() != 4 {
		t.Fatalf("configuredLimit = %d, want 4", rt.configuredLimit.Load())
	}
	if rt.effectiveLimit.Load() != 4 {
		t.Fatalf("effectiveLimit = %d, want 4 after increase", rt.effectiveLimit.Load())
	}
	if !rt.available.Load() {
		t.Fatalf("available should be re-enabled after a capacity increase")
	}
}

// TestModelRuntimeRefreshConfigDecrease verifies a lowered configured limit
// clamps the effective ceiling down so the scheduler stops over-admitting;
// in-flight permits above the new cap drain naturally as requests complete.
func TestModelRuntimeRefreshConfigDecrease(t *testing.T) {
	rt := newModelRuntime("refresh-down", 4, 0)
	rt.refreshConfig(2, 0)
	if rt.configuredLimit.Load() != 2 {
		t.Fatalf("configuredLimit = %d, want 2", rt.configuredLimit.Load())
	}
	if rt.effectiveLimit.Load() != 2 {
		t.Fatalf("effectiveLimit = %d, want 2 after decrease", rt.effectiveLimit.Load())
	}
}
