package service

import (
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

// linkRef is a single (slug, matchText) candidate for cross-link injection.
type linkRef struct {
	slug      string
	matchText string
}

// span is a half-open byte range [start, end) inside the content that must not
// be touched by linkification. It covers fenced code blocks, inline code,
// existing [[...]] wiki links, markdown links [text](url), and image
// ![alt](url) forms.
type span struct {
	start int
	end   int
}

// linkifyContent injects [[slug|matchText]] cross-links into content for each
// ref, skipping occurrences that fall inside code or existing links and
// requiring word boundaries for ASCII-letter matchTexts.
//
// For every ref, at most the FIRST eligible occurrence is wrapped. Refs that
// are already linked to their slug (either [[slug]] or [[slug|...]]) are
// skipped entirely. Refs pointing at selfSlug are skipped.
//
// Returns the possibly-updated content and a bool indicating whether any
// change was made. The input ref slice is not mutated.
func linkifyContent(content string, refs []linkRef, selfSlug string) (string, bool) {
	if content == "" || len(refs) == 0 {
		return content, false
	}

	// Work on a local copy sorted by matchText length (rune count) descending so
	// longer names win over their substrings.
	sorted := make([]linkRef, 0, len(refs))
	for _, r := range refs {
		if r.slug == "" || r.matchText == "" {
			continue
		}
		if r.slug == selfSlug {
			continue
		}
		sorted = append(sorted, r)
	}
	sort.SliceStable(sorted, func(i, j int) bool {
		return utf8.RuneCountInString(sorted[i].matchText) >
			utf8.RuneCountInString(sorted[j].matchText)
	})

	forbidden, used := computeForbiddenSpans(content)
	changed := false

	for _, ref := range sorted {
		// Skip if the ref's slug is already wired up anywhere in content.
		if _, ok := used[ref.slug]; ok {
			continue
		}
		pos := findFirstSafeMatch(content, ref.matchText, forbidden)
		if pos < 0 {
			continue
		}
		replacement := "[[" + ref.slug + "|" + ref.matchText + "]]"
		content = content[:pos] + replacement + content[pos+len(ref.matchText):]
		// Shift / extend the forbidden spans to reflect the edit so subsequent
		// refs don't nest a link inside the newly created [[...]].
		delta := len(replacement) - len(ref.matchText)
		forbidden = shiftSpansAfter(forbidden, pos, delta)
		forbidden = append(forbidden, span{start: pos, end: pos + len(replacement)})
		sortSpans(forbidden)
		used[ref.slug] = struct{}{}
		changed = true
	}

	return content, changed
}

// findFirstSafeMatch returns the byte offset of the first occurrence of needle
// in haystack that (a) doesn't fall inside any forbidden span and (b) for
// ASCII-letter needles is not adjacent to other word characters.
// Returns -1 if no such occurrence exists.
func findFirstSafeMatch(haystack, needle string, forbidden []span) int {
	if needle == "" {
		return -1
	}
	needsBoundary := hasASCIILetterEdge(needle)

	start := 0
	for start <= len(haystack)-len(needle) {
		rel := strings.Index(haystack[start:], needle)
		if rel < 0 {
			return -1
		}
		pos := start + rel
		end := pos + len(needle)

		if spanContains(forbidden, pos, end) {
			start = pos + 1
			continue
		}
		if needsBoundary && !hasWordBoundary(haystack, pos, end) {
			start = pos + 1
			continue
		}
		return pos
	}
	return -1
}

// hasASCIILetterEdge reports whether the needle starts or ends with an ASCII
// letter/digit. Only such needles need word-boundary checks — pure CJK or
// punctuation-led matchText doesn't have a word-boundary concept.
func hasASCIILetterEdge(s string) bool {
	if s == "" {
		return false
	}
	first, _ := utf8.DecodeRuneInString(s)
	last, _ := utf8.DecodeLastRuneInString(s)
	return isASCIIWordRune(first) || isASCIIWordRune(last)
}

func isASCIIWordRune(r rune) bool {
	if r > unicode.MaxASCII {
		return false
	}
	return r == '_' || (r >= '0' && r <= '9') ||
		(r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')
}

// hasWordBoundary checks that the characters immediately before pos and at end
// are not ASCII word runes (letters/digits/underscore). Non-ASCII runes (e.g.
// CJK) are treated as boundaries so "北京" embedded in "北京邮电大学" still
// matches — length-descending ordering handles that conflict separately.
func hasWordBoundary(s string, pos, end int) bool {
	if pos > 0 {
		r, _ := utf8.DecodeLastRuneInString(s[:pos])
		if isASCIIWordRune(r) {
			return false
		}
	}
	if end < len(s) {
		r, _ := utf8.DecodeRuneInString(s[end:])
		if isASCIIWordRune(r) {
			return false
		}
	}
	return true
}

// spanContains reports whether any span overlaps [pos, end).
func spanContains(spans []span, pos, end int) bool {
	for _, sp := range spans {
		if pos < sp.end && end > sp.start {
			return true
		}
	}
	return false
}

func shiftSpansAfter(spans []span, pivot, delta int) []span {
	if delta == 0 {
		return spans
	}
	out := make([]span, len(spans))
	for i, sp := range spans {
		if sp.start >= pivot {
			sp.start += delta
			sp.end += delta
		}
		out[i] = sp
	}
	return out
}

func sortSpans(spans []span) {
	sort.Slice(spans, func(i, j int) bool {
		if spans[i].start != spans[j].start {
			return spans[i].start < spans[j].start
		}
		return spans[i].end < spans[j].end
	})
}

// computeForbiddenSpans returns byte ranges in s that must be left untouched
// by cross-link injection, plus the set of wiki-link slugs already referenced
// in content so callers can skip already-linked refs without a second scan.
//
// Covered forbidden regions:
//   - fenced code blocks delimited by ``` or ~~~
//   - inline code delimited by matching ` runs
//   - existing [[slug|...]] / [[slug]] wiki links
//   - inline markdown links [text](url) and images ![alt](url)
//   - full reference-style links [text][label]
//   - reference link definitions: lines of the form [label]: url ...
//   - autolinks <url>
func computeForbiddenSpans(s string) ([]span, map[string]struct{}) {
	spans := make([]span, 0, 8)
	used := make(map[string]struct{})
	i := 0
	n := len(s)

	// Pass 1: reference link definitions. These live on their own line and the
	// main structural pass below would otherwise treat `[label]` as a dangling
	// open bracket. Recording them up-front keeps both branches simple.
	for _, sp := range scanReferenceDefinitions(s) {
		spans = append(spans, sp)
	}

	for i < n {
		// Fenced code block: line starts with ``` or ~~~
		if isFenceStart(s, i) {
			fenceLen, fenceCh := fenceRun(s, i)
			end := findFenceEnd(s, i+fenceLen, fenceCh, fenceLen)
			spans = append(spans, span{start: i, end: end})
			i = end
			continue
		}

		c := s[i]
		switch c {
		case '`':
			// inline code: count backticks, find matching closing run of same length
			run := 1
			for i+run < n && s[i+run] == '`' {
				run++
			}
			closeIdx := findInlineCodeClose(s, i+run, run)
			if closeIdx < 0 {
				i += run
				continue
			}
			spans = append(spans, span{start: i, end: closeIdx + run})
			i = closeIdx + run
		case '[':
			// [[slug...]] wiki link — record the slug in `used`
			if i+1 < n && s[i+1] == '[' {
				if close := strings.Index(s[i+2:], "]]"); close >= 0 {
					end := i + 2 + close + 2
					inner := s[i+2 : i+2+close]
					if slug := extractWikiSlug(inner); slug != "" {
						used[slug] = struct{}{}
					}
					spans = append(spans, span{start: i, end: end})
					i = end
					continue
				}
			}
			// [text](url) inline markdown link
			if end, ok := matchMarkdownLink(s, i); ok {
				spans = append(spans, span{start: i, end: end})
				i = end
				continue
			}
			// [text][label] reference-style link
			if end, ok := matchReferenceStyleLink(s, i); ok {
				spans = append(spans, span{start: i, end: end})
				i = end
				continue
			}
			i++
		case '!':
			// ![alt](url) image
			if i+1 < n && s[i+1] == '[' {
				if end, ok := matchMarkdownLink(s, i+1); ok {
					spans = append(spans, span{start: i, end: end})
					i = end
					continue
				}
				if end, ok := matchReferenceStyleLink(s, i+1); ok {
					spans = append(spans, span{start: i, end: end})
					i = end
					continue
				}
			}
			i++
		case '<':
			if end, ok := matchAutolink(s, i); ok {
				spans = append(spans, span{start: i, end: end})
				i = end
				continue
			}
			i++
		default:
			i++
		}
	}

	sortSpans(spans)
	return spans, used
}

// extractWikiSlug parses the inner text of [[...]] and returns the slug part.
// For [[slug|display]] returns "slug"; for [[slug]] returns the whole inner
// text (trimmed). Returns "" if the inner text contains whitespace that would
// make it an unlikely real slug.
func extractWikiSlug(inner string) string {
	if pipe := strings.IndexByte(inner, '|'); pipe >= 0 {
		inner = inner[:pipe]
	}
	inner = strings.TrimSpace(inner)
	if inner == "" {
		return ""
	}
	return inner
}

// matchReferenceStyleLink matches `[text][label]` starting at `[` and returns
// the byte offset just past the closing `]`. The text and label portions must
// not span multiple lines and must both be bracket-balanced.
func matchReferenceStyleLink(s string, i int) (int, bool) {
	if i >= len(s) || s[i] != '[' {
		return 0, false
	}
	// First `[text]`
	textEnd, ok := findClosingBracket(s, i)
	if !ok {
		return 0, false
	}
	if textEnd+1 >= len(s) || s[textEnd+1] != '[' {
		return 0, false
	}
	// Second `[label]`
	labelEnd, ok := findClosingBracket(s, textEnd+1)
	if !ok {
		return 0, false
	}
	return labelEnd + 1, true
}

// findClosingBracket returns the byte offset of the matching `]` for the `[`
// at position i, honoring `\[` / `\]` escapes and giving up on newlines.
func findClosingBracket(s string, i int) (int, bool) {
	if i >= len(s) || s[i] != '[' {
		return 0, false
	}
	depth := 1
	j := i + 1
	for j < len(s) {
		switch s[j] {
		case '\\':
			if j+1 < len(s) {
				j += 2
				continue
			}
		case '[':
			depth++
		case ']':
			depth--
			if depth == 0 {
				return j, true
			}
		case '\n':
			return 0, false
		}
		j++
	}
	return 0, false
}

// scanReferenceDefinitions finds all `[label]: url ...` definition lines and
// returns their byte ranges (including the trailing newline if present).
// Only lines whose first non-space character is `[` are considered, matching
// CommonMark's rule that definitions may be indented up to 3 spaces.
func scanReferenceDefinitions(s string) []span {
	var out []span
	lineStart := 0
	for lineStart < len(s) {
		nl := strings.IndexByte(s[lineStart:], '\n')
		var lineEnd int
		if nl < 0 {
			lineEnd = len(s)
		} else {
			lineEnd = lineStart + nl + 1 // include trailing \n
		}

		// Measure leading indent (up to 3 spaces per CommonMark)
		indent := 0
		for indent < 3 && lineStart+indent < lineEnd && s[lineStart+indent] == ' ' {
			indent++
		}
		start := lineStart + indent

		if start < lineEnd && s[start] == '[' {
			labelEnd, ok := findClosingBracket(s, start)
			if ok && labelEnd+1 < lineEnd && s[labelEnd+1] == ':' {
				// It's a reference definition; guard region covers the whole line.
				out = append(out, span{start: lineStart, end: lineEnd})
			}
		}

		lineStart = lineEnd
	}
	return out
}

// isFenceStart reports whether index i is at the start of a line and begins a
// fence run (``` or ~~~, three or more).
func isFenceStart(s string, i int) bool {
	if i > 0 && s[i-1] != '\n' {
		return false
	}
	if i+2 >= len(s) {
		return false
	}
	c := s[i]
	if c != '`' && c != '~' {
		return false
	}
	return s[i+1] == c && s[i+2] == c
}

func fenceRun(s string, i int) (int, byte) {
	c := s[i]
	j := i
	for j < len(s) && s[j] == c {
		j++
	}
	return j - i, c
}

// findFenceEnd returns the byte offset just past the closing fence, or len(s)
// if no close is found.
func findFenceEnd(s string, start int, ch byte, minLen int) int {
	// Advance to next line
	nl := strings.IndexByte(s[start:], '\n')
	if nl < 0 {
		return len(s)
	}
	pos := start + nl + 1
	for pos < len(s) {
		if s[pos] == ch {
			runLen, _ := fenceRun(s, pos)
			if runLen >= minLen {
				// Close must be at start of line (we're past a newline already).
				// Skip trailing chars to end of line.
				endLine := strings.IndexByte(s[pos:], '\n')
				if endLine < 0 {
					return len(s)
				}
				return pos + endLine + 1
			}
		}
		nl := strings.IndexByte(s[pos:], '\n')
		if nl < 0 {
			return len(s)
		}
		pos += nl + 1
	}
	return len(s)
}

// findInlineCodeClose returns the byte offset of the start of a closing
// backtick run of exactly runLen, or -1 if none.
func findInlineCodeClose(s string, start, runLen int) int {
	i := start
	for i < len(s) {
		// newlines do not terminate inline code in CommonMark, but we stop at
		// a double newline (paragraph break) to avoid runaway spans.
		if i+1 < len(s) && s[i] == '\n' && s[i+1] == '\n' {
			return -1
		}
		if s[i] == '`' {
			j := i
			for j < len(s) && s[j] == '`' {
				j++
			}
			if j-i == runLen {
				return i
			}
			i = j
			continue
		}
		i++
	}
	return -1
}

// matchMarkdownLink matches [text](url) starting at i where s[i] == '['.
// Returns the byte offset just past the closing ')' and true on match.
func matchMarkdownLink(s string, i int) (int, bool) {
	if i >= len(s) || s[i] != '[' {
		return 0, false
	}
	// Find ']' respecting balanced [[ ]] inside (rare)
	depth := 1
	j := i + 1
	for j < len(s) && depth > 0 {
		switch s[j] {
		case '\\':
			if j+1 < len(s) {
				j += 2
				continue
			}
		case '[':
			depth++
		case ']':
			depth--
			// depth==0 is handled by the post-switch check below, which breaks
			// out of the for loop (switch-local break would only exit the switch)
		case '\n':
			// markdown link text can't span many newlines; give up to avoid
			// runaway spans
			return 0, false
		}
		if depth == 0 {
			break
		}
		j++
	}
	if j >= len(s) || s[j] != ']' {
		return 0, false
	}
	if j+1 >= len(s) || s[j+1] != '(' {
		return 0, false
	}
	// Find matching ')' with shallow paren nesting.
	k := j + 2
	parenDepth := 1
	for k < len(s) && parenDepth > 0 {
		switch s[k] {
		case '\\':
			if k+1 < len(s) {
				k += 2
				continue
			}
		case '(':
			parenDepth++
		case ')':
			parenDepth--
			if parenDepth == 0 {
				return k + 1, true
			}
		case '\n':
			return 0, false
		}
		k++
	}
	return 0, false
}

// matchAutolink matches <scheme://...> starting at i where s[i] == '<'.
func matchAutolink(s string, i int) (int, bool) {
	if i >= len(s) || s[i] != '<' {
		return 0, false
	}
	close := strings.IndexByte(s[i+1:], '>')
	if close < 0 {
		return 0, false
	}
	inner := s[i+1 : i+1+close]
	if len(inner) == 0 || strings.ContainsAny(inner, " \t\n") {
		return 0, false
	}
	// Require scheme://host or mailto: style
	if !strings.Contains(inner, "://") && !strings.HasPrefix(inner, "mailto:") {
		return 0, false
	}
	return i + 1 + close + 1, true
}
