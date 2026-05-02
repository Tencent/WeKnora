// Package chunker implements text splitting for document chunking.
//
// Ported from the Python docreader/splitter/splitter.py recursive text splitter.
package chunker

import (
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/Tencent/WeKnora/internal/infrastructure/docparser"
)

// Chunk represents a piece of split text with position tracking.
//
// Content holds exactly the text from the original document between Start
// and End (rune offsets), so End-Start == utf8.RuneCountInString(Content).
// This invariant is relied on by document-reconstruction code paths
// (knowledge.go:2278+ for summary generation, UI highlighting, etc.).
//
// ContextHeader is a separately-tracked context string (e.g. a Markdown
// heading breadcrumb) that should be prepended at embedding/retrieval time
// but is NOT part of Content. Keeping the two apart preserves the
// position invariant while still letting embedding pipelines see the
// section context.
type Chunk struct {
	Content       string
	ContextHeader string
	Seq           int
	Start         int
	End           int
}

// EmbeddingContent returns the text that should be fed to the embedding
// model — the ContextHeader prepended (when set) plus the chunk content.
// Use this where Content alone would lose semantic context (Tier-1 chunks).
func (c Chunk) EmbeddingContent() string {
	if c.ContextHeader == "" {
		return c.Content
	}
	return c.ContextHeader + "\n\n" + c.Content
}

// ImageRef is an image reference found within a chunk's content.
type ImageRef struct {
	OriginalRef string
	AltText     string
	Start       int // offset within the chunk content
	End         int
}

// SplitterConfig configures the text splitter. Strategy and TokenLimit are
// honored by the strategy entry point in strategy.go; the legacy SplitText
// path uses only ChunkSize/Overlap/Separators.
type SplitterConfig struct {
	ChunkSize    int
	ChunkOverlap int
	Separators   []string

	// Strategy selects an adaptive tier. Empty = legacy (backwards-compatible).
	// See strategy.go for valid values.
	Strategy string
	// TokenLimit caps chunk size in approximate tokens. 0 = use ChunkSize chars.
	TokenLimit int
	// Languages hints multilingual heuristic patterns. Empty = auto-detect.
	Languages []string
}

// Default sizes used by all entry points (DefaultConfig, ensureDefaults,
// and buildSplitterConfig in the knowledge service).
//
// MIGRATION NOTE: Prior versions had three different overlap defaults
// (Go DefaultConfig: 64, knowledge.go buildSplitterConfig: 50, Python
// docreader: 100). This file is now the single source of truth at 80
// (≈15% of DefaultChunkSize) — a community-recommended sweet spot.
//
// Existing knowledge bases that stored ChunkOverlap=0 in the DB will pick
// up 80 on next re-index; their previously-indexed embeddings will not
// match new ones bit-for-bit. Recall stays similar but search ranking
// can shift slightly. To freeze the old behavior on a per-KB basis,
// explicitly set ChunkingConfig.ChunkOverlap to 64 before re-indexing.
const (
	DefaultChunkSize    = 512
	DefaultChunkOverlap = 80
)

