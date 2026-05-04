// Package chunker - heading_hierarchy.go tracks Markdown heading nesting so
// that the heading-aware splitter (heading_splitter.go) can prepend a
// breadcrumb like "# Top > ## Section > ### Subsection" to each chunk.
//
// Conceptually similar to header_tracker.go (which tracks table headers via
// start/end hooks) but a Markdown heading does not have an explicit end —
// it is ended by the next heading of equal or shallower depth. The hook
// abstraction in header_tracker.go does not fit that pattern, so we model
// it as an explicit level-stack.
package chunker

import "strings"

// HeadingHierarchy maintains a stack of active Markdown headings indexed by
// level (1..6). Pushing a level-N heading pops every entry of level >= N
// because the previous siblings/descendants are no longer in scope.
type HeadingHierarchy struct {
	// stack[i] holds the heading text for level i+1 (so stack[0] = H1).
	// Entries beyond the deepest active level are empty strings.
	stack [6]string
	depth int // current deepest active level (0 if no active heading)
}

// NewHeadingHierarchy returns an empty hierarchy.
func NewHeadingHierarchy() *HeadingHierarchy {
	return &HeadingHierarchy{}
}

// Observe parses line and updates the hierarchy if line is a Markdown
// heading. Returns the (level, headingText) when a heading was recognized,
// or (0, "") otherwise. Lines that look like headings inside fenced code
// blocks are NOT detected here — callers must avoid feeding code-block
// content to Observe (the heading splitter does so).
func (h *HeadingHierarchy) Observe(line string) (int, string) {
	m := MarkdownHeadingPattern.FindStringSubmatch(line)
	if m == nil {
		return 0, ""
	}
	level := len(m[1])
	if level < 1 || level > 6 {
		return 0, ""
	}
	heading := strings.TrimSpace(m[2])
	// Replace this level and clear deeper ones — siblings/descendants of
	// the previous heading at this level are no longer in scope.
	h.stack[level-1] = heading
	for i := level; i < 6; i++ {
		h.stack[i] = ""
	}
	if level > h.depth {
		h.depth = level
	} else {
		// Recompute depth: it might shrink if we just pushed a shallower heading.
		h.depth = 0
		for i := 0; i < 6; i++ {
			if h.stack[i] != "" {
				h.depth = i + 1
			}
		}
	}
	return level, heading
}

// Breadcrumb returns the current heading path joined by " > ", e.g.
// "Chapter 1 > Section 2 > Subsection a". Returns "" when no headings
// are active.
func (h *HeadingHierarchy) Breadcrumb() string {
	if h.depth == 0 {
		return ""
	}
	parts := make([]string, 0, h.depth)
	for i := 0; i < h.depth; i++ {
		if h.stack[i] != "" {
			parts = append(parts, h.stack[i])
		}
	}
	return strings.Join(parts, " > ")
}

// BreadcrumbWithHashes returns the path with the original `#` prefixes,
// suitable for embedding back into chunk content as a context header.
// Example: "# Chapter 1\n## Section 2\n### Subsection a"
func (h *HeadingHierarchy) BreadcrumbWithHashes() string {
	if h.depth == 0 {
		return ""
	}
	var sb strings.Builder
	for i := 0; i < h.depth; i++ {
		if h.stack[i] == "" {
			continue
		}
		if sb.Len() > 0 {
			sb.WriteByte('\n')
		}
		sb.WriteString(strings.Repeat("#", i+1))
		sb.WriteByte(' ')
		sb.WriteString(h.stack[i])
	}
	return sb.String()
}

// Depth returns the current deepest active heading level.
func (h *HeadingHierarchy) Depth() int { return h.depth }

// Reset clears all state.
func (h *HeadingHierarchy) Reset() {
	for i := range h.stack {
		h.stack[i] = ""
	}
	h.depth = 0
}
