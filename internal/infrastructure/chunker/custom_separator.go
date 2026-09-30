// Package chunker - custom_separator.go implements first-class support for
// pre-chunked documents.
//
// Use case: upstream AI pipelines or engineering tooling often produces
// documents whose chunk boundaries are already decided (each pre-made chunk
// joined by a special marker such as "======" or "<|chunk|>"). The operator
// declares that marker via chunking_config.custom_separator and WeKnora then
// preserves the external chunking verbatim instead of re-deriving its own.
//
// Semantics:
//   - When CustomSeparator is set it takes precedence over every strategy
//     tier and over the ordinary separators list.
//   - With CustomSeparatorOnly = true each marker-delimited segment becomes
//     exactly one chunk: no other splitting logic runs, and segments are NOT
//     capped by ChunkSize (pre-chunked content is trusted as-is).
//   - With CustomSeparatorOnly = false each segment is further processed by
//     the regular strategy chain (useful when some segments are far larger
//     than ChunkSize).
//   - The separator itself — plus surrounding whitespace — is removed from
//     chunk content so the marker never leaks into embeddings or RAG output.
//     Chunk position spans (Start/End) are narrowed accordingly, preserving
//     the End-Start == rune-count(Content) invariant relied on by document
//     reconstruction and UI highlighting.
package chunker

import (
	"strings"
	"unicode"
)

// TierCustom is the diagnostics tier reported when a custom separator
// produced the chunks.
const TierCustom StrategyTier = "custom"

// SplitByCustomSeparator splits text on cfg.CustomSeparator (highest
// priority) and strips the separator from the resulting chunk content.
//
// Positions are rune offsets into the original text, matching every other
// chunker entry point. cfg must already be normalized (ensureDefaults).
func SplitByCustomSeparator(text string, cfg SplitterConfig) []Chunk {
	if text == "" || cfg.CustomSeparator == "" {
		return nil
	}

	runes := []rune(text)
	sep := []rune(cfg.CustomSeparator)

	var chunks []Chunk
	segStart := 0
	appendSegment := func(segEnd int) {
		a, b := trimCustomSegment(runes, segStart, segEnd)
		if a >= b {
			return // whitespace-only segment between adjacent separators
		}
		if cfg.CustomSeparatorOnly {
			chunks = append(chunks, Chunk{
				Content: string(runes[a:b]),
				Start:   a,
				End:     b,
				Seq:     len(chunks),
			})
			return
		}
		// Mixed mode: respect the external boundaries first, then let the
		// regular strategy chain refine within each segment. Child chunk
		// positions are relative to the segment; rebase them onto the
		// document so the position invariant holds globally.
		sub := cfg
		sub.CustomSeparator = ""
		sub.CustomSeparatorOnly = false
		for _, c := range Split(string(runes[a:b]), sub) {
			c.Start += a
			c.End += a
			c.Seq = len(chunks)
			chunks = append(chunks, c)
		}
	}

	for i := 0; i+len(sep) <= len(runes); i++ {
		if !runesMatchAt(runes, sep, i) {
			continue
		}
		appendSegment(i)
		i += len(sep) - 1 // skip the separator itself
		segStart = i + 1
	}
	appendSegment(len(runes))

	return chunks
}

// runesMatchAt reports whether sep occurs at runes[i:].
func runesMatchAt(runes, sep []rune, i int) bool {
	for j, r := range sep {
		if runes[i+j] != r {
			return false
		}
	}
	return true
}

// trimCustomSegment shrinks [start, end) to exclude leading/trailing Unicode
// whitespace so separator remnants and filler newlines never reach chunk
// content or embeddings. The narrowing keeps Start/End consistent with the
// trimmed content (position invariant).
func trimCustomSegment(runes []rune, start, end int) (int, int) {
	for start < end && unicode.IsSpace(runes[start]) {
		start++
	}
	for end > start && unicode.IsSpace(runes[end-1]) {
		end--
	}
	return start, end
}

// hasCustomSeparator is a tiny helper for entry points deciding whether to
// divert to the custom-separator path.
func hasCustomSeparator(cfg SplitterConfig) bool {
	return strings.TrimSpace(cfg.CustomSeparator) != ""
}
