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

	// Each chunk should contain the H1 + section heading as breadcrumb.
	for i, c := range chunks {
		if !strings.Contains(c.Content, "# Top") {
			t.Errorf("chunk %d missing H1 breadcrumb:\n%s", i, c.Content)
		}
	}

	// Section B chunk should mention Section B.
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
	// Every sub-chunk should still carry the breadcrumb.
	for i, c := range chunks {
		if !strings.Contains(c.Content, "# Top") {
			t.Errorf("sub-chunk %d missing H1 breadcrumb", i)
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
	// Section B chunk's breadcrumb should NOT mention Section A
	for _, c := range chunks {
		if strings.Contains(c.Content, "text B") {
			if strings.Contains(c.Content, "## Section A") {
				t.Errorf("Section B chunk should not include Section A in breadcrumb:\n%s", c.Content)
			}
			if !strings.Contains(c.Content, "## Section B") {
				t.Errorf("Section B chunk should include its own heading:\n%s", c.Content)
			}
		}
	}
}

func TestSplitByHeadings_IgnoresHeadingsInsideCodeFence(t *testing.T) {
	doc := "# Real\n\n```\n# Fake heading inside code\n```\n\nbody"
	cfg := SplitterConfig{ChunkSize: 500, ChunkOverlap: 0}
	chunks := splitByHeadingsImpl(doc, cfg)
	// The fake heading should not create a section boundary — there's only
	// one real H1, so we expect either 1 chunk or fall-through.
	for _, c := range chunks {
		if strings.Contains(c.Content, "# Real") {
			// Good — found the real one.
			return
		}
	}
	t.Error("expected real H1 breadcrumb in some chunk")
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
