package types

import (
	"context"
	"testing"
)

func TestVLMCallerRoundTrip(t *testing.T) {
	ctx := WithVLMCaller(context.Background(), CallerKBBackground)
	if got := VLMCallerFromContext(ctx); got != CallerKBBackground {
		t.Errorf("caller = %q, want kb_background", got)
	}
}

func TestVLMCallerDefaultUnknown(t *testing.T) {
	if got := VLMCallerFromContext(context.Background()); got != CallerUnknown {
		t.Errorf("default caller = %q, want unknown", got)
	}
}

func TestVLMCallerNilContext(t *testing.T) {
	// A nil-valued context must not panic and must fall back to unknown. It is
	// passed via a variable because staticcheck SA1012 flags a literal nil arg.
	var nilCtx context.Context
	if got := VLMCallerFromContext(nilCtx); got != CallerUnknown {
		t.Errorf("nil context caller = %q, want unknown", got)
	}
}

func TestVLMCallerEmptyNoOp(t *testing.T) {
	base := WithVLMCaller(context.Background(), CallerKBBackground)
	// An empty caller is a no-op: any previously set caller survives.
	same := WithVLMCaller(base, "")
	if got := VLMCallerFromContext(same); got != CallerKBBackground {
		t.Errorf("empty caller overwrote existing mark: %q", got)
	}
	// And a fresh context with an empty caller falls back to unknown.
	fresh := WithVLMCaller(context.Background(), "")
	if got := VLMCallerFromContext(fresh); got != CallerUnknown {
		t.Errorf("fresh empty caller = %q, want unknown", got)
	}
}

func TestVLMCallerCloneDecisionDeclared(t *testing.T) {
	clone, declared := ContextCloneDecision(VLMCallerContextKey)
	if !declared {
		t.Error("VLMCallerContextKey has no clone decision recorded")
	}
	if !clone {
		t.Error("VLMCallerContextKey must survive a context detach (clone=true)")
	}
}
