package chunker

import (
	"strings"
	"testing"
)

func TestSplitByHeadings_BasicSections(t *testing.T) {
	// Each section is intentionally larger than the merge-target (≈
	// ChunkSize/2) so the post-split coalesce pass leaves them as distinct
	// chunks. We're testing per-section emission + breadcrumb here, not
	// merging.
	body := strings.Repeat("Lorem ipsum dolor sit amet consectetur adipiscing elit. ", 4)
	doc := "# Top\n" + body + "\n\n## Section A\n" + body + "\n\n## Section B\n" + body + "\n\n## Section C\n" + body
	cfg := SplitterConfig{ChunkSize: 300, ChunkOverlap: 0}
	chunks := splitByHeadingsImpl(doc, cfg, nil)
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
		if strings.Contains(c.Content, "## Section B") && strings.Contains(c.Content, "Lorem ipsum") {
			found = true
		}
	}
	if !found {
		t.Error("no chunk contains Section B with its body")
	}
}

func TestSplitByHeadings_FallsThroughForUnstructuredDoc(t *testing.T) {
	doc := "Just a plain paragraph without any headings at all in this text."
	cfg := SplitterConfig{ChunkSize: 200, ChunkOverlap: 0}
	chunks := splitByHeadingsImpl(doc, cfg, nil)
	// no headings → falls through to SplitText, which keeps the whole thing
	if len(chunks) != 1 {
		t.Errorf("expected fallthrough single chunk, got %d", len(chunks))
	}
}

func TestSplitByHeadings_LargeSectionRecursesIntoLegacy(t *testing.T) {
	body := strings.Repeat("This is a long sentence repeated many times. ", 50)
	doc := "# Top\n## Big\n" + body
	cfg := SplitterConfig{ChunkSize: 300, ChunkOverlap: 30, Separators: []string{". "}}
	chunks := splitByHeadingsImpl(doc, cfg, nil)
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
	// Sized so each section stays its own chunk after the tiny-section
	// coalesce pass — we're verifying breadcrumb assignment per section,
	// not the merge behavior.
	body := strings.Repeat("Lorem ipsum dolor sit amet consectetur adipiscing elit. ", 4)
	doc := "# Chapter 1\n" + body + "\n\n## Section A\n" + body + "\n\n## Section B\n" + body
	cfg := SplitterConfig{ChunkSize: 300, ChunkOverlap: 0}
	chunks := splitByHeadingsImpl(doc, cfg, nil)
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
	chunks := splitByHeadingsImpl(doc, cfg, nil)
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
	chunks := splitByHeadingsImpl(doc, cfg, nil)
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
	chunks := splitByHeadingsImpl(doc, cfg, nil)
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

// TestSplitByHeadings_CoalescesTinyAdjacentSections covers the FAQ /
// install-log case where a parent heading hosts many short sub-sections.
// Without merging, each `##` becomes its own <50-char chunk and the
// validator rejects the tier with "too many tiny chunks". After merging,
// they collapse into a small number of properly-sized chunks while still
// surfacing the shared parent breadcrumb.
func TestSplitByHeadings_CoalescesTinyAdjacentSections(t *testing.T) {
	doc := `# Install Log

## Docker镜像
使用 daocloud 部署 v0.3.1。

## 前端老版本
浏览器缓存了旧前端资源。

## 登录报错
ERROR: column missing.

## 解析失败
embedding 表缺列。`
	cfg := SplitterConfig{ChunkSize: 500, ChunkOverlap: 0}
	chunks := splitByHeadingsImpl(doc, cfg, nil)
	if len(chunks) == 0 {
		t.Fatal("expected at least one chunk")
	}
	if len(chunks) >= 5 {
		t.Errorf("expected coalesce to produce <5 chunks, got %d", len(chunks))
	}
	// Every merged chunk must carry the shared parent in its breadcrumb so
	// retrieval can still answer "what document is this from".
	for i, c := range chunks {
		if !strings.Contains(c.ContextHeader, "# Install Log") {
			t.Errorf("chunk %d missing parent H1 in breadcrumb: %q", i, c.ContextHeader)
		}
	}
	// All four sub-section headings must remain visible somewhere in the
	// merged content (heading_splitter keeps the heading line as part of
	// each section's Content).
	for _, h := range []string{"## Docker镜像", "## 前端老版本", "## 登录报错", "## 解析失败"} {
		seen := false
		for _, c := range chunks {
			if strings.Contains(c.Content, h) {
				seen = true
				break
			}
		}
		if !seen {
			t.Errorf("merged chunks should still contain heading %q somewhere", h)
		}
	}
}

// TestSplitByHeadings_CoalescePreservesPositionInvariant guards the
// End-Start == len([]rune(Content)) invariant after merging. Adjacent
// chunks (cur.End == next.Start) must concatenate cleanly; the merge must
// refuse to combine non-adjacent chunks (e.g. legacy sub-chunks from an
// oversized section that overlap).
func TestSplitByHeadings_CoalescePreservesPositionInvariant(t *testing.T) {
	doc := `# Top

## A
short A.

## B
short B.

## C
short C.`
	cfg := SplitterConfig{ChunkSize: 500, ChunkOverlap: 0}
	chunks := splitByHeadingsImpl(doc, cfg, nil)
	docRunes := []rune(doc)
	for i, c := range chunks {
		contentRuneLen := len([]rune(c.Content))
		if c.End-c.Start != contentRuneLen {
			t.Errorf("chunk %d: End-Start(%d) != content_runes(%d) after merge",
				i, c.End-c.Start, contentRuneLen)
		}
		if c.Start >= 0 && c.End <= len(docRunes) {
			if string(docRunes[c.Start:c.End]) != c.Content {
				t.Errorf("chunk %d: source[Start:End] != Content after merge", i)
			}
		}
	}
}

// TestSplitByHeadings_CoalesceRespectsChunkSize ensures the merge target
// stays within the ChunkSize budget — the validator caps oversize chunks
// at 2x and we should never approach that line via merging.
func TestSplitByHeadings_CoalesceRespectsChunkSize(t *testing.T) {
	const sections = 30
	var sb strings.Builder
	sb.WriteString("# Doc\n")
	for i := 0; i < sections; i++ {
		sb.WriteString("\n## Section ")
		sb.WriteString(strings.Repeat("X", 1)) // unique-ish heading
		sb.WriteString("\nshort body line.\n")
	}
	cfg := SplitterConfig{ChunkSize: 200, ChunkOverlap: 0}
	chunks := splitByHeadingsImpl(sb.String(), cfg, nil)
	for i, c := range chunks {
		if l := len([]rune(c.Content)); l > cfg.ChunkSize {
			t.Errorf("chunk %d exceeds ChunkSize: %d > %d", i, l, cfg.ChunkSize)
		}
	}
}

// TestCommonHeadingPrefix exercises the breadcrumb-prefix helper directly.
func TestCommonHeadingPrefix(t *testing.T) {
	cases := []struct {
		a, b, want string
	}{
		{"# Top\n## A", "# Top\n## B", "# Top"},
		{"# Top", "# Top", "# Top"},
		{"# X", "# Y", ""},
		{"# Top\n## A\n### x", "# Top\n## A\n### y", "# Top\n## A"},
		{"", "# Top", ""},
	}
	for _, tc := range cases {
		got := commonHeadingPrefix(tc.a, tc.b)
		if got != tc.want {
			t.Errorf("commonHeadingPrefix(%q, %q) = %q, want %q", tc.a, tc.b, got, tc.want)
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
	chunks := splitByHeadingsImpl(doc, cfg, nil)
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
