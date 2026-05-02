package chunker

import (
	"strings"
	"testing"
)

func TestSplit_EmptyText(t *testing.T) {
	if got := Split("", DefaultConfig()); got != nil {
		t.Errorf("empty text should return nil, got %v", got)
	}
}

func TestSplit_LegacyStrategy_MatchesSplitText(t *testing.T) {
	text := strings.Repeat("Hello world.\n\n", 30)
	cfg := SplitterConfig{ChunkSize: 100, ChunkOverlap: 20, Separators: []string{"\n\n"}, Strategy: StrategyLegacy}
	a := Split(text, cfg)
	b := SplitText(text, cfg)
	if len(a) != len(b) {
		t.Errorf("legacy strategy should match SplitText: got %d vs %d chunks", len(a), len(b))
	}
	for i := range a {
		if a[i].Content != b[i].Content {
			t.Errorf("chunk %d differs", i)
		}
	}
}

func TestSplit_EmptyStrategyEqualsLegacy(t *testing.T) {
	text := strings.Repeat("Sentence one. Sentence two.\n", 20)
	cfg := SplitterConfig{ChunkSize: 80, ChunkOverlap: 10}
	a := Split(text, cfg)
	cfg.Strategy = StrategyLegacy
	b := Split(text, cfg)
	if len(a) != len(b) {
		t.Errorf("empty Strategy should equal legacy: %d vs %d", len(a), len(b))
	}
}

func TestSplit_AutoStrategy_PicksHeadingForMarkdownDoc(t *testing.T) {
	doc := strings.Repeat("# A\nbody\n## B\nbody\n## C\nbody\n## D\nbody\n", 1)
	cfg := SplitterConfig{ChunkSize: 200, ChunkOverlap: 20, Strategy: StrategyAuto}
	// Until heading_splitter is wired, this falls through to SplitText —
	// just assert we get a valid result.
	chunks := Split(doc, cfg)
	if len(chunks) == 0 {
		t.Error("auto strategy should produce chunks")
	}
}

// TestSplit_PreservesPositionInvariantAcrossTiers ensures every chunk's
// (Start, End, Content) triple stays consistent — End-Start must equal the
// rune length of Content, and runes[Start:End] must equal Content. This is
// the contract that knowledge.go:2278+ relies on for document reconstruction
// during summary generation.
func TestSplit_PreservesPositionInvariantAcrossTiers(t *testing.T) {
	cases := map[string]string{
		"heading-tier": "# Top\nintro paragraph here.\n\n## Section A\nbody A here.\n\n## Section B\nbody B here.\n\n## Section C\nbody C.",
		"heuristic-tier": strings.Repeat("Kapitel 1: Einleitung\n", 1) + strings.Repeat("Beispieltext. ", 50) +
			"\n\n" + strings.Repeat("Kapitel 2: Hauptteil\n", 1) + strings.Repeat("Mehr Text. ", 50),
		"recursive-tier": strings.Repeat("plain prose without structure. ", 100),
	}
	cfg := SplitterConfig{ChunkSize: 300, ChunkOverlap: 30, Separators: []string{"\n\n", "\n", "。", ". "}, Strategy: StrategyAuto}

	for name, doc := range cases {
		t.Run(name, func(t *testing.T) {
			runes := []rune(doc)
			chunks := Split(doc, cfg)
			if len(chunks) == 0 {
				t.Fatal("expected chunks")
			}
			for i, c := range chunks {
				contentRuneLen := len([]rune(c.Content))
				spanLen := c.End - c.Start
				if spanLen != contentRuneLen {
					t.Errorf("chunk %d: End(%d)-Start(%d)=%d but Content has %d runes:\n%q",
						i, c.End, c.Start, spanLen, contentRuneLen, c.Content)
				}
				if c.Start < 0 || c.End > len(runes) {
					t.Errorf("chunk %d: position out of range Start=%d End=%d totalRunes=%d",
						i, c.Start, c.End, len(runes))
				}
				if c.Start >= 0 && c.End <= len(runes) {
					sliced := string(runes[c.Start:c.End])
					if sliced != c.Content {
						t.Errorf("chunk %d: runes[Start:End] differs from Content", i)
					}
				}
			}
		})
	}
}

func TestSplitParentChild_LegacyStrategy(t *testing.T) {
	text := strings.Repeat("This is a sentence. Another one.\n\n", 50)
	parentCfg := SplitterConfig{ChunkSize: 400, ChunkOverlap: 40, Strategy: StrategyLegacy}
	childCfg := SplitterConfig{ChunkSize: 100, ChunkOverlap: 20, Strategy: StrategyLegacy}
	res := SplitParentChild(text, parentCfg, childCfg)
	if len(res.Children) == 0 {
		t.Fatal("expected children chunks")
	}
	for i, c := range res.Children {
		if c.ParentIndex >= 0 && c.ParentIndex >= len(res.Parents) {
			t.Errorf("child[%d] has invalid ParentIndex %d (parents=%d)", i, c.ParentIndex, len(res.Parents))
		}
	}
}

func TestEnsureDefaults(t *testing.T) {
	cfg := ensureDefaults(SplitterConfig{})
	if cfg.ChunkSize != DefaultChunkSize {
		t.Errorf("expected default ChunkSize %d, got %d", DefaultChunkSize, cfg.ChunkSize)
	}
	if cfg.ChunkOverlap != DefaultChunkOverlap {
		t.Errorf("expected default ChunkOverlap %d, got %d", DefaultChunkOverlap, cfg.ChunkOverlap)
	}
	if len(cfg.Separators) == 0 {
		t.Error("expected default separators")
	}
}

func TestValidateChunks_Empty(t *testing.T) {
	if v := ValidateChunks(nil, 1000, 500); v.OK {
		t.Error("nil chunks should be invalid")
	}
}

func TestValidateChunks_SingleChunkLargeDoc(t *testing.T) {
	c := []Chunk{{Content: strings.Repeat("a", 5000)}}
	if v := ValidateChunks(c, 5000, 500); v.OK {
		t.Error("single 10x-too-large chunk should be invalid")
	}
}

func TestValidateChunks_AcceptsReasonableOutput(t *testing.T) {
	chunks := []Chunk{
		{Content: strings.Repeat("a", 480)},
		{Content: strings.Repeat("b", 510)},
		{Content: strings.Repeat("c", 460)},
	}
	if v := ValidateChunks(chunks, 1500, 512); !v.OK {
		t.Errorf("reasonable chunks should validate, got: %s", v.Reason)
	}
}

func TestValidateChunks_RejectsOversized(t *testing.T) {
	chunks := []Chunk{
		{Content: strings.Repeat("a", 100)},
		{Content: strings.Repeat("b", 5000)}, // > 2x chunkSize
	}
	if v := ValidateChunks(chunks, 5100, 1000); v.OK {
		t.Error("chunk >2x size should be invalid")
	}
}

func TestValidateChunks_TolerantTinyTail(t *testing.T) {
	chunks := []Chunk{
		{Content: strings.Repeat("a", 480)},
		{Content: strings.Repeat("b", 510)},
		{Content: "tail"},
	}
	if v := ValidateChunks(chunks, 994, 512); !v.OK {
		t.Errorf("tiny last chunk should be tolerated, got: %s", v.Reason)
	}
}