// DefaultConfig returns sensible defaults.
func DefaultConfig() SplitterConfig {
	return SplitterConfig{
		ChunkSize:    DefaultChunkSize,
		ChunkOverlap: DefaultChunkOverlap,
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

// splitBySeparators splits text by separators in priority order, recursively
// applying the next separator to any piece that is still larger than
// chunkSize. Mirrors the recursive priority semantics of the Python
// reference splitter (docreader/splitter/splitter.py:_split): if `\n\n`
// produces a piece that's still too big, `\n` (and subsequent separators)
// are applied within that piece — not to the whole text.
//
// chunkSize == 0 disables the recursion guard; callers that don't care
// about size budget (e.g. a final mergeUnits-style pass) pass 0.
func splitBySeparators(text string, separators []string, chunkSize int) []string {
	if text == "" || len(separators) == 0 {
		return []string{text}
	}
	if chunkSize > 0 && runeLen(text) <= chunkSize {
		return []string{text}
	}

	for i, sep := range separators {
		if sep == "" {
			continue
		}
		re := regexp.MustCompile("(" + regexp.QuoteMeta(sep) + ")")
		splits := re.Split(text, -1)
		matches := re.FindAllString(text, -1)
		if len(matches) == 0 {
			continue
		}

		var pieces []string
		for j, s := range splits {
			if s != "" {
				pieces = append(pieces, s)
			}
			if j < len(matches) && matches[j] != "" {
				pieces = append(pieces, matches[j])
			}
		}
		if len(pieces) <= 1 {
			continue
		}

		// Recursively split any piece that is still too large with the
		// remaining (lower-priority) separators.
		var out []string
		remaining := separators[i+1:]
		for _, p := range pieces {
			if chunkSize > 0 && runeLen(p) > chunkSize && len(remaining) > 0 {
				out = append(out, splitBySeparators(p, remaining, chunkSize)...)
			} else {
				out = append(out, p)
			}
		}
		return out
	}
	return []string{text}
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

	// Step 2: Split non-protected regions by separators, keep protected as atomic units.
	// chunkSize is forwarded so splitBySeparators can recursively apply lower-priority
	// separators to oversize pieces (Python-parity recursive split).
	units := buildUnitsWithProtection(text, protected, separators, chunkSize)

	// Step 3: Merge units into chunks with overlap
	return mergeUnits(units, chunkSize, chunkOverlap)
}

// buildUnitsWithProtection splits text into units, preserving protected spans as atomic.
// Start/End positions in the returned units are rune offsets (not byte offsets),
// because downstream merge logic indexes content via []rune slicing.
// If a protected span exceeds maxProtectedSize, it will be forcibly split to prevent
// creating chunks that are too large for downstream processing (e.g., embedding APIs).
// chunkSize is forwarded to splitBySeparators so recursive splitting can keep pieces
// under the budget when one separator alone leaves a piece oversize.
func buildUnitsWithProtection(text string, protected []span, separators []string, chunkSize int) []splitUnit {
	const maxProtectedSize = 7500 // Maximum size for a protected unit (留余量给标题等)

	var units []splitUnit
	bytePos := 0
	runePos := 0

	for _, p := range protected {
		if p.start > bytePos {
			pre := text[bytePos:p.start]
			parts := splitBySeparators(pre, separators, chunkSize)
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

		// If protected content is too large, forcibly split it
		if protRuneLen > maxProtectedSize {
			// Split into smaller chunks at line breaks or spaces
			runes := []rune(protText)
			offset := 0
			for offset < len(runes) {
				chunkEnd := offset + maxProtectedSize
				if chunkEnd > len(runes) {
					chunkEnd = len(runes)
				} else {
					// Try to break at a newline or space
					for i := chunkEnd - 1; i > offset && i > chunkEnd-200; i-- {
						if runes[i] == '\n' || runes[i] == ' ' {
							chunkEnd = i + 1
							break
						}
					}
				}

				chunkText := string(runes[offset:chunkEnd])
				chunkLen := chunkEnd - offset
				units = append(units, splitUnit{
					text:  chunkText,
					start: runePos + offset,
					end:   runePos + offset + chunkLen,
				})
				offset = chunkEnd
			}
		} else {
			// Normal case: keep protected content as a single unit
			units = append(units, splitUnit{
				text:  protText,
				start: runePos,
				end:   runePos + protRuneLen,
			})
		}
		runePos += protRuneLen
		bytePos = p.end
	}

	if bytePos < len(text) {
		remaining := text[bytePos:]
		parts := splitBySeparators(remaining, separators, chunkSize)
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
// Enforces an absolute maximum chunk size to prevent exceeding downstream limits (e.g., embedding APIs).
// Active contextual headers (e.g., Markdown table headers) are prepended to new
// chunks so that every chunk carries its own header context.
func mergeUnits(units []splitUnit, chunkSize, chunkOverlap int) []Chunk {
	if len(units) == 0 {
		return nil
	}

	const absoluteMaxSize = 7500

	ht := newHeaderTracker()

	var chunks []Chunk
	var current []splitUnit
	curLen := 0

	for _, u := range units {
		uLen := runeLen(u.text)

		// If this single unit exceeds absolute max, force split it further
		if uLen > absoluteMaxSize {
			// Flush current chunk if any
			if len(current) > 0 {
				chunks = append(chunks, buildChunk(current, len(chunks)))
				current = nil
				curLen = 0
			}

			// Update header state even for oversized units
			ht.update(u.text)

			// Split this oversized unit into smaller chunks
			runes := []rune(u.text)
			offset := 0
			for offset < len(runes) {
				chunkEnd := offset + absoluteMaxSize
				if chunkEnd > len(runes) {
					chunkEnd = len(runes)
				} else {
					for i := chunkEnd - 1; i > offset && i > chunkEnd-200; i-- {
						if runes[i] == '\n' || runes[i] == ' ' {
							chunkEnd = i + 1
							break
						}
					}
				}

				chunkText := string(runes[offset:chunkEnd])
				chunks = append(chunks, Chunk{
					Content: chunkText,
					Seq:     len(chunks),
					Start:   u.start + offset,
					End:     u.start + chunkEnd,
				})
				offset = chunkEnd
			}
			continue
		}

		// Update header tracking
		ht.update(u.text)
		headers := ht.getHeaders()
		headersLen := runeLen(headers)
		if headersLen > chunkSize {
			headers = ""
			headersLen = 0
		}

		// If adding this unit (plus reserving space for headers in a potential
		// next chunk) would exceed chunk size, flush the current chunk.
		if curLen+uLen+headersLen > chunkSize && len(current) > 0 {
			chunks = append(chunks, buildChunk(current, len(chunks)))

			// Keep overlap from the end of current
			current, curLen = computeOverlap(current, chunkOverlap, chunkSize, uLen)

			// Shrink overlap further if needed to fit headers + next unit
			if headers != "" && headersLen+uLen <= chunkSize {
				for len(current) > 0 && curLen+uLen+headersLen > chunkSize {
					curLen -= runeLen(current[0].text)
					current = current[1:]
				}

				// Prepend headers if the column-name context is not already present
				// in the overlap or the next unit being added.
				overlapText := unitsText(current)
				if !headerAlreadyPresent(headers, overlapText, u.text) {
					startPos := u.start
					if len(current) > 0 {
						startPos = current[0].start
					}
					hUnit := splitUnit{text: headers, start: startPos, end: startPos}
					current = append([]splitUnit{hUnit}, current...)
					curLen += headersLen
				}
			}
		}

		// Check if adding this unit would exceed absolute max
		if curLen+uLen > absoluteMaxSize {
			if len(current) > 0 {
				chunks = append(chunks, buildChunk(current, len(chunks)))
				current = nil
				curLen = 0
			}
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

// unitsText concatenates the text of all units.
func unitsText(units []splitUnit) string {
	var sb strings.Builder
	for _, u := range units {
		sb.WriteString(u.text)
	}
	return sb.String()
}

// headerAlreadyPresent returns true if the column-name row from the header
// is already present in the overlap or the next unit, preventing duplication.
func headerAlreadyPresent(headers, overlapText, unitText string) bool {
	// Fast path: full header already in overlap or unit
	if strings.Contains(overlapText, headers) || strings.Contains(unitText, headers) {
		return true
	}

	// Extract the column-name row (first meaningful non-separator line).
	// For a rewritten header like "| col1 | col2 |\n| --- | --- |\n",
	// the first line is the column names.
	colRow := headerColumnRow(headers)
	if colRow == "" {
		return false
	}

	return strings.Contains(overlapText, colRow) || strings.Contains(unitText, colRow)
}

// headerColumnRow extracts the column-name line from a header string.
// Returns empty string if the header has no meaningful column names.
func headerColumnRow(header string) string {
	for _, line := range strings.Split(header, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.Contains(line, "---") {
			continue
		}
		// Skip lines that are only pipes/whitespace (empty header rows)
		onlyPipes := true
		for _, r := range line {
			if r != '|' && r != ' ' && r != '\t' {
				onlyPipes = false
				break
			}
		}
		if !onlyPipes {
			return line
		}
	}
	return ""
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

	// Skip leading separator-only and header-marker units in the overlap
	for startIdx < len(current) {
		u := current[startIdx]
		isHeaderMarker := u.start == u.end
		trimmed := strings.TrimSpace(u.text)
		if isHeaderMarker || trimmed == "" || isSeparatorOnly(u.text) {
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

// ParentChildResult holds the two-level chunking output.
// Parent chunks provide context (large window), child chunks are used for
// embedding/retrieval (small window). Each child carries its ParentIndex so
// the caller can wire up ParentChunkID after DB insertion.
type ParentChildResult struct {
	Parents  []Chunk
	Children []ChildChunk
}

// ChildChunk extends Chunk with a reference to its parent.
type ChildChunk struct {
	Chunk
	ParentIndex int // index into ParentChildResult.Parents
}

// SplitTextParentChild performs two-level chunking:
//  1. Split text into large parent chunks (parentCfg).
//  2. Split each parent into smaller child chunks (childCfg) for embedding.
//
// The child Seq is globally unique across the entire document.
func SplitTextParentChild(text string, parentCfg, childCfg SplitterConfig) ParentChildResult {
	parents := SplitText(text, parentCfg)
	if len(parents) == 0 {
		return ParentChildResult{}
	}

	var newParents []Chunk
	var children []ChildChunk
	childSeq := 0
	for _, parent := range parents {
		subs := SplitText(parent.Content, childCfg)

		parentIndex := -1
		if len(subs) > 1 || (len(subs) == 1 && subs[0].Content != parent.Content) {
			parentIndex = len(newParents)
			newParents = append(newParents, parent)
		}

		for _, sub := range subs {
			// Adjust offsets: sub positions are relative to parent content,
			// shift to document-level offsets.
			// Use additive shift (not Content-length based) so that chunks with
			// prepended context headers keep correct positional tracking.
			sub.Seq = childSeq
			sub.Start += parent.Start
			sub.End += parent.Start
			children = append(children, ChildChunk{
				Chunk:       sub,
				ParentIndex: parentIndex,
			})
			childSeq++
		}
	}
	return ParentChildResult{Parents: newParents, Children: children}
}

// ExtractImageRefs extracts markdown image references from text.
// The URL group supports one level of balanced parentheses so that URLs
// like https://example.com/item_(abc)/123 are captured in full.
var imageRefPattern = regexp.MustCompile(`!\[([^\]]*)\]\(([^()\s]*(?:\([^)]*\)[^()\s]*)*)\)`)

func ExtractImageRefs(text string) []ImageRef {
	text = docparser.UnwrapLinkedImages(text)
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
