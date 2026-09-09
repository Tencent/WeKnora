package catalog

import (
	"context"
	"testing"
)

// TestEmbeddedSeedParses is the LOCAL-source guard: the embedded models.json
// must parse and carry the schema's mandatory fields — a broken seed would
// silently disable prefill for every deployment.
func TestEmbeddedSeedParses(t *testing.T) {
	if err := Load(context.Background(), SourceLocal); err != nil {
		t.Fatalf("Load(LOCAL) = %v", err)
	}
	cat := Get()
	if cat == nil {
		t.Fatal("Get() = nil after successful load")
	}
	if cat.Version == "" {
		t.Error("catalog version empty")
	}
	if len(cat.Providers) == 0 {
		t.Fatal("embedded seed has no providers")
	}
	// Spot-check entries the conversion pipeline is expected to produce:
	// openai/o3 must carry thinking levels (api.json effort values).
	o3, ok := LookupModel("openai", "o3")
	if !ok {
		t.Fatal("openai/o3 missing from embedded seed")
	}
	if o3.ContextWindow <= 0 {
		t.Errorf("openai/o3 context_window = %d, want > 0", o3.ContextWindow)
	}
	if o3.Thinking == nil || !o3.Thinking.Supported {
		t.Fatal("openai/o3 thinking missing or unsupported")
	}
	if len(o3.Thinking.Levels) == 0 {
		t.Error("openai/o3 thinking levels empty")
	}
	if o3.Thinking.DefaultLevel == "" {
		t.Error("openai/o3 default_level empty")
	}
}

func TestLookupModel(t *testing.T) {
	if err := Load(context.Background(), SourceLocal); err != nil {
		t.Fatalf("Load(LOCAL) = %v", err)
	}

	t.Run("exact id matches", func(t *testing.T) {
		if _, ok := LookupModel("openai", "o3"); !ok {
			t.Fatal("exact lookup failed for openai/o3")
		}
	})

	t.Run("date suffix normalizes", func(t *testing.T) {
		if _, ok := LookupModel("openai", "o3-2025-04-16"); !ok {
			t.Fatal("normalized lookup failed for o3-2025-04-16")
		}
	})

	t.Run("latest suffix normalizes", func(t *testing.T) {
		if _, ok := LookupModel("openai", "o3-LATEST"); !ok {
			t.Fatal("normalized lookup failed for o3-LATEST (case-insensitive)")
		}
	})

	t.Run("unknown provider misses", func(t *testing.T) {
		if _, ok := LookupModel("nonexistent", "o3"); ok {
			t.Fatal("unknown provider must not match")
		}
	})

	t.Run("unknown model misses", func(t *testing.T) {
		if _, ok := LookupModel("openai", "totally-made-up-model"); ok {
			t.Fatal("unknown model must not match")
		}
	})

	t.Run("empty inputs miss safely", func(t *testing.T) {
		if _, ok := LookupModel("", "o3"); ok {
			t.Fatal("empty provider must not match")
		}
		if _, ok := LookupModel("openai", ""); ok {
			t.Fatal("empty model must not match")
		}
	})
}
