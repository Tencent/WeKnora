package sourceloc

import (
	"html"
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

// Skip units too short to place reliably.
const alignMinRunes = 2

// Align maps complete source units, never extending a hit through unmatched
// content. Unknown gaps must stay unknown: assigning them to the preceding
// block can point a citation at an entirely different page.
func Align(markdown string, units []Unit) []types.SourceBlock {
	norm := normalizeMarkdown(markdown)
	runes := []rune(markdown)
	var blocks []types.SourceBlock
	cursor, floor := 0, 0
	counts := make(map[string]int)
	matches := make(map[string][]int)
	for _, u := range units {
		counts[normalizeKey(u.Text)]++
	}
	for _, u := range units {
		key := normalizeKey(u.Text)
		if utf8.RuneCountInString(key) < alignMinRunes {
			continue
		}
		// Repeated source text is safe only when every occurrence is accounted
		// for by a source unit in reading order (not a TOC or an omitted block).
		positions, known := matches[key]
		if !known {
			positions = normalizedMatches(norm.text, key)
			matches[key] = positions
		}
		if len(positions) != counts[key] {
			continue
		}
		matchIndex := sort.SearchInts(positions, cursor)
		if matchIndex == len(positions) {
			continue
		}
		at := positions[matchIndex]
		endByte := at + len(key)
		start := lineLeadStart(runes, norm.pos[at], floor)
		end := norm.end[endByte-1]
		// Include trailing markup/URLs on the matched line and blank lines,
		// but stop before the next visible text's line. This also preserves
		// an image's destination without swallowing another paragraph.
		if endByte == len(norm.text) {
			end = len(runes)
		} else {
			next := norm.pos[endByte]
			lead := next
			for lead > 0 && runes[lead-1] != '\n' {
				lead--
			}
			if lead >= end {
				end = lead
			}
		}
		loc := u.Locator
		loc.Mapping = "exact"
		blocks = append(blocks, types.SourceBlock{Start: start, End: end, Locator: loc})
		cursor, floor = endByte, end
	}
	return blocks
}

// normalizedMatches rejects a number embedded in a different numeric value.
func normalizedMatches(text, key string) []int {
	var out []int
	if key == "" {
		return out
	}
	first, _ := utf8.DecodeRuneInString(key)
	last, _ := utf8.DecodeLastRuneInString(key)
	for from := 0; from <= len(text)-len(key); {
		i := strings.Index(text[from:], key)
		if i < 0 {
			break
		}
		at := from + i
		before, _ := utf8.DecodeLastRuneInString(text[:at])
		after, _ := utf8.DecodeRuneInString(text[at+len(key):])
		left := unicode.IsDigit(first) && (unicode.IsDigit(before) || strings.ContainsRune(".,:/+−-", before))
		right := unicode.IsDigit(last) && (unicode.IsDigit(after) || strings.ContainsRune(".,:/%‰", after))
		if !left && !right {
			out = append(out, at)
		}
		_, size := utf8.DecodeRuneInString(text[at:])
		from = at + size
	}
	return out
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

// normalized retains letters, digits and numeric symbols: text holds the
// kept runes, pos[b] the rune offset in the source of the rune starting at
// byte b of text.
type normalized struct {
	text string
	pos  []int
	end  []int
}

// normalizeMarkdown projects markdown onto its letters and digits, skipping
// link and image destinations and HTML tags, whose URLs and attributes are
// not text of the document.
func normalizeMarkdown(markdown string) normalized {
	var b strings.Builder
	b.Grow(len(markdown))
	pos := make([]int, 0, len(markdown))
	ends := make([]int, 0, len(markdown))
	emit := func(r rune, start, end int) {
		before := b.Len()
		b.WriteRune(r)
		for k := before; k < b.Len(); k++ {
			pos = append(pos, start)
			ends = append(ends, end)
		}
	}
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
		if r == '&' {
			if end := indexRune(runes, ';', i+1, 32); end > 0 {
				raw := string(runes[i : end+1])
				if decoded := html.UnescapeString(raw); decoded != raw {
					context := []rune{' '}
					if i > 0 {
						context[0] = runes[i-1]
					}
					context = append(context, []rune(decoded)...)
					context = append(context, ' ')
					if end+1 < len(runes) {
						context[len(context)-1] = runes[end+1]
					}
					for j := 1; j+1 < len(context); j++ {
						if nr, ok := matchRune(context, j); ok {
							emit(nr, i, end+1)
						}
					}
					i = end
					continue
				}
			}
		}
		if nr, ok := matchRune(runes, i); ok {
			emit(nr, i, i+1)
		}
	}
	return normalized{text: b.String(), pos: pos, end: ends}
}

// normalizeKey projects plain text onto its letters and digits.
func normalizeKey(text string) string {
	var b strings.Builder
	runes := []rune(text)
	for i := range runes {
		if r, ok := matchRune(runes, i); ok {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// Preserve distinctions such as 1.5/15, -5/5, 5%/5 and <5/>5.
func matchRune(rs []rune, i int) (rune, bool) {
	r := rs[i]
	if r >= 0xFF01 && r <= 0xFF5E {
		r -= 0xFEE0
	}
	if !strings.ContainsRune(".,:/+−-%‰<>=≤≥≠", r) {
		return foldRune(r)
	}
	before, after := i-1, i+1
	for before >= 0 && (rs[before] == ' ' || rs[before] == '\t') {
		before--
	}
	for after < len(rs) && (rs[after] == ' ' || rs[after] == '\t') {
		after++
	}
	prevDigit := before >= 0 && unicode.IsDigit(rs[before])
	nextDigit := after < len(rs) && unicode.IsDigit(rs[after])
	if ((r == '.' || r == ',' || r == '/' || r == ':') && prevDigit && nextDigit) ||
		((r == '-' || r == '+' || r == '−') && nextDigit) ||
		((r == '%' || r == '‰') && prevDigit) || strings.ContainsRune("<>=≤≥≠", r) {
		if r == '−' {
			r = '-'
		}
		return r, true
	}
	return foldRune(r)
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

// SortBlocks orders blocks by start offset.
func SortBlocks(blocks []types.SourceBlock) {
	sort.SliceStable(blocks, func(i, j int) bool { return blocks[i].Start < blocks[j].Start })
}
