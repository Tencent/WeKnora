package chunker

import (
	"strconv"
	"strings"
	"testing"
)

// buildOCRStrayBracketDoc models the OCR failure mode behind this regression:
// a full-width "【公章】" whose opening bracket was mis-read as the half-width
// '[' (closing bracket lost), followed later by an unrelated half-width
// "](...)" fragment. CommonMark forbids a link's text/destination from
// spanning a blank line, but the previous unbounded
// `\[[^\]]*\]\([^)]+\)` joined the stray '[' to the distant '](' and
// swallowed the entire document as one protected atomic span.
func buildOCRStrayBracketDoc() string {
	var sb strings.Builder
	sb.WriteString("[公章】\n\n")
	for i := 0; i < 60; i++ {
		sb.WriteString("这是第")
		sb.WriteString(strconv.Itoa(i))
		sb.WriteString("段用于测试切分的中文内容，包含常见标点符号。\n\n")
	}
	sb.WriteString("第三段（北京)\n\n")
	sb.WriteString("[引用](来源)")
	return sb.String()
}

func maxChunkRunes(chunks []Chunk) int {
	best := 0
	for _, c := range chunks {
		if n := len([]rune(c.Content)); n > best {
			best = n
		}
	}
	return best
}

// TestSplitText_OCRStrayBracketDoesNotSwallowDocument is the core regression:
// before the fix the malformed '[' turned the whole document into a single
// protected span (~1700 runes) and SplitText returned one oversized chunk.
func TestSplitText_OCRStrayBracketDoesNotSwallowDocument(t *testing.T) {
	doc := buildOCRStrayBracketDoc()
	if !strings.Contains(doc, "[公章】") || !strings.Contains(doc, "](来源)") {
		t.Fatalf("fixture missing malformed-bracket markers")
	}
	const chunkSize = 128
	cfg := SplitterConfig{ChunkSize: chunkSize, ChunkOverlap: 0, Separators: []string{"\n\n", "\n", "。"}}

	chunks := SplitText(doc, cfg)
	t.Logf("doc=%d runes chunkSize=%d -> chunks=%d max=%d runes",
		len([]rune(doc)), chunkSize, len(chunks), maxChunkRunes(chunks))
	if len(chunks) < 2 {
		t.Fatalf("expected multiple chunks, got %d (max=%d runes)", len(chunks), maxChunkRunes(chunks))
	}
	if got := maxChunkRunes(chunks); got > 2*chunkSize {
		t.Fatalf("max chunk %d runes exceeds 2*chunkSize=%d: stray '[' swallowed the document", got, 2*chunkSize)
	}
}

// TestSplit_OCRStrayBracketDoesNotSwallowDocument exercises the strategy-aware
// entry point with the legacy tier pinned, so the protected-pattern behaviour
// is what is under test rather than the profiler's tier selection.
func TestSplit_OCRStrayBracketDoesNotSwallowDocument(t *testing.T) {
	doc := buildOCRStrayBracketDoc()
	const chunkSize = 128
	cfg := SplitterConfig{
		ChunkSize:    chunkSize,
		ChunkOverlap: 0,
		Separators:   []string{"\n\n", "\n", "。"},
		Strategy:     StrategyLegacy,
	}

	chunks := Split(doc, cfg)
	if len(chunks) < 2 {
		t.Fatalf("expected multiple chunks, got %d (max=%d runes)", len(chunks), maxChunkRunes(chunks))
	}
	if got := maxChunkRunes(chunks); got > 2*chunkSize {
		t.Fatalf("max chunk %d runes exceeds 2*chunkSize=%d: stray '[' swallowed the document", got, 2*chunkSize)
	}
}

// TestProtectedSpans_NoCrossLineLinkOrImage asserts no protected span may
// contain a newline for the malformed document: link text and destination
// cannot span a blank line (CommonMark).
func TestProtectedSpans_NoCrossLineLinkOrImage(t *testing.T) {
	doc := buildOCRStrayBracketDoc()
	for _, s := range protectedSpans(doc) {
		seg := doc[s.start:s.end]
		if strings.Contains(seg, "\n") {
			t.Fatalf("protected span spans a newline (%d runes): %q", len([]rune(seg)), seg)
		}
	}
}

// TestProtectedSpans_MarkdownLinksAndImagesStillProtected is the positive
// guard: the newline-bounded patterns must keep recognising well-formed
// single-line Markdown links and images.
func TestProtectedSpans_MarkdownLinksAndImagesStillProtected(t *testing.T) {
	cases := []string{
		"![alt](http://x/y.png)",
		"[文字](http://x.com)",
		`[link](url "title")`,
		"![](resource://abc)",
	}
	for _, c := range cases {
		spans := protectedSpans(c)
		if len(spans) == 0 {
			t.Errorf("expected %q to be protected, got no span", c)
			continue
		}
		if spans[0].start != 0 || spans[0].end != len(c) {
			t.Errorf("%q: protected span = [%d,%d), want [0,%d)", c, spans[0].start, spans[0].end, len(c))
		}
	}
}

// TestProtectedSpans_LiteralCrossLineExample documents that an isolated
// malformed bracket pair with NO complete '](...)' construct is never treated
// as a protected link/image region.
func TestProtectedSpans_MultilineLinkIntentionallyUnprotected(t *testing.T) {
	// CommonMark allows a single soft line break in link text. The protected
	// patterns still refuse it so a stray OCR '[' cannot swallow a paragraph.
	doc := "[link\ntext](http://example.com)"
	if spans := protectedSpans(doc); len(spans) != 0 {
		t.Fatalf("expected multiline link to stay unprotected, got %v", spans)
	}
}

func TestProtectedSpans_LiteralCrossLineExample(t *testing.T) {
	doc := "[公章】\n\n第一段。\n\n第二段。\n\n第三段（北京)"
	if spans := protectedSpans(doc); len(spans) != 0 {
		t.Fatalf("expected no protected span, got %v", spans)
	}
}
