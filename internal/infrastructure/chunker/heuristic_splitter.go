// Package chunker - heuristic_splitter.go implements Tier 2: boundary-driven
// chunking for documents that lack proper Markdown headings but contain
// recognizable structural cues (page breaks, numbered sections, multilingual
// chapter markers, visual separators, all-caps section titles, page footers).
//
// The algorithm finds all candidate boundary positions, then performs greedy
// bin-packing — accumulating blocks between boundaries into chunks until the
// next block would exceed cfg.ChunkSize. Blocks larger than ChunkSize are
// recursively delegated to the legacy splitter for inner segmentation.
package chunker

import (
	"sort"
	"strings"
	"unicode/utf8"
)

func init() {
	splitByHeuristics = splitByHeuristicsImpl
}

// boundary marks a candidate split point in the document.
type boundary struct {
	runeStart int // rune offset where the next chunk should start
	priority  int
}

// splitByHeuristicsImpl is the Tier-2 implementation. Falls through to the
// legacy splitter when no heuristic boundaries are found.
//
// profile is currently unused (this tier scans for boundaries directly) but
// is accepted to keep the splitByHeadings / splitByHeuristics signatures
// uniform — see strategy.runTier.
func splitByHeuristicsImpl(text string, cfg SplitterConfig, _ *DocProfile) []Chunk {
	if text == "" {
		return nil
	}
	runes := []rune(text)
	totalRunes := len(runes)
	if totalRunes <= cfg.ChunkSize {
		return SplitText(text, cfg)
	}

	bounds := findHeuristicBoundaries(text, cfg.Languages)
	if len(bounds) == 0 {
		return SplitText(text, cfg)
	}

	// Append a sentinel at end-of-document so the bin-packer can flush.
	bounds = append(bounds, boundary{runeStart: totalRunes})
	// Always start with a boundary at offset 0 if not already there.
	if bounds[0].runeStart != 0 {
		bounds = append([]boundary{{runeStart: 0}}, bounds...)
	}

	// Greedy bin-packing.
	var out []Chunk
	seq := 0
	chunkStart := bounds[0].runeStart
	curEnd := chunkStart
	minChunkSize := cfg.ChunkSize / 4
	if minChunkSize < 50 {
		minChunkSize = 50
	}

	for i := 1; i < len(bounds); i++ {
		nextEnd := bounds[i].runeStart
		blockLen := nextEnd - curEnd

		if blockLen > cfg.ChunkSize {
			// The block between the previous and this boundary is itself too
			// large to fit in any chunk. Flush current accumulation, then
			// recursively chunk the oversize block via the legacy splitter.
			if curEnd-chunkStart > 0 {
				out = appendChunk(out, runes, chunkStart, curEnd, &seq)
				chunkStart = curEnd
			}
			out = appendOversizeBlock(out, runes, curEnd, nextEnd, cfg, &seq)
			curEnd = nextEnd
			chunkStart = nextEnd
			continue
		}

		// Would adding this block exceed the budget?
		accumulated := nextEnd - chunkStart
		if accumulated > cfg.ChunkSize && curEnd-chunkStart >= minChunkSize {
			// Flush accumulated content as a chunk, restart at curEnd.
			out = appendChunk(out, runes, chunkStart, curEnd, &seq)
			// Snap overlap start to the nearest semantic boundary or line
			// break instead of slicing mid-line / mid-word.
			chunkStart = applyOverlapAligned(runes, curEnd, cfg.ChunkOverlap, bounds)
		}
		curEnd = nextEnd
	}

	// Flush remaining content.
	if curEnd > chunkStart {
		out = appendChunk(out, runes, chunkStart, curEnd, &seq)
	}
	return out
}

