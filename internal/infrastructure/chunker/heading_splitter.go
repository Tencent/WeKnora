// Package chunker - heading_splitter.go implements Tier 1: Markdown
// heading-aware chunking. Documents with proper heading structure are split
// at heading boundaries and each chunk is prefixed with a breadcrumb of
// active heading context (e.g. "# Chapter 1\n## Section 1.2").
package chunker

import (
	"strings"
	"unicode/utf8"
)

// init wires this implementation into the strategy resolver.
func init() {
	splitByHeadings = splitByHeadingsImpl
}

// headingBoundary marks where a section starts. The first boundary is at
// rune offset 0 (covers any preamble before the first heading), subsequent
// boundaries sit at headings whose level is <= primaryLevel.
type headingBoundary struct {
	runeStart int
	line      string // raw heading line, "" when this is the leading boundary
}

// splitByHeadingsImpl is the Tier-1 implementation. It falls through to the
// legacy splitter when the document has no usable heading structure or when
// the heading split would produce a single section anyway.
//
// profile may be nil; we compute one on demand. When the strategy resolver
// already ran the profiler (auto strategy), the same profile is threaded
// through here so we don't re-scan the entire document.
func splitByHeadingsImpl(text string, cfg SplitterConfig, profile *DocProfile) []Chunk {
	if text == "" {
		return nil
	}
	if profile == nil {
		profile = ProfileDocument(text)
	}
	primaryLevel := profile.DominantHeadingLevel()
	if primaryLevel == 0 {
		return SplitText(text, cfg)
	}

	bounds := findHeadingBoundaries(text, primaryLevel)
	if len(bounds) <= 1 {
		return SplitText(text, cfg)
	}

	runes := []rune(text)
	hierarchy := NewHeadingHierarchy()

	// Pre-walk every heading (not just primary-level) so the hierarchy
	// reflects the full nesting context for each section's start. We only
	// snapshot the breadcrumb at section boundaries; deeper sub-headings
	// inside a section update the hierarchy but do not change the chunk's
	// breadcrumb (chunks within a section share one breadcrumb).
	var out []Chunk
	seq := 0

	for i, b := range bounds {
		endRune := len(runes)
		if i+1 < len(bounds) {
			endRune = bounds[i+1].runeStart
		}
		if b.line != "" {
			hierarchy.Observe(b.line)
		}
		// Catch sub-headings that occur between this primary boundary and
		// the next so the hierarchy stays in sync for subsequent sections.
		// We intentionally do this after observing the section header so
		// the breadcrumb reflects the section-leading heading.
		breadcrumb := hierarchy.BreadcrumbWithHashes()
		observeSubHeadings(runes[b.runeStart:endRune], primaryLevel, hierarchy)

		sectionRunes := runes[b.runeStart:endRune]
		sectionContent := string(sectionRunes)
		secLen := len(sectionRunes)
		if secLen == 0 {
			continue
		}

		bcLen := utf8.RuneCountInString(breadcrumb)
		// Single-chunk section: emit as-is, breadcrumb tracked separately.
		// The breadcrumb is delivered via Chunk.ContextHeader (not Content)
		// to preserve End-Start == len(Content) invariants relied on by
		// document reconstruction (knowledge.go:2278+).
		if bcLen+2+secLen <= cfg.ChunkSize {
			out = append(out, Chunk{
				Content:       sectionContent,
				ContextHeader: breadcrumb,
				Seq:           seq,
				Start:         b.runeStart,
				End:           endRune,
			})
			seq++
			continue
		}

		// Section too large: defer to the legacy splitter for inner
		// segmentation. Sub-chunks inherit the same breadcrumb via
		// ContextHeader. We do NOT shrink the inner ChunkSize budget here
		// because the breadcrumb no longer counts against Content size.
		subChunks := SplitText(sectionContent, cfg)
		for _, sub := range subChunks {
			out = append(out, Chunk{
				Content:       sub.Content,
				ContextHeader: breadcrumb,
				Seq:           seq,
				Start:         b.runeStart + sub.Start,
				End:           b.runeStart + sub.End,
			})
			seq++
		}
	}

	return coalesceTinyChunks(out, cfg.ChunkSize)
}

