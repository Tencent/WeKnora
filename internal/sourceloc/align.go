package sourceloc

import (
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/Tencent/WeKnora/internal/types"
)

// Unit is one structural piece of an original file (a paragraph, a slide
// shape, a sheet row) with the locator it should resolve to.
type Unit struct {
	Text    string
	Locator types.SourceLocator
}

const (
	// alignKeyRunes is how much of a unit's normalized text is searched for.
	alignKeyRunes = 32
	// alignMinRunes skips units too short to place reliably.
	alignMinRunes = 2
	// alignWindowRunes bounds how far past the cursor a unit is searched, so
	// a unit the parser dropped cannot drag the cursor across the document.
	alignWindowRunes = 20000
)

// Align places units, which are in document order, into markdown and returns
// one block per placed unit. A block runs from where its unit was found to
// where the next placed unit starts, so the blocks tile the markdown from the
// first placed unit on. Units are matched on letters and digits only, which
// makes the match indifferent to Markdown syntax, whitespace and
// punctuation the parser added or dropped.
func Align(markdown string, units []Unit) []types.SourceBlock {
	norm := normalizeMarkdown(markdown)
	if norm.text == "" || len(units) == 0 {
		return nil
	}
	type hit struct {
		pos  int // rune offset in markdown
		unit int
	}
	var hits []hit
	cursor := 0 // byte offset into norm.text
	for i, u := range units {
		key := normalizeKey(u.Text)
		if utf8.RuneCountInString(key) < alignMinRunes {
			continue
		}
		at, width := norm.find(key, cursor)
		if at < 0 {
			continue
		}
		hits = append(hits, hit{pos: norm.pos[at], unit: i})
		cursor = at + width
	}
	if len(hits) == 0 {
		return nil
	}
	runes := []rune(markdown)
	prev := 0
	for i := range hits {
		hits[i].pos = lineLeadStart(runes, hits[i].pos, prev)
		prev = hits[i].pos
	}
	total := len(runes)
	blocks := make([]types.SourceBlock, 0, len(hits))
	for i, h := range hits {
		end := total
		if i+1 < len(hits) {
			end = hits[i+1].pos
		}
		if end <= h.pos {
			continue
		}
		blocks = append(blocks, types.SourceBlock{Start: h.pos, End: end, Locator: units[h.unit].Locator})
	}
	return blocks
}

// lineLeadStart moves pos back to the start of its line when everything
// before it on the line is markup or numbering ("## ", "| ", "1. ", "(2) "),
// which belongs to the unit but was not part of its text. It never crosses
// floor.
func lineLeadStart(runes []rune, pos, floor int) int {
	const maxLead = 16
	start := pos
	for start > floor && runes[start-1] != '\n' {
		if pos-start >= maxLead || unicode.IsLetter(runes[start-1]) {
			return pos
		}
		start--
	}
	return start
}

// normalized is the letters-and-digits projection of a text: text holds the
// kept runes, pos[b] the rune offset in the source of the rune starting at
// byte b of text.
type normalized struct {
	text string
	pos  []int
}

// find searches key (already normalized) at or after byte offset from,
// within the search window. It tries the key's head first and falls back to
// a slice from its middle, so a unit whose opening differs (a list number
// the parser rendered, a leading image) still lands. It returns the byte
// offset of the match and how far the cursor should advance.
func (n normalized) find(key string, from int) (int, int) {
	limit := len(n.text)
	if w := from + alignWindowRunes*3; w < limit {
		limit = w
	}
	window := n.text[from:limit]
	head := firstRunes(key, alignKeyRunes)
	if idx := strings.Index(window, head); idx >= 0 {
		return from + idx, len(head)
	}
	runes := []rune(key)
	if len(runes) < alignKeyRunes*2 {
		return -1, 0
	}
	mid := string(runes[len(runes)/2 : len(runes)/2+alignKeyRunes])
	if idx := strings.Index(window, mid); idx >= 0 {
		// Aim at where the unit should begin, not at its middle.
		back := len(string(runes[:len(runes)/2]))
		start := from + idx - back
		start = max(start, from)
		for start < from+idx && !utf8.RuneStart(n.text[start]) {
			start++
		}
		return start, idx + len(mid) - (start - from)
	}
	return -1, 0
}

// normalizeMarkdown projects markdown onto its letters and digits, skipping
// link and image destinations and HTML tags, whose URLs and attributes are
// not text of the document.
func normalizeMarkdown(markdown string) normalized {
	var b strings.Builder
	b.Grow(len(markdown))
	pos := make([]int, 0, len(markdown))
	runes := []rune(markdown)
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		switch {
		case r == '<' && i+1 < len(runes) && (isASCIILetter(runes[i+1]) || runes[i+1] == '/' || runes[i+1] == '!'):
			if end := indexRune(runes, '>', i+1, 4096); end > 0 {
				i = end
				continue
			}
		case r == ']' && i+1 < len(runes) && runes[i+1] == '(':
			if end := indexRune(runes, ')', i+2, 1<<20); end > 0 {
				i = end
				continue
			}
		}
		if nr, ok := foldRune(r); ok {
			start := b.Len()
			b.WriteRune(nr)
			for k := start; k < b.Len(); k++ {
				pos = append(pos, i)
			}
		}
	}
	return normalized{text: b.String(), pos: pos}
}

// normalizeKey projects plain text onto its letters and digits.
func normalizeKey(text string) string {
	var b strings.Builder
	b.Grow(len(text))
	for _, r := range text {
		if nr, ok := foldRune(r); ok {
			b.WriteRune(nr)
		}
	}
	return b.String()
}

// foldRune keeps letters and digits, lower-cased and with full-width ASCII
// folded to its half-width form.
func foldRune(r rune) (rune, bool) {
	if r >= 0xFF01 && r <= 0xFF5E {
		r -= 0xFEE0
	}
	if !unicode.IsLetter(r) && !unicode.IsDigit(r) {
		return 0, false
	}
	return unicode.ToLower(r), true
}

func isASCIILetter(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')
}

// indexRune finds r in runes[from:], giving up after limit runes or at a
// blank line.
func indexRune(runes []rune, r rune, from, limit int) int {
	end := min(len(runes), from+limit)
	for i := from; i < end; i++ {
		if runes[i] == r {
			return i
		}
		if runes[i] == '\n' && i+1 < len(runes) && runes[i+1] == '\n' {
			return -1
		}
	}
	return -1
}

func firstRunes(s string, n int) string {
	i := 0
	for idx := range s {
		if i == n {
			return s[:idx]
		}
		i++
	}
	return s
}

// SortBlocks orders blocks by start offset.
func SortBlocks(blocks []types.SourceBlock) {
	sort.SliceStable(blocks, func(i, j int) bool { return blocks[i].Start < blocks[j].Start })
}
