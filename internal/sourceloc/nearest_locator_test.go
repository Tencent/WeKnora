package sourceloc

import (
	"testing"
	"unicode/utf8"

	"github.com/Tencent/WeKnora/internal/types"
)

// pdfBlock is a one-page PDF block spanning runes [start, end) of the markdown.
func pdfBlock(start, end, page int) types.SourceBlock {
	return types.SourceBlock{
		Start: start,
		End:   end,
		Locator: types.SourceLocator{
			Type:  types.SourceLocatorPDF,
			Page:  page,
			BBox:  []float64{0.1, 0.2, 0.3, 0.4},
			Quote: "quote",
		},
	}
}

func runeOfAt(t *testing.T, markdown, needle string) int {
	t.Helper()
	byteAt := indexOf(markdown, needle)
	if byteAt < 0 {
		t.Fatalf("needle %q not in markdown", needle)
	}
	return utf8.RuneCountInString(markdown[:byteAt])
}

func indexOf(haystack, needle string) int {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return i
		}
	}
	return -1
}

// An image reference sits on its own line between two text blocks, so a strict
// containment test finds nothing. Without the fallback the image loses its page
// and the gallery cannot order by page.
func TestNearestLocatorsAtFallsBackToPrecedingBlock(t *testing.T) {
	md := "第一段文字\n\n![](local://a.png)\n\n第二段文字"
	blocks := []types.SourceBlock{pdfBlock(0, 5, 1), pdfBlock(26, 31, 2)}
	idx := NewIndex(md, blocks)
	pos := runeOfAt(t, md, "![")

	if got := idx.LocatorsAt(pos); len(got) != 0 {
		t.Fatalf("LocatorsAt must stay strict, got %+v", got)
	}
	got := idx.NearestLocatorsAt(pos)
	if len(got) != 1 || got[0].Page != 1 {
		t.Fatalf("expected page 1 from the preceding block, got %+v", got)
	}
	if !got[0].Partial {
		t.Error("an inherited locator must be Partial: the page is exact, the region is not")
	}
	if len(got[0].BBox) != 0 {
		t.Errorf("an inherited locator must not carry the text block's BBox, got %v", got[0].BBox)
	}
	if got[0].Quote != "" {
		t.Errorf("an image has no text to quote, got %q", got[0].Quote)
	}
}

// A position inside a block keeps the exact behaviour, BBox included.
func TestNearestLocatorsAtKeepsContainingBlock(t *testing.T) {
	md := "第一段文字\n\n![](local://a.png)"
	blocks := []types.SourceBlock{pdfBlock(0, 5, 3)}
	idx := NewIndex(md, blocks)

	got := idx.NearestLocatorsAt(2)
	if len(got) != 1 || got[0].Page != 3 || len(got[0].BBox) == 0 {
		t.Fatalf("expected the containing block with its BBox, got %+v", got)
	}
}

// An image above all text has no preceding block; it belongs to the first page.
func TestNearestLocatorsAtFallsForwardWhenNoPrecedingBlock(t *testing.T) {
	md := "![](local://a.png)\n\n正文开始"
	blocks := []types.SourceBlock{pdfBlock(20, 24, 7)}
	idx := NewIndex(md, blocks)

	got := idx.NearestLocatorsAt(runeOfAt(t, md, "!["))
	if len(got) != 1 || got[0].Page != 7 {
		t.Fatalf("expected page 7 from the following block, got %+v", got)
	}
}

func TestNearestLocatorsAtWithoutBlocks(t *testing.T) {
	if got := NewIndex("任意内容", nil).NearestLocatorsAt(0); got != nil {
		t.Fatalf("expected nil without blocks, got %+v", got)
	}
	var nilIdx *Index
	if got := nilIdx.NearestLocatorsAt(0); got != nil {
		t.Fatalf("expected nil on a nil index, got %+v", got)
	}
}
