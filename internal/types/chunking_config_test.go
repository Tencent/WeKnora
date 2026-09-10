package types

import (
	"encoding/json"
	"testing"
)

// Regression tests for the chunking_config key mismatch between the
// migration DDL defaults ("split_markers") and the Go json tag
// ("separators"): the default must not be silently dropped, and legacy
// rows written with the old key must still be readable.
func TestChunkingConfigUnmarshalFromDDLDefault(t *testing.T) {
	// Exact default JSON now emitted by the migrations
	// (migrations/{paradedb,sqlite,versioned}/000000_init*).
	ddlDefault := `{"chunk_size": 512, "chunk_overlap": 50, "separators": ["\n\n", "\n", "。"], "keep_separator": true}`
	var cfg ChunkingConfig
	if err := json.Unmarshal([]byte(ddlDefault), &cfg); err != nil {
		t.Fatalf("unmarshal DDL default: %v", err)
	}
	if cfg.ChunkSize != 512 || cfg.ChunkOverlap != 50 {
		t.Fatalf("chunk size/overlap lost: %#v", cfg)
	}
	if len(cfg.Separators) != 3 || cfg.Separators[0] != "\n\n" || cfg.Separators[1] != "\n" || cfg.Separators[2] != "。" {
		t.Fatalf("separators not parsed from DDL default, got %#v", cfg.Separators)
	}
}

func TestChunkingConfigUnmarshalLegacySplitMarkersKey(t *testing.T) {
	// Rows created before the fix stored the default under "split_markers".
	legacy := `{"chunk_size": 512, "chunk_overlap": 50, "split_markers": ["\n\n", "\n", "。"], "keep_separator": true}`
	var cfg ChunkingConfig
	if err := json.Unmarshal([]byte(legacy), &cfg); err != nil {
		t.Fatalf("unmarshal legacy default: %v", err)
	}
	if len(cfg.Separators) != 3 || cfg.Separators[0] != "\n\n" || cfg.Separators[2] != "。" {
		t.Fatalf("legacy split_markers not honored, got %#v", cfg.Separators)
	}
}

func TestChunkingConfigUnmarshalExplicitEmptySeparators(t *testing.T) {
	// Explicitly empty separators must stay empty even when a legacy
	// split_markers key is present: the caller asked for no separators.
	payload := `{"chunk_size": 100, "separators": [], "split_markers": ["\n\n"]}`
	var cfg ChunkingConfig
	if err := json.Unmarshal([]byte(payload), &cfg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(cfg.Separators) != 0 {
		t.Fatalf("explicit empty separators overridden by legacy key, got %#v", cfg.Separators)
	}
}

func TestChunkingConfigRoundTripUsesCanonicalKey(t *testing.T) {
	cfg := ChunkingConfig{
		ChunkSize:    256,
		ChunkOverlap: 20,
		Separators:   []string{"\n\n", "。"},
	}
	raw, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var probe map[string]interface{}
	if err := json.Unmarshal(raw, &probe); err != nil {
		t.Fatalf("unmarshal probe: %v", err)
	}
	if _, ok := probe["separators"]; !ok {
		t.Fatalf("marshal did not emit canonical separators key: %s", raw)
	}
	if _, ok := probe["split_markers"]; ok {
		t.Fatalf("marshal must never emit legacy split_markers key: %s", raw)
	}
	var back ChunkingConfig
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatalf("round trip unmarshal: %v", err)
	}
	if len(back.Separators) != 2 {
		t.Fatalf("round trip lost separators: %#v", back.Separators)
	}
}