// coalesceTinyChunks merges adjacent small chunks under their shared heading
// context so that documents whose primary sections are mostly short (FAQs,
// install logs, change-lists) don't trip the validator's "too many tiny
// chunks" rule and fall through all the way to legacy. The merged breadcrumb
// is the line-prefix shared by both inputs; the original sub-headings remain
// visible because heading_splitter includes the heading line in each
// section's Content.
//
// Safety:
//   - We only merge when cur.End == next.Start. That preserves the
//     End-Start == len([]rune(Content)) invariant that document
//     reconstruction relies on, and naturally skips legacy sub-chunks (which
//     may overlap due to ChunkOverlap).
//   - We stop accumulating once the running chunk reaches the merge target
//     (≈ ChunkSize/2) so we don't aggressively pack chunks beyond what the
//     validator considers comfortable.
func coalesceTinyChunks(in []Chunk, chunkSize int) []Chunk {
	if len(in) <= 1 || chunkSize <= 0 {
		return in
	}
	target := chunkSize / 2
	if target < 200 {
		target = 200
	}

	out := make([]Chunk, 0, len(in))
	cur := in[0]
	curLen := utf8.RuneCountInString(cur.Content)

	for i := 1; i < len(in); i++ {
		next := in[i]
		nextLen := utf8.RuneCountInString(next.Content)
		// Adjacent + still-small + would not blow the size budget → merge.
		if cur.End == next.Start && curLen < target && curLen+nextLen <= chunkSize {
			cur.Content += next.Content
			cur.ContextHeader = commonHeadingPrefix(cur.ContextHeader, next.ContextHeader)
			cur.End = next.End
			curLen += nextLen
			continue
		}
		out = append(out, cur)
		cur = next
		curLen = nextLen
	}
	out = append(out, cur)

	// Re-sequence — downstream code (knowledge.go) expects Seq to be a dense
	// 0..N-1 range over the returned slice.
	for i := range out {
		out[i].Seq = i
	}
	return out
}

// commonHeadingPrefix returns the longest line-aligned prefix shared by two
// breadcrumb strings. Heading hierarchies are emitted as
// "# Top\n## Section\n### Sub", so a line-by-line comparison is sufficient
// and avoids partial-line truncation that would corrupt the breadcrumb.
func commonHeadingPrefix(a, b string) string {
	if a == b {
		return a
	}
	la := strings.Split(a, "\n")
	lb := strings.Split(b, "\n")
	n := len(la)
	if len(lb) < n {
		n = len(lb)
	}
	common := 0
	for i := 0; i < n; i++ {
		if la[i] != lb[i] {
			break
		}
		common = i + 1
	}
	if common == 0 {
		return ""
	}
	return strings.Join(la[:common], "\n")
}

// findHeadingBoundaries returns one boundary at offset 0 plus one per
// Markdown heading at level <= primaryLevel that sits outside fenced code
// blocks. Heading detection is line-oriented — a heading must occupy a
// whole line to be recognized.
func findHeadingBoundaries(text string, primaryLevel int) []headingBoundary {
	runes := []rune(text)
	bounds := []headingBoundary{{runeStart: 0}}
	if len(runes) == 0 {
		return bounds
	}

	pos := 0
	inFence := false
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") {
			inFence = !inFence
			pos += utf8.RuneCountInString(line)
			if i < len(lines)-1 {
				pos++ // newline
			}
			continue
		}
		if !inFence {
			m := MarkdownHeadingPattern.FindStringSubmatch(line)
			if m != nil {
				level := len(m[1])
				if level >= 1 && level <= primaryLevel && pos > 0 {
					bounds = append(bounds, headingBoundary{
						runeStart: pos,
						line:      line,
					})
				}
				if level >= 1 && level <= primaryLevel && pos == 0 {
					// First line is a heading — replace the leading boundary
					bounds[0].line = line
				}
			}
		}
		pos += utf8.RuneCountInString(line)
		if i < len(lines)-1 {
			pos++ // account for the \n that strings.Split removed
		}
	}
	return bounds
}

// observeSubHeadings walks the section's lines and feeds every Markdown
// heading deeper than primaryLevel into the hierarchy. This keeps the
// hierarchy state correct so the breadcrumb at the next primary section
// reflects the truly active stack.
func observeSubHeadings(runes []rune, primaryLevel int, h *HeadingHierarchy) {
	if len(runes) == 0 {
		return
	}
	text := string(runes)
	inFence := false
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") {
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}
		m := MarkdownHeadingPattern.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		level := len(m[1])
		if level > primaryLevel {
			h.Observe(line)
		}
	}
}

