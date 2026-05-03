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

// TestSplit_AndDiagnostics_AgreeOnChunks ensures Split (no diagnostics)
// and SplitWithDiagnostics produce the same chunk set for a given input.
// They run independent loops as of the post-audit refactor — this test
// is the regression wall against them drifting.
func TestSplit_AndDiagnostics_AgreeOnChunks(t *testing.T) {
	text := "para one.\n\npara two.\n\npara three."
	cfg := SplitterConfig{ChunkSize: 100, ChunkOverlap: 10}
	a := Split(text, cfg)
	b, diag := SplitWithDiagnostics(text, cfg)
	if len(a) != len(b) {
		t.Fatalf("chunk count disagrees: Split=%d Diagnostics=%d", len(a), len(b))
	}
	for i := range a {
		if a[i].Content != b[i].Content || a[i].Start != b[i].Start || a[i].End != b[i].End {
			t.Errorf("chunk %d differs:\n  Split: %+v\n  Diag : %+v", i, a[i], b[i])
		}
	}
	if diag == nil {
		t.Error("diagnostics must not be nil")
	}
}

// TestSplitWithDiagnostics_ProfileSetForAuto verifies that auto-strategy
// returns the DocProfile that drove tier selection — required by the
// preview endpoint to avoid double-profiling.
func TestSplitWithDiagnostics_ProfileSetForAuto(t *testing.T) {
	doc := "# Top\nintro.\n\n## A\nbody A.\n\n## B\nbody B."
	_, diag := SplitWithDiagnostics(doc, SplitterConfig{ChunkSize: 200, Strategy: StrategyAuto})
	if diag.Profile == nil {
		t.Fatal("auto strategy must populate diag.Profile")
	}
	if diag.Profile.MdHeadingTotal == 0 {
		t.Errorf("profile should have detected headings, got %+v", diag.Profile)
	}
}

// TestSplitWithDiagnostics_ProfileNilForExplicit verifies the inverse:
// explicit strategies bypass profiling and leave Profile nil so the
// preview handler knows to materialize one if it needs stats.
func TestSplitWithDiagnostics_ProfileNilForExplicit(t *testing.T) {
	for _, strat := range []string{StrategyHeading, StrategyHeuristic, StrategyRecursive, StrategyLegacy} {
		t.Run(strat, func(t *testing.T) {
			_, diag := SplitWithDiagnostics("plain text", SplitterConfig{ChunkSize: 200, Strategy: strat})
			if diag.Profile != nil {
				t.Errorf("strategy %q should leave Profile nil, got %+v", strat, diag.Profile)
			}
		})
	}
}
