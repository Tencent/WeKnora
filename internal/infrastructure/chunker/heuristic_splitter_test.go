package chunker

import (
	"strings"
	"testing"
)

func TestSplitByHeuristics_FormFeedBoundary(t *testing.T) {
	doc := strings.Repeat("page one body text. ", 30) + "\f" + strings.Repeat("page two body. ", 30)
	cfg := SplitterConfig{ChunkSize: 400, ChunkOverlap: 20, Separators: []string{". "}}
	chunks := splitByHeuristicsImpl(doc, cfg)
	if len(chunks) < 2 {
		t.Fatalf("form feed should produce ≥2 chunks, got %d", len(chunks))
	}
}

func TestSplitByHeuristics_NumberedSections(t *testing.T) {
	body := strings.Repeat("body sentence. ", 8)
	doc := "1. Introduction\n" + body + "\n\n2. Methods\n" + body + "\n\n3. Results\n" + body
	cfg := SplitterConfig{ChunkSize: 200, ChunkOverlap: 20, Separators: []string{". "}}
	chunks := splitByHeuristicsImpl(doc, cfg)
	if len(chunks) < 2 {
		t.Fatalf("numbered sections should split: got %d chunks", len(chunks))
	}
}

func TestSplitByHeuristics_GermanChapterMarkers(t *testing.T) {
	body := strings.Repeat("Beispieltext. ", 10)
	doc := "Kapitel 1: Einführung\n" + body + "\n\nKapitel 2: Hauptteil\n" + body
	cfg := SplitterConfig{ChunkSize: 200, ChunkOverlap: 20, Separators: []string{". "}}
	chunks := splitByHeuristicsImpl(doc, cfg)
	if len(chunks) < 2 {
		t.Fatalf("German chapter markers should split: got %d", len(chunks))
	}
}

func TestSplitByHeuristics_ChineseChapterMarkers(t *testing.T) {
	body := strings.Repeat("内容内容内容。", 60)
	doc := "第一章 引言\n" + body + "\n\n第二章 方法\n" + body
	cfg := SplitterConfig{ChunkSize: 200, ChunkOverlap: 20, Separators: []string{"。"}, Languages: []string{LangChinese}}
	chunks := splitByHeuristicsImpl(doc, cfg)
	if len(chunks) < 2 {
		t.Fatalf("Chinese chapter markers should split: got %d", len(chunks))
	}
}

func TestSplitByHeuristics_FallsThroughForUnstructuredDoc(t *testing.T) {
	doc := strings.Repeat("plain prose without structure. ", 5)
	cfg := SplitterConfig{ChunkSize: 1000, ChunkOverlap: 20}
	chunks := splitByHeuristicsImpl(doc, cfg)
	if len(chunks) != 1 {
		t.Errorf("unstructured short doc should be one chunk, got %d", len(chunks))
	}
}

func TestSplitByHeuristics_OversizeBlockRecursesIntoLegacy(t *testing.T) {
	huge := strings.Repeat("This is a long sentence. ", 200) // ~5000 chars
	doc := "1. Intro\n" + huge
	cfg := SplitterConfig{ChunkSize: 500, ChunkOverlap: 50, Separators: []string{". "}}
	chunks := splitByHeuristicsImpl(doc, cfg)
	if len(chunks) < 5 {
		t.Errorf("oversize block should produce many sub-chunks, got %d", len(chunks))
	}
	// No single chunk should massively exceed the budget.
	for i, c := range chunks {
		if len([]rune(c.Content)) > 2*cfg.ChunkSize {
			t.Errorf("chunk %d exceeds 2x size: %d runes", i, len([]rune(c.Content)))
		}
	}
}

func TestSplitByHeuristics_BoundariesAreOrdered(t *testing.T) {
	doc := "Kapitel 1: A\nbody\n\n---\n\n2. Section B\nbody\n\nPage 3 of 10\n\n第三章 C\nbody"
	bounds := findHeuristicBoundaries(doc, nil)
	if len(bounds) < 2 {
		t.Fatalf("expected multiple boundaries, got %d", len(bounds))
	}
	for i := 1; i < len(bounds); i++ {
		if bounds[i].runeStart < bounds[i-1].runeStart {
			t.Errorf("bounds not sorted: %d before %d", bounds[i].runeStart, bounds[i-1].runeStart)
		}
	}
}

func TestSplitByHeuristics_EmptyText(t *testing.T) {
	if got := splitByHeuristicsImpl("", DefaultConfig()); got != nil {
		t.Errorf("empty doc should be nil, got %v", got)
	}
}

// Regression: applyOverlapAligned previously included curEnd itself in its
// boundary search, and curEnd is always one of the bounds (the bin-packer
// flushes at boundary positions). The function therefore always returned
// curEnd, producing zero overlap regardless of cfg.ChunkOverlap.
func TestSplitByHeuristics_OverlapActuallyOverlaps(t *testing.T) {
	// Build many small numbered sections so the bin-packer flushes mid-doc
	// with at least one earlier boundary inside the overlap window.
	var sb strings.Builder
	for i := 1; i <= 12; i++ {
		sb.WriteString("\n\n")
		sb.WriteByte(byte('0' + i%10))
		sb.WriteString(". ")
		sb.WriteString(strings.Repeat("alpha beta gamma. ", 4)) // ~72 chars / section
	}
	doc := sb.String()

	cfg := SplitterConfig{ChunkSize: 200, ChunkOverlap: 80, Separators: []string{". "}}
	chunks := splitByHeuristicsImpl(doc, cfg)
	if len(chunks) < 2 {
		t.Fatalf("need >=2 chunks to test overlap, got %d", len(chunks))
	}

	// At least one consecutive chunk pair must share a non-trivial suffix /
	// prefix. We don't require *every* pair to overlap (oversize blocks
	// short-circuit through legacy and reset chunkStart), but at least one
	// regular flush boundary should produce real overlap.
	saw := false
	for i := 1; i < len(chunks); i++ {
		prev := strings.TrimSpace(chunks[i-1].Content)
		cur := strings.TrimSpace(chunks[i].Content)
		// Walk back the longest suffix of prev that prefixes cur.
		match := 0
		maxScan := len(prev)
		if len(cur) < maxScan {
			maxScan = len(cur)
		}
		for n := 1; n <= maxScan; n++ {
			if strings.HasPrefix(cur, prev[len(prev)-n:]) {
				match = n
			}
		}
		if match >= 20 {
			saw = true
			break
		}
	}
	if !saw {
		t.Fatalf("expected at least one chunk pair to overlap by >=20 chars, none did. chunk sizes: %v",
			chunkLengths(chunks))
	}
}

func chunkLengths(chunks []Chunk) []int {
	out := make([]int, len(chunks))
	for i, c := range chunks {
		out[i] = len([]rune(c.Content))
	}
	return out
}
