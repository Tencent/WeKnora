package sourceloc

import (
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/Tencent/WeKnora/internal/types"
)

// MaxLocatorsPerChunk caps how many locators one chunk (or merged search
// result) carries.
const MaxLocatorsPerChunk = 64

// Index answers "which source positions does this range of the final
// Markdown cover" for the chunker's output.
type Index struct {
	runes  []rune
	blocks []types.SourceBlock // sorted by Start
	maxEnd []int               // maxEnd[k] = max End over blocks[:k+1]
}

// NewIndex builds an index over blocks recorded against markdown.
func NewIndex(markdown string, blocks []types.SourceBlock) *Index {
	if len(blocks) == 0 {
		return nil
	}
	sorted := append([]types.SourceBlock(nil), blocks...)
	SortBlocks(sorted)
	maxEnd := make([]int, len(sorted))
	for k, b := range sorted {
		maxEnd[k] = b.End
		if k > 0 && maxEnd[k-1] > b.End {
			maxEnd[k] = maxEnd[k-1]
		}
	}
	return &Index{runes: []rune(markdown), blocks: sorted, maxEnd: maxEnd}
}

// Locators returns the locators of the blocks overlapping the rune range
// [start, end), each quoting the overlapping text. Adjacent locators that
// point at the same place (one page without a region, one slide, a run of
// rows) are folded together.
func (x *Index) Locators(start, end int) types.SourceLocators {
	if x == nil || end <= start {
		return nil
	}
	start = max(start, 0)
	end = min(end, len(x.runes))
	// Blocks before i all end at or before start.
	i := sort.Search(len(x.blocks), func(k int) bool { return x.maxEnd[k] > start })
	var out types.SourceLocators
	for ; i < len(x.blocks) && x.blocks[i].Start < end; i++ {
		b := x.blocks[i]
		lo, hi := max(b.Start, start), min(b.End, end)
		if hi <= lo {
			continue
		}
		quote := CleanQuote(string(x.runes[lo:hi]))
		if quote == "" && b.Locator.Type != types.SourceLocatorPDF {
			continue
		}
		loc := b.Locator
		loc.BBox = append([]float64(nil), loc.BBox...)
		loc.Quote = quote
		out = AppendLocator(out, loc)
	}
	if len(out) > MaxLocatorsPerChunk {
		out = out[:MaxLocatorsPerChunk]
	}
	return out
}

// LocatorsAt returns the locators of the blocks containing rune offset pos,
// without quotes. Used to place an image reference found in the markdown.
func (x *Index) LocatorsAt(pos int) types.SourceLocators {
	if x == nil || pos < 0 {
		return nil
	}
	i := sort.Search(len(x.blocks), func(k int) bool { return x.maxEnd[k] > pos })
	var out types.SourceLocators
	for ; i < len(x.blocks) && x.blocks[i].Start <= pos; i++ {
		if b := x.blocks[i]; b.End > pos {
			loc := b.Locator
			loc.BBox = append([]float64(nil), loc.BBox...)
			loc.Quote = ""
			out = append(out, loc)
		}
	}
	return out
}

// AppendLocator appends loc to list, folding it into the last entry when both
// point at the same place.
func AppendLocator(list types.SourceLocators, loc types.SourceLocator) types.SourceLocators {
	if n := len(list); n > 0 && foldInto(&list[n-1], loc) {
		return list
	}
	return append(list, loc)
}

func foldInto(last *types.SourceLocator, loc types.SourceLocator) bool {
	if last.Type != loc.Type {
		return false
	}
	switch loc.Type {
	case types.SourceLocatorPDF:
		if last.Page != loc.Page || len(last.BBox) > 0 || len(loc.BBox) > 0 {
			return false
		}
	case types.SourceLocatorSlide:
		if last.Slide != loc.Slide {
			return false
		}
	case types.SourceLocatorSection:
		if last.Section != loc.Section {
			return false
		}
	case types.SourceLocatorSheet:
		if last.Sheet != loc.Sheet || loc.RowStart > last.RowEnd+1 || loc.RowEnd < last.RowStart {
			return false
		}
		last.RowStart = min(last.RowStart, loc.RowStart)
		last.RowEnd = max(last.RowEnd, loc.RowEnd)
	case types.SourceLocatorText:
		if loc.Start > last.End+2 || loc.End < last.Start {
			return false
		}
		last.Start = min(last.Start, loc.Start)
		last.End = max(last.End, loc.End)
	default:
		return false
	}
	last.Quote = joinQuote(last.Quote, loc.Quote)
	return true
}

// MergeLocators unions two locator lists, dropping exact duplicates, for
// search results that the chat pipeline merges.
func MergeLocators(a, b types.SourceLocators) types.SourceLocators {
	if len(b) == 0 {
		return a
	}
	if len(a) == 0 {
		return b
	}
	out := append(types.SourceLocators(nil), a...)
	for _, loc := range b {
		dup := false
		for _, have := range out {
			if sameLocator(have, loc) {
				dup = true
				break
			}
		}
		if !dup {
			out = append(out, loc)
		}
	}
	if len(out) > MaxLocatorsPerChunk {
		out = out[:MaxLocatorsPerChunk]
	}
	return out
}

func sameLocator(a, b types.SourceLocator) bool {
	if a.Type != b.Type || a.Page != b.Page || a.Block != b.Block || a.Slide != b.Slide ||
		a.Sheet != b.Sheet || a.RowStart != b.RowStart || a.RowEnd != b.RowEnd ||
		a.Start != b.Start || a.End != b.End || a.StartMs != b.StartMs || a.EndMs != b.EndMs ||
		a.Section != b.Section || len(a.BBox) != len(b.BBox) {
		return false
	}
	for i := range a.BBox {
		if a.BBox[i] != b.BBox[i] {
			return false
		}
	}
	return true
}

var (
	quoteImageRe = regexp.MustCompile(`!\[[^\]\n]*\]\([^)\n]*\)`)
	quoteLinkRe  = regexp.MustCompile(`\[([^\]\n]*)\]\([^)\n]*\)`)
	quoteTagRe   = regexp.MustCompile(`</?[a-zA-Z][^>\n]*>|<!--.*?-->`)
	quoteSpaceRe = regexp.MustCompile(`\s+`)
)

// CleanQuote strips Markdown image and link destinations and HTML tags from
// text, collapses whitespace, and caps it at types.SourceLocatorQuoteMax
// runes.
func CleanQuote(text string) string {
	text = quoteImageRe.ReplaceAllString(text, " ")
	text = quoteLinkRe.ReplaceAllString(text, "$1")
	text = quoteTagRe.ReplaceAllString(text, " ")
	text = strings.TrimSpace(quoteSpaceRe.ReplaceAllString(text, " "))
	return capRunes(text, types.SourceLocatorQuoteMax)
}

func joinQuote(a, b string) string {
	if a == "" {
		return b
	}
	if b == "" || utf8.RuneCountInString(a) >= types.SourceLocatorQuoteMax {
		return a
	}
	return capRunes(a+" "+b, types.SourceLocatorQuoteMax)
}

func capRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n])
}
