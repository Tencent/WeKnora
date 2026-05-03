package chunker

import (
	"strings"
	"testing"
)

func TestSplitWithDiagnostics_LegacyStrategy_ReportsLegacyTier(t *testing.T) {
	// Splittable input so the validator accepts the legacy output cleanly.
	text := strings.Repeat("Hello world.\n\nNext paragraph here.\n\n", 50)
	cfg := SplitterConfig{ChunkSize: 200, ChunkOverlap: 20, Separators: []string{"\n\n", "\n"}, Strategy: StrategyLegacy}
	chunks, diag := SplitWithDiagnostics(text, cfg)
	if len(chunks) == 0 {
		t.Fatal("expected chunks")
	}
	if diag.SelectedTier != TierLegacy {
		t.Errorf("expected SelectedTier=legacy, got %s", diag.SelectedTier)
	}
	if len(diag.TierChain) != 1 || diag.TierChain[0] != TierLegacy {
		t.Errorf("expected single-tier chain [legacy], got %v", diag.TierChain)
	}
	if len(diag.Rejected) != 0 {
		t.Errorf("expected no rejections for splittable input, got %v", diag.Rejected)
	}
}

func TestSplitWithDiagnostics_AutoOnHeadingDoc_PicksHeading(t *testing.T) {
	doc := strings.Repeat("# Top\nintro paragraph here.\n\n## Section A\nbody A here.\n\n## Section B\nbody B here.\n\n## Section C\nbody C here.\n\n", 1)
	cfg := SplitterConfig{ChunkSize: 300, ChunkOverlap: 30, Strategy: StrategyAuto}
	_, diag := SplitWithDiagnostics(doc, cfg)
	if len(diag.TierChain) == 0 {
		t.Fatal("expected non-empty tier chain")
	}
	// Heading tier should be tried first for this doc.
	if diag.TierChain[0] != TierHeading {
		t.Errorf("expected heading tier first, got chain %v", diag.TierChain)
	}
}

func TestSplitWithDiagnostics_EmptyText(t *testing.T) {
	chunks, diag := SplitWithDiagnostics("", DefaultConfig())
	if chunks != nil {
		t.Errorf("expected nil chunks for empty text, got %v", chunks)
	}
	if diag == nil {
		t.Fatal("diag must never be nil")
	}
}

func TestSplit_DelegatesToSplitWithDiagnostics(t *testing.T) {
	text := "para one.\n\npara two.\n\npara three."
	cfg := SplitterConfig{ChunkSize: 100, ChunkOverlap: 10}
	a := Split(text, cfg)
	b, _ := SplitWithDiagnostics(text, cfg)
	if len(a) != len(b) {
		t.Errorf("Split and SplitWithDiagnostics disagree: %d vs %d", len(a), len(b))
	}
}