// findHeuristicBoundaries scans text and returns boundary positions in
// ascending order. Lower-priority duplicates at the same offset are dropped.
func findHeuristicBoundaries(text string, langs []string) []boundary {
	var bounds []boundary

	// Form feeds — strongest single-character boundary.
	for _, idx := range allRuneIndices(text, "\f") {
		bounds = append(bounds, boundary{runeStart: idx, priority: PrioFormFeed})
	}

	// Per-line patterns walk the text once, line by line.
	lines := strings.Split(text, "\n")
	chapterPatterns := ChapterPatternsForLangs(langs)
	pos := 0
	inFence := false
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") {
			inFence = !inFence
		} else if !inFence {
			runeStart := pos
			added := false
			for _, pat := range chapterPatterns {
				if pat.MatchString(line) {
					bounds = append(bounds, boundary{runeStart: runeStart, priority: PrioChapterMarker})
					added = true
					break
				}
			}
			if !added && NumberedSectionPattern.MatchString(line) {
				bounds = append(bounds, boundary{runeStart: runeStart, priority: PrioNumberedHead})
				added = true
			}
			if !added && AllCapsHeadingPattern.MatchString(line) {
				bounds = append(bounds, boundary{runeStart: runeStart, priority: PrioAllCapsHeading})
				added = true
			}
			if !added && VisualSeparatorPattern.MatchString(line) {
				bounds = append(bounds, boundary{runeStart: runeStart, priority: PrioVisualSep})
				added = true
			}
			if !added && PageFooterPattern.MatchString(line) {
				bounds = append(bounds, boundary{runeStart: runeStart, priority: PrioPageFooter})
			}
		}
		pos += utf8.RuneCountInString(line)
		if i < len(lines)-1 {
			pos++ // \n
		}
	}

	// Excessive blank blocks (\n{3,}). Match at the *start* of the run so we
	// drop into the next paragraph cleanly.
	for _, idx := range ExcessiveBlanksPattern.FindAllStringIndex(text, -1) {
		runeStart := utf8.RuneCountInString(text[:idx[1]])
		bounds = append(bounds, boundary{runeStart: runeStart, priority: PrioBlankBlock})
	}

	if len(bounds) == 0 {
		return nil
	}

	// Sort by position; drop near-duplicate offsets keeping the highest priority.
	sort.Slice(bounds, func(i, j int) bool {
		if bounds[i].runeStart != bounds[j].runeStart {
			return bounds[i].runeStart < bounds[j].runeStart
		}
		return bounds[i].priority > bounds[j].priority
	})
	deduped := bounds[:0]
	prev := -1
	for _, b := range bounds {
		if b.runeStart != prev {
			deduped = append(deduped, b)
			prev = b.runeStart
		}
	}
	return deduped
}

// allRuneIndices returns every rune offset where needle starts in text.
// Only used for single-rune needles like form-feed.
func allRuneIndices(text, needle string) []int {
	var out []int
	if needle == "" {
		return out
	}
	pos := 0
	for _, r := range text {
		if string(r) == needle {
			out = append(out, pos)
		}
		pos++
	}
	return out
}

// appendChunk slices runes[start:end] into a Chunk and appends it to out.
func appendChunk(out []Chunk, runes []rune, start, end int, seq *int) []Chunk {
	if end <= start {
		return out
	}
	content := strings.TrimSpace(string(runes[start:end]))
	if content == "" {
		return out
	}
	c := Chunk{Content: string(runes[start:end]), Seq: *seq, Start: start, End: end}
	*seq++
	return append(out, c)
}

// appendOversizeBlock recursively chunks a region that is itself larger than
// cfg.ChunkSize, using the legacy splitter so internal length budgets and
// protected patterns are still respected.
func appendOversizeBlock(out []Chunk, runes []rune, start, end int, cfg SplitterConfig, seq *int) []Chunk {
	if end <= start {
		return out
	}
	subText := string(runes[start:end])
	subs := SplitText(subText, cfg)
	for _, s := range subs {
		out = append(out, Chunk{
			Content: s.Content,
			Seq:     *seq,
			Start:   start + s.Start,
			End:     start + s.End,
		})
		*seq++
	}
	return out
}

// applyOverlapAligned returns the rune offset where the next chunk should
// start. The target is `curEnd - overlap`, but we snap to the nearest
// preceding boundary (within 2x overlap) or, failing that, the previous
// newline so chunks don't begin mid-line / mid-word. Falls back to the raw
// target only if neither option is available.
//
// curEnd itself is always a boundary (the bin-packer flushes at boundary
// positions), so we exclude it from the search — picking it would yield
// zero overlap, defeating the purpose of this function.
func applyOverlapAligned(runes []rune, curEnd, overlap int, bounds []boundary) int {
	if overlap <= 0 {
		return curEnd
	}
	target := curEnd - overlap
	if target < 0 {
		target = 0
	}
	// Allowed search window: [curEnd - 2*overlap, curEnd)
	windowStart := curEnd - 2*overlap
	if windowStart < 0 {
		windowStart = 0
	}

	// Prefer a semantic boundary strictly inside the window.
	bestBound := -1
	for _, b := range bounds {
		if b.runeStart >= windowStart && b.runeStart < curEnd && b.runeStart > bestBound {
			bestBound = b.runeStart
		}
	}
	if bestBound >= 0 {
		return bestBound
	}

	// Fallback: scan backwards from `target` to the previous newline, but
	// not past windowStart so we keep the overlap roughly the right size.
	for i := target; i > windowStart && i < len(runes); i-- {
		if runes[i] == '\n' {
			return i + 1
		}
	}
	return target
}
