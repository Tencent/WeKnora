// Package chunker implements text splitting for document chunking.
//
// Ported from the Python docreader/splitter/splitter.py recursive text splitter.
package chunker

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

// Chunk represents a piece of split text with position tracking.
type Chunk struct {
	Content string
	Seq     int
	Start   int
	End     int
}

// ImageRef is an image reference found within a chunk's content.
type ImageRef struct {
	OriginalRef string
	AltText     string
	Start       int // offset within the chunk content
	End         int
}

// SplitterConfig configures the text splitter.
type SplitterConfig struct {
	ChunkSize    int
	ChunkOverlap int
	Separators   []string
}

// DefaultConfig returns sensible defaults.
func DefaultConfig() SplitterConfig {
	return SplitterConfig{
		ChunkSize:    512,
		ChunkOverlap: 50,
		Separators:   []string{"\n\n", "\n", "。"},
	}
}

// protectedPatterns are regex patterns for content that must not be split.
var protectedPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?s)\$\$.*?\$\$`),                                                               // LaTeX block math
	regexp.MustCompile(`!\[[^\]]*\]\([^)]+\)`),                                                          // Markdown images
	regexp.MustCompile(`\[[^\]]*\]\([^)]+\)`),                                                           // Markdown links
	regexp.MustCompile("(?m)[ ]*(?:\\|[^|\\n]*)+\\|[\\r\\n]+\\s*(?:\\|\\s*:?-{3,}:?\\s*)+\\|[\\r\\n]+"), // Table header+separator
	regexp.MustCompile("(?m)[ ]*(?:\\|[^|\\n]*)+\\|[\\r\\n]+"),                                          // Table rows
	regexp.MustCompile("(?s)```(?:\\w+)?[\\r\\n].*?```"),                                                // Fenced code blocks
}

type span struct {
	start, end int
}

// protectedSpans finds all non-overlapping protected regions in text.
func protectedSpans(text string) []span {
	type match struct {
		start, end int
	}
	var all []match
	for _, pat := range protectedPatterns {
		locs := pat.FindAllStringIndex(text, -1)
		for _, loc := range locs {
			if loc[1]-loc[0] > 0 {
				all = append(all, match{loc[0], loc[1]})
			}
		}
	}
	if len(all) == 0 {
		return nil
	}

	// Sort by start, then by length descending
	for i := 1; i < len(all); i++ {
		for j := i; j > 0; j-- {
			if all[j].start < all[j-1].start ||
				(all[j].start == all[j-1].start && (all[j].end-all[j].start) > (all[j-1].end-all[j-1].start)) {
				all[j], all[j-1] = all[j-1], all[j]
			} else {
				break
			}
		}
	}

	// Remove overlaps
	var result []span
	lastEnd := 0
	for _, m := range all {
		if m.start >= lastEnd {
			result = append(result, span{m.start, m.end})
			lastEnd = m.end
		}
	}
	return result
}

// splitUnit is a piece of text with its original position.
type splitUnit struct {
	text       string
	start, end int
}

// splitBySeparators splits text by separators in priority order, keeping separators.
func splitBySeparators(text string, separators []string) []string {
	if len(separators) == 0 || text == "" {
		return []string{text}
	}

	// Build regex that captures separators
	var parts []string
	for _, sep := range separators {
		parts = append(parts, regexp.QuoteMeta(sep))
	}
	pattern := "(" + strings.Join(parts, "|") + ")"
	re := regexp.MustCompile(pattern)

	splits := re.Split(text, -1)
	matches := re.FindAllString(text, -1)

	var result []string
	for i, s := range splits {
		if s != "" {
			result = append(result, s)
		}
		if i < len(matches) && matches[i] != "" {
			result = append(result, matches[i])
		}
	}
	return result
}

// runeLen returns the number of runes in s.
func runeLen(s string) int {
	return utf8.RuneCountInString(s)
}

// SplitText splits text into chunks with overlap, respecting protected patterns.
func SplitText(text string, cfg SplitterConfig) []Chunk {
	if text == "" {
		return nil
	}

	chunkSize := cfg.ChunkSize
	chunkOverlap := cfg.ChunkOverlap
	separators := cfg.Separators

	if chunkSize <= 0 {
		chunkSize = 512
	}
	if chunkOverlap < 0 {
		chunkOverlap = 0
	}

	// Step 1: Find protected spans
	protected := protectedSpans(text)

	// Step 2: Split non-protected regions by separators, keep protected as atomic units
	units := buildUnitsWithProtection(text, protected, separators)

	// Step 3: Merge units into chunks with overlap
	return mergeUnits(units, chunkSize, chunkOverlap)
}

// buildUnitsWithProtection splits text into units, preserving protected spans as atomic.
// Start/End positions in the returned units are rune offsets (not byte offsets),
// because downstream merge logic indexes content via []rune slicing.
func buildUnitsWithProtection(text string, protected []span, separators []string) []splitUnit {
	var units []splitUnit
	bytePos := 0
	runePos := 0

	for _, p := range protected {
		if p.start > bytePos {
			pre := text[bytePos:p.start]
			parts := splitBySeparators(pre, separators)
			runeOffset := runePos
			for _, part := range parts {
				partRuneLen := runeLen(part)
				units = append(units, splitUnit{
					text:  part,
					start: runeOffset,
					end:   runeOffset + partRuneLen,
				})
				runeOffset += partRuneLen
			}
			runePos += runeLen(pre)
		}

		protText := text[p.start:p.end]
		protRuneLen := runeLen(protText)
		units = append(units, splitUnit{
			text:  protText,
			start: runePos,
			end:   runePos + protRuneLen,
		})
		runePos += protRuneLen
		bytePos = p.end
	}

	if bytePos < len(text) {
		remaining := text[bytePos:]
		parts := splitBySeparators(remaining, separators)
		runeOffset := runePos
		for _, part := range parts {
			partRuneLen := runeLen(part)
			units = append(units, splitUnit{
				text:  part,
				start: runeOffset,
				end:   runeOffset + partRuneLen,
			})
			runeOffset += partRuneLen
		}
	}

	return units
}

// mergeUnits combines split units into chunks with overlap tracking.
func mergeUnits(units []splitUnit, chunkSize, chunkOverlap int) []Chunk {
	if len(units) == 0 {
		return nil
	}

	var chunks []Chunk
	var current []splitUnit
	curLen := 0

	for _, u := range units {
		uLen := runeLen(u.text)

		// If adding this unit exceeds chunk size and we have content, flush
		if curLen+uLen > chunkSize && len(current) > 0 {
			chunks = append(chunks, buildChunk(current, len(chunks)))

			// Keep overlap from the end of current
			current, curLen = computeOverlap(current, chunkOverlap, chunkSize, uLen)
		}

		current = append(current, u)
		curLen += uLen
	}

	// Flush remaining
	if len(current) > 0 {
		chunks = append(chunks, buildChunk(current, len(chunks)))
	}

	return chunks
}

func buildChunk(units []splitUnit, seq int) Chunk {
	var sb strings.Builder
	for _, u := range units {
		sb.WriteString(u.text)
	}
	return Chunk{
		Content: sb.String(),
		Seq:     seq,
		Start:   units[0].start,
		End:     units[len(units)-1].end,
	}
}

// computeOverlap returns the units to keep for overlap and their total rune length.
func computeOverlap(current []splitUnit, chunkOverlap, chunkSize, nextLen int) ([]splitUnit, int) {
	if chunkOverlap <= 0 {
		return nil, 0
	}

	// Walk backward from end, accumulating overlap
	overlapLen := 0
	startIdx := len(current)
	for i := len(current) - 1; i >= 0; i-- {
		uLen := runeLen(current[i].text)
		if overlapLen+uLen > chunkOverlap {
			break
		}
		// Check that overlap + next unit fits in chunk
		if overlapLen+uLen+nextLen > chunkSize {
			break
		}
		overlapLen += uLen
		startIdx = i
	}

	// Skip leading separators-only units in the overlap
	for startIdx < len(current) {
		u := current[startIdx]
		trimmed := strings.TrimSpace(u.text)
		if trimmed == "" || isSeparatorOnly(u.text) {
			overlapLen -= runeLen(u.text)
			startIdx++
		} else {
			break
		}
	}

	if startIdx >= len(current) {
		return nil, 0
	}

	overlap := make([]splitUnit, len(current)-startIdx)
	copy(overlap, current[startIdx:])
	return overlap, overlapLen
}

func isSeparatorOnly(s string) bool {
	for _, r := range s {
		if r != '\n' && r != '\r' && r != ' ' && r != '\t' && r != '。' {
			return false
		}
	}
	return true
}

// ExtractImageRefs extracts markdown image references from text.
var imageRefPattern = regexp.MustCompile(`!\[([^\]]*)\]\(([^)]+)\)`)

func ExtractImageRefs(text string) []ImageRef {
	matches := imageRefPattern.FindAllStringSubmatchIndex(text, -1)
	var refs []ImageRef
	for _, m := range matches {
		refs = append(refs, ImageRef{
			OriginalRef: text[m[4]:m[5]], // group 2: URL
			AltText:     text[m[2]:m[3]], // group 1: alt text
			Start:       m[0],
			End:         m[1],
		})
	}
	return refs
}
