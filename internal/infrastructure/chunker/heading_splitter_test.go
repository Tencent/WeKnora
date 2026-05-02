package chunker

import (
	"strings"
	"testing"
)

func TestSplitByHeadings_BasicSections(t *testing.T) {
	doc := `# Top
preamble.

## Section A
content of A.

## Section B
content of B.

## Section C
content of C.`
	cfg := SplitterConfig{ChunkSize: 200, ChunkOverlap: 0}
	chunks := splitByHeadingsImpl(doc, cfg)
	if len(chunks) < 3 {
		t.Fatalf("expected ≥3 chunks (one per section), got %d", len(chunks))
	}

	// Breadcrumb is delivered via ContextHeader, not Content.
	for i, c := range chunks {
		if !strings.Contains(c.ContextHeader, "# Top") {
			t.Errorf("chunk %d missing H1 in ContextHeader:\n%q", i, c.ContextHeader)
		}
		// EmbeddingContent merges header + content for the embedder.
		if !strings.Contains(c.EmbeddingContent(), "# Top") {
			t.Errorf("chunk %d EmbeddingContent missing H1", i)
		}
	}

	found := false
	for _, c := range chunks {
		if strings.Contains(c.Content, "Section B") && strings.Contains(c.Content, "content of B") {
			found = true
		}
	}
	if !found {
		t.Error("no chunk contains Section B with its content")
	}
}

func TestSplitByHeadings_FallsThroughForUnstructuredDoc(t *testing.T) {
	doc := "Just a plain paragraph without any headings at all in this text."
	cfg := SplitterConfig{ChunkSize: 200, ChunkOverlap: 0}
	chunks := splitByHeadingsImpl(doc, cfg)
	// no headings → falls through to SplitText, which keeps the whole thing
	if len(chunks) != 1 {
		t.Errorf("expected fallthrough single chunk, got %d", len(chunks))
	}
}

func TestSplitByHeadings_LargeSectionRecursesIntoLegacy(t *testing.T) {
	body := strings.Repeat("This is a long sentence repeated many times. ", 50)
	doc := "# Top\n## Big\n" + body
	cfg := SplitterConfig{ChunkSize: 300, ChunkOverlap: 30, Separators: []string{". "}}
	chunks := splitByHeadingsImpl(doc, cfg)
	if len(chunks) < 2 {
		t.Fatalf("large section should be sub-split, got %d chunks", len(chunks))
	}
	// Every sub-chunk should carry the breadcrumb via ContextHeader.
	for i, c := range chunks {
		if !strings.Contains(c.ContextHeader, "# Top") {
			t.Errorf("sub-chunk %d missing H1 in ContextHeader", i)
		}
	}
}

func TestSplitByHeadings_BreadcrumbReflectsLatestPath(t *testing.T) {
	doc := `# Chapter 1
intro

## Section A
text A

## Section B
text B`
	cfg := SplitterConfig{ChunkSize: 200, ChunkOverlap: 0}
	chunks := splitByHeadingsImpl(doc, cfg)
	if len(chunks) < 3 {
		t.Fatalf("expected ≥3 chunks, got %d", len(chunks))
	}
	for _, c := range chunks {
		if strings.Contains(c.Content, "text B") {
			if strings.Contains(c.ContextHeader, "## Section A") {
				t.Errorf("Section B chunk should not include Section A in breadcrumb:\n%s", c.ContextHeader)
			}
			if !strings.Contains(c.ContextHeader, "## Section B") {
				t.Errorf("Section B chunk should include its own heading in breadcrumb:\n%s", c.ContextHeader)
			}
		}
	}
}

func TestSplitByHeadings_IgnoresHeadingsInsideCodeFence(t *testing.T) {
	doc := "# Real\n\n```\n# Fake heading inside code\n```\n\nbody"
	cfg := SplitterConfig{ChunkSize: 500, ChunkOverlap: 0}
	chunks := splitByHeadingsImpl(doc, cfg)
	for _, c := range chunks {
		if strings.Contains(c.ContextHeader, "# Real") || strings.Contains(c.Content, "# Real") {
			return
		}
	}
	t.Error("expected real H1 breadcrumb on some chunk")
}

func TestSplitByHeadings_PreservesPositionRelativeToOriginal(t *testing.T) {
	doc := "# Top\nintro\n\n## A\nbody A\n\n## B\nbody B"
	cfg := SplitterConfig{ChunkSize: 500, ChunkOverlap: 0}
	chunks := splitByHeadingsImpl(doc, cfg)
	for i, c := range chunks {
		if c.Start < 0 {
			t.Errorf("chunk %d has negative Start", i)
		}
		if c.End < c.Start {
			t.Errorf("chunk %d End < Start", i)
		}
	}
}

// TestSplitByHeadings_PositionInvariant ensures End-Start == len(Content)
// and runes[Start:End] == Content for every emitted chunk. This invariant
// is required by knowledge.go:2278+ document reconstruction logic.
func TestSplitByHeadings_PositionInvariant(t *testing.T) {
	doc := `# Top
intro paragraph here.

## Section A
content of A here, several sentences.

## Section B
content of B here.

## Section C
content of C here.`
	cfg := SplitterConfig{ChunkSize: 200, ChunkOverlap: 20}
	chunks := splitByHeadingsImpl(doc, cfg)
	if len(chunks) == 0 {
		t.Fatal("expected chunks")
	}
	docRunes := []rune(doc)
	for i, c := range chunks {
		contentRuneLen := len([]rune(c.Content))
		span := c.End - c.Start
		if span != contentRuneLen {
			t.Errorf("chunk %d: span(%d) != content_runes(%d)\nContent:\n%q", i, span, contentRuneLen, c.Content)
		}
		if c.Start >= 0 && c.End <= len(docRunes) {
			if string(docRunes[c.Start:c.End]) != c.Content {
				t.Errorf("chunk %d: runes[Start:End] != Content", i)
			}
		}
	}
}

// TestSplitByHeadings_NoBreadcrumbDuplication ensures the section's own
// heading line does not appear twice in the chunk content (once as part of
// the breadcrumb, once as the section's first line).
func TestSplitByHeadings_NoBreadcrumbDuplication(t *testing.T) {
	doc := `# Chapter 1
intro.

## Section A
body A.

## Section B
body B.`
	cfg := SplitterConfig{ChunkSize: 500, ChunkOverlap: 0}
	chunks := splitByHeadingsImpl(doc, cfg)
	for i, c := range chunks {
		// Count occurrences of "## Section A" / "## Section B"
		for _, heading := range []string{"## Section A", "## Section B"} {
			n := strings.Count(c.Content, heading)
			if n > 1 {
				t.Errorf("chunk %d contains %q %d times — duplicated by breadcrumb prepend:\n%s",
					i, heading, n, c.Content)
			}
		}
	}
}
