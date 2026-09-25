// Package sourceloc maps parsed Markdown back to positions in the original
// file: it re-aims parser-reported source blocks after the ingestion pipeline
// rewrites the text, aligns parser output to the structure of office files,
// and derives the per-chunk locators that citations open.
package sourceloc

import (
	"sort"
	"strings"

	"github.com/Tencent/WeKnora/internal/types"
)

// Remapper translates rune offsets in one version of a text to the matching
// offsets in a rewritten version.
//
// Ingestion rewrites parser output before chunking (line endings, HTML
// tables, image references). Every rewrite is local, so the two texts share
// most of their lines: the remapper pairs identical lines, maps offsets
// inside a paired line directly, and spreads offsets proportionally across
// the unpaired stretches between pairs. That keeps it correct for any
// future rewrite step without each step having to report its edits.
type Remapper struct {
	oldStarts []int // rune offset of each old line
	newStarts []int
	oldLen    int
	newLen    int
	pair      []int // pair[i] = new line index matched to old line i, or -1
}

// NewRemapper prepares a mapping from oldText to newText.
func NewRemapper(oldText, newText string) *Remapper {
	oldKeys, oldStarts, oldLen := splitLines(oldText)
	newKeys, newStarts, newLen := splitLines(newText)
	return &Remapper{
		oldStarts: oldStarts,
		newStarts: newStarts,
		oldLen:    oldLen,
		newLen:    newLen,
		pair:      matchLines(oldKeys, newKeys),
	}
}

// Map returns the offset in the new text corresponding to pos in the old one.
func (r *Remapper) Map(pos int) int {
	if pos <= 0 {
		return 0
	}
	if pos >= r.oldLen {
		return r.newLen
	}
	i := sort.SearchInts(r.oldStarts, pos+1) - 1
	if j := r.pair[i]; j >= 0 {
		lineLen := r.lineLen(r.newStarts, r.newLen, j)
		return r.newStarts[j] + min(pos-r.oldStarts[i], lineLen)
	}
	// Unpaired line: find the unpaired stretch around it.
	lo := i
	for lo > 0 && r.pair[lo-1] < 0 {
		lo--
	}
	hi := i
	for hi+1 < len(r.pair) && r.pair[hi+1] < 0 {
		hi++
	}
	// A stretch rewritten line for line (image references given new URLs)
	// has as many lines on both sides: map each line onto its counterpart,
	// so the drift of a whole-document interpolation cannot accumulate.
	newLo, newHi := 0, len(r.newStarts)-1
	if lo > 0 {
		newLo = r.pair[lo-1] + 1
	}
	if hi+1 < len(r.pair) {
		newHi = r.pair[hi+1] - 1
	}
	if newHi-newLo == hi-lo {
		j := newLo + (i - lo)
		oldLine := r.lineLen(r.oldStarts, r.oldLen, i)
		newLine := r.lineLen(r.newStarts, r.newLen, j)
		within := pos - r.oldStarts[i]
		if within >= oldLine {
			return r.newStarts[j] + newLine
		}
		return r.newStarts[j] + within*newLine/oldLine
	}
	// Otherwise interpolate across the stretch.
	oldA := r.oldStarts[lo]
	oldB := r.oldLen
	if hi+1 < len(r.oldStarts) {
		oldB = r.oldStarts[hi+1]
	}
	newA := 0
	if lo > 0 {
		prev := r.pair[lo-1]
		newA = r.newStarts[prev] + r.lineLen(r.newStarts, r.newLen, prev) + 1
	}
	newB := r.newLen
	if hi+1 < len(r.pair) {
		newB = r.newStarts[r.pair[hi+1]]
	}
	newA = min(newA, newB)
	if oldB <= oldA {
		return newA
	}
	return newA + (pos-oldA)*(newB-newA)/(oldB-oldA)
}

// lineLen is the rune length of line j excluding its trailing newline.
func (r *Remapper) lineLen(starts []int, total, j int) int {
	end := total
	if j+1 < len(starts) {
		end = starts[j+1] - 1
	}
	return max(end-starts[j], 0)
}

