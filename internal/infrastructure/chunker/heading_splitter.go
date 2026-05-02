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
func splitByHeadingsImpl(text string, cfg SplitterConfig) []Chunk {
	if text == "" {
		return nil
	}
	profile := ProfileDocument(text)
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
		// Reserve some headroom (breadcrumb + 2 newlines) when fitting a section.
		if bcLen+2+secLen <= cfg.ChunkSize {
			content := prependBreadcrumb(sectionContent, breadcrumb)
			out = append(out, Chunk{
				Content: content,
				Seq:     seq,
				Start:   b.runeStart,
				End:     endRune,
			})
			seq++
			continue
		}

		// Section too large for one chunk: defer to the legacy splitter for
		// inner segmentation, then prepend the breadcrumb to every sub-chunk.
		// Reduce the inner budget by the breadcrumb length so the final
		// chunk (incl. breadcrumb) still fits under cfg.ChunkSize.
		innerCfg := cfg
		if bcLen > 0 && innerCfg.ChunkSize > bcLen+10 {
			innerCfg.ChunkSize -= bcLen + 2
		}
		subChunks := SplitText(sectionContent, innerCfg)
		for _, sub := range subChunks {
			content := prependBreadcrumb(sub.Content, breadcrumb)
			out = append(out, Chunk{
				Content: content,
				Seq:     seq,
				Start:   b.runeStart + sub.Start,
				End:     b.runeStart + sub.End,
			})
			seq++
		}
	}

	return out
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

// prependBreadcrumb attaches the breadcrumb to content unless content
// already begins with that exact breadcrumb (avoid duplication when a
// section's first line is itself the section heading).
func prependBreadcrumb(content, breadcrumb string) string {
	if breadcrumb == "" {
		return content
	}
	trimmed := strings.TrimLeft(content, " \t\r\n")
	if strings.HasPrefix(trimmed, breadcrumb) {
		return content
	}
	return breadcrumb + "\n\n" + content
}
