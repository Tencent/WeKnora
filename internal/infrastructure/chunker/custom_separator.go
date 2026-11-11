// Package chunker - custom_separator.go implements the "custom_separator"
// strategy: first-class support for pre-chunked documents.
//
// Use case: upstream AI pipelines or engineering tooling often produces
// documents whose chunk boundaries are already decided (each pre-made chunk
// joined by a special marker such as "======" or "<|chunk|>"). The operator
// selects strategy "custom_separator", declares the marker via
// chunking_config.custom_separator, and WeKnora preserves the external
// chunking instead of re-deriving its own.
//
// Semantics:
//   - Selected by Strategy == "custom_separator" plus a non-blank
//     CustomSeparator marker. The marker is inert under every other
//     strategy, so choosing a regular strategy disables it.
//   - With CustomSeparatorOnly = true ("marker only") each marker-delimited
//     segment becomes exactly one chunk. No other splitting logic runs, but
//     segments ARE capped by a size budget (see customSegmentCharBudget):
//     an oversized segment is split further by the legacy recursive
//     splitter and a warning is logged, because oversized chunks make the
//     embedding batch layer reject the whole document
//     (models/api.SplitBatches). Pre-chunked content is otherwise trusted
//     as-is.
//   - With CustomSeparatorOnly = false ("split further") each segment is
//     refined by the legacy recursive splitter (SplitText) sized by
//     ChunkSize / ChunkOverlap / Separators / TokenLimit — the same fields
//     the plain legacy strategy uses. The strategy tiers are not consulted:
//     within a segment the external boundary is authoritative and the
//     legacy splitter is deterministic.
//   - The separator itself — plus surrounding whitespace — is removed from
//     chunk content so the marker never leaks into embeddings or RAG output.
//     Chunk position spans (Start/End) are narrowed accordingly, preserving
//     the End-Start == rune-count(Content) invariant relied on by document
//     reconstruction and UI highlighting.
package chunker

import (
	"context"
	"unicode"

	"github.com/Tencent/WeKnora/internal/logger"
)

// TierCustom is the diagnostics tier reported when the custom_separator
// strategy produced the chunks.
const TierCustom StrategyTier = "custom"

// embeddingInputReserveChars is the character headroom subtracted from
// EmbeddingCharLimit before capping marker-only segments. The ingestion
// pipeline may prepend a title or context header to the text it sends to
// the embedding API; the budget must leave room for that.
const embeddingInputReserveChars = 256

// SplitByCustomSeparator implements the "custom_separator" strategy: it
// splits text on cfg.CustomSeparator and strips the separator from the
// resulting chunk content.
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
	segIndex := 0
	appendSegment := func(segEnd int) {
		a, b := trimCustomSegment(runes, segStart, segEnd)
		if a < b {
			emitCustomSegment(runes[a:b], a, cfg, &chunks)
		}
		segIndex++
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

// emitCustomSegment appends one marker-delimited segment (content runes,
// rebase offset a) to chunks according to the strategy options.
func emitCustomSegment(seg []rune, a int, cfg SplitterConfig, chunks *[]Chunk) {
	if !cfg.CustomSeparatorOnly {
		// "Split further" mode: respect the external boundaries first, then
		// let the legacy recursive splitter refine within each segment
		// (deterministic; sized by ChunkSize/Overlap/Separators/TokenLimit).
		// Child chunk positions are relative to the segment; rebase them
		// onto the document so the position invariant holds globally.
		sub := cfg
		sub.Strategy = ""
		sub.CustomSeparator = ""
		sub.CustomSeparatorOnly = false
		for _, c := range SplitText(string(seg), sub) {
			c.Start += a
			c.End += a
			c.Seq = len(*chunks)
			*chunks = append(*chunks, c)
		}
		return
	}

	// "Marker only" mode: one segment = one chunk, but still capped by the
	// size budget. An oversized segment would otherwise be rejected by the
	// embedding batch layer and fail the whole document's indexing.
	budget := customSegmentCharBudget(cfg)
	if budget <= 0 || len(seg) <= budget {
		*chunks = append(*chunks, Chunk{
			Content: string(seg),
			Start:   a,
			End:     a + len(seg),
			Seq:     len(*chunks),
		})
		return
	}

	logger.Warnf(context.Background(),
		"chunker: custom_separator marker-only segment %d (offsets %d-%d, %d runes) exceeds the %d-rune budget; "+
			"splitting it further with the legacy splitter to keep embedding inputs within limits",
		len(*chunks), a, a+len(seg), len(seg), budget)
	sub := cfg
	sub.Strategy = ""
	sub.CustomSeparator = ""
	sub.CustomSeparatorOnly = false
	sub.ChunkSize = budget
	sub.ChunkOverlap = 0 // marker boundaries are authoritative; no smoothing band
	for _, c := range SplitText(string(seg), sub) {
		// The legacy splitter keeps a unit whole when no separator matches,
		// so a separator-less run can still come back oversized. Hard-split
		// any survivor at fixed rune windows — pre-chunked content has no
		// natural boundaries for the legacy splitter to use.
		if runeLen(c.Content) <= budget {
			c.Start += a
			c.End += a
			c.Seq = len(*chunks)
			*chunks = append(*chunks, c)
			continue
		}
		cRunes := []rune(c.Content)
		for off := 0; off < len(cRunes); off += budget {
			end := off + budget
			if end > len(cRunes) {
				end = len(cRunes)
			}
			*chunks = append(*chunks, Chunk{
				Content: string(cRunes[off:end]),
				Start:   a + c.Start + off,
				End:     a + c.Start + end,
				Seq:     len(*chunks),
			})
		}
	}
}

// customSegmentCharBudget is the approximate character cap applied to each
// marker-only segment. The budget is deliberately approximate:
//
//   - With TokenLimit set it converts the token limit to characters via
//     CharsForTokenLimit (a per-language heuristic, not a tokenizer
//     guarantee — it must not be presented as an exact model-token cap).
//   - With TokenLimit unset it is ChunkSize in characters (ensureDefaults
//     has already clamped ChunkSize by the TokenLimit conversion and filled
//     the default when zero).
//   - When the caller knows the embedding model's per-input character limit
//     (EmbeddingCharLimit, e.g. from the vendor catalog) the budget is the
//     smaller of the two, minus a reserve for title/context text the
//     ingestion pipeline may prepend to the embedding input.
func customSegmentCharBudget(cfg SplitterConfig) int {
	budget := cfg.ChunkSize
	if cfg.TokenLimit > 0 {
		lang := LangMixed
		if len(cfg.Languages) > 0 {
			lang = cfg.Languages[0]
		}
		if charBudget := CharsForTokenLimit(cfg.TokenLimit, lang); charBudget > 0 {
			budget = charBudget
		}
	}
	if cfg.EmbeddingCharLimit > 0 {
		embedBudget := cfg.EmbeddingCharLimit - embeddingInputReserveChars
		if embedBudget > 0 && (budget <= 0 || embedBudget < budget) {
			budget = embedBudget
		}
	}
	return budget
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
