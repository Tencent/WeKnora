package types

import "testing"

// Zero values keep the built-in defaults so an unset config — every existing
// workspace — keeps the historic ranking behavior.
func TestGetEffectiveWebSearchDefaults(t *testing.T) {
	var nilCfg *RetrievalConfig
	if got := nilCfg.GetEffectiveWebSearchBaseScore(); got != DefaultWebSearchBaseScore {
		t.Fatalf("nil config base score = %v, want %v", got, DefaultWebSearchBaseScore)
	}
	if got := nilCfg.GetEffectiveWebSearchSourceWeight(); got != DefaultWebSearchSourceWeight {
		t.Fatalf("nil config source weight = %v, want %v", got, DefaultWebSearchSourceWeight)
	}
	empty := &RetrievalConfig{}
	if got := empty.GetEffectiveWebSearchBaseScore(); got != DefaultWebSearchBaseScore {
		t.Fatalf("empty config base score = %v, want %v", got, DefaultWebSearchBaseScore)
	}
	if got := empty.GetEffectiveWebSearchSourceWeight(); got != DefaultWebSearchSourceWeight {
		t.Fatalf("empty config source weight = %v, want %v", got, DefaultWebSearchSourceWeight)
	}
}

func TestGetEffectiveWebSearchConfigured(t *testing.T) {
	cfg := &RetrievalConfig{WebSearchBaseScore: 0.3, WebSearchSourceWeight: 0.5}
	if got := cfg.GetEffectiveWebSearchBaseScore(); got != 0.3 {
		t.Fatalf("base score = %v, want 0.3", got)
	}
	if got := cfg.GetEffectiveWebSearchSourceWeight(); got != 0.5 {
		t.Fatalf("source weight = %v, want 0.5", got)
	}
}
