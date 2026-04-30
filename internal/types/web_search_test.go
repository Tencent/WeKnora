package types

import "testing"

func TestEffectiveWebSearchConfigUsesDefaultsForNilConfig(t *testing.T) {
	cfg := EffectiveWebSearchConfig(nil)

	if cfg.MaxResults != DefaultWebSearchMaxResults {
		t.Fatalf("MaxResults = %d, want %d", cfg.MaxResults, DefaultWebSearchMaxResults)
	}
	if cfg.CompressionMethod != DefaultWebSearchCompressionMethod {
		t.Fatalf("CompressionMethod = %q, want %q", cfg.CompressionMethod, DefaultWebSearchCompressionMethod)
	}
	if cfg.Blacklist == nil {
		t.Fatal("Blacklist = nil, want empty slice")
	}
	if len(cfg.Blacklist) != 0 {
		t.Fatalf("Blacklist length = %d, want 0", len(cfg.Blacklist))
	}
}

func TestEffectiveWebSearchConfigNormalizesZeroValuesWithoutMutatingSource(t *testing.T) {
	source := &WebSearchConfig{
		IncludeDate: true,
	}

	cfg := EffectiveWebSearchConfig(source)

	if cfg == source {
		t.Fatal("EffectiveWebSearchConfig returned original pointer, want normalized copy")
	}
	if cfg.MaxResults != DefaultWebSearchMaxResults {
		t.Fatalf("MaxResults = %d, want %d", cfg.MaxResults, DefaultWebSearchMaxResults)
	}
	if cfg.CompressionMethod != DefaultWebSearchCompressionMethod {
		t.Fatalf("CompressionMethod = %q, want %q", cfg.CompressionMethod, DefaultWebSearchCompressionMethod)
	}
	if cfg.Blacklist == nil {
		t.Fatal("Blacklist = nil, want empty slice")
	}
	if !cfg.IncludeDate {
		t.Fatal("IncludeDate = false, want true")
	}

	if source.MaxResults != 0 {
		t.Fatalf("source MaxResults mutated to %d, want 0", source.MaxResults)
	}
	if source.CompressionMethod != "" {
		t.Fatalf("source CompressionMethod mutated to %q, want empty string", source.CompressionMethod)
	}
	if source.Blacklist != nil {
		t.Fatalf("source Blacklist mutated to non-nil value: %#v", source.Blacklist)
	}
}
