package embedding

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

// The per-input character limit must come out of the same catalog + spec
// overlay resolution the protocol client uses, so ingestion sizing and the
// batching layer agree on the budget.
func TestInputCharLimit_FromSpecCompat(t *testing.T) {
	limit := 1024
	cfg := Config{
		ModelName: "test-embedder",
		BaseURL:   "https://example.invalid/v1",
		Provider:  "generic",
		Spec: &types.ModelSpecOverride{
			Compat: map[string]any{"max_input_chars": float64(limit)},
		},
	}
	if got := InputCharLimit(cfg); got != limit {
		t.Fatalf("InputCharLimit = %d, want %d", got, limit)
	}
}

func TestInputCharLimit_UnknownWhenUnset(t *testing.T) {
	cfg := Config{ModelName: "test-embedder", BaseURL: "https://example.invalid/v1", Provider: "generic"}
	if got := InputCharLimit(cfg); got != 0 {
		t.Fatalf("InputCharLimit = %d, want 0 when no limit is declared", got)
	}
	if got := InputCharLimit(Config{}); got != 0 {
		t.Fatalf("InputCharLimit = %d, want 0 for an empty config", got)
	}
}