// RemapBlocks re-aims blocks recorded against oldText at newText. Blocks
// that collapse to nothing are dropped.
func RemapBlocks(blocks []types.SourceBlock, oldText, newText string) []types.SourceBlock {
	if len(blocks) == 0 || oldText == newText {
		return blocks
	}
	r := NewRemapper(oldText, newText)
	out := make([]types.SourceBlock, 0, len(blocks))
	for _, b := range blocks {
		start, end := r.Map(b.Start), r.Map(b.End)
		if end <= start {
			continue
		}
		b.Start, b.End = start, end
		out = append(out, b)
	}
	return out
}

// splitLines returns each line's comparison key (without its line break or a
// trailing carriage return), the rune offset where it starts, and the total
// rune length.
func splitLines(text string) (keys []string, starts []int, total int) {
	pos := 0
	for {
		idx := strings.IndexByte(text, '\n')
		line := text
		if idx >= 0 {
			line = text[:idx]
		}
		keys = append(keys, strings.TrimSuffix(line, "\r"))
		starts = append(starts, pos)
		pos += runeCount(line)
		if idx < 0 {
			break
		}
		pos++ // the newline
		text = text[idx+1:]
	}
	return keys, starts, pos
}

// matchLines pairs equal lines of a and b in order. Lines unique to both
// texts anchor the pairing (longest increasing run of anchors), and the
// stretches between anchors are extended greedily from both ends.
func matchLines(a, b []string) []int {
	pair := make([]int, len(a))
	for i := range pair {
		pair[i] = -1
	}
	countA := make(map[string]int, len(a))
	for _, k := range a {
		countA[k]++
	}
	countB := make(map[string]int, len(b))
	indexB := make(map[string]int, len(b))
	for j, k := range b {
		countB[k]++
		indexB[k] = j
	}
	var candA, candB []int
	for i, k := range a {
		if countA[k] == 1 && countB[k] == 1 {
			candA = append(candA, i)
			candB = append(candB, indexB[k])
		}
	}
	anchors := longestIncreasing(candB)
	prevA, prevB := 0, 0
	for _, idx := range anchors {
		ai, bj := candA[idx], candB[idx]
		fillGap(a, b, pair, prevA, ai, prevB, bj)
		pair[ai] = bj
		prevA, prevB = ai+1, bj+1
	}
	fillGap(a, b, pair, prevA, len(a), prevB, len(b))
	return pair
}

// fillGap pairs equal lines of a[a0:a1] and b[b0:b1] from the front and the
// back until the first mismatch on each side.
func fillGap(a, b []string, pair []int, a0, a1, b0, b1 int) {
	for a0 < a1 && b0 < b1 && a[a0] == b[b0] {
		pair[a0] = b0
		a0++
		b0++
	}
	for a0 < a1 && b0 < b1 && a[a1-1] == b[b1-1] {
		pair[a1-1] = b1 - 1
		a1--
		b1--
	}
}

// longestIncreasing returns the indices of a longest strictly increasing
// subsequence of seq.
func longestIncreasing(seq []int) []int {
	if len(seq) == 0 {
		return nil
	}
	tails := make([]int, 0, len(seq)) // index into seq of the tail of each length
	prev := make([]int, len(seq))
	for i, v := range seq {
		k := sort.Search(len(tails), func(t int) bool { return seq[tails[t]] >= v })
		if k > 0 {
			prev[i] = tails[k-1]
		} else {
			prev[i] = -1
		}
		if k == len(tails) {
			tails = append(tails, i)
		} else {
			tails[k] = i
		}
	}
	out := make([]int, len(tails))
	for i, k := len(tails)-1, tails[len(tails)-1]; i >= 0; i-- {
		out[i] = k
		k = prev[k]
	}
	return out
}

func runeCount(s string) int {
	n := 0
	for range s {
		n++
	}
	return n
}
