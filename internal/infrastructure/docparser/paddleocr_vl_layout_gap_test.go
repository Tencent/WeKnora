package docparser

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/sourceloc"
	"github.com/Tencent/WeKnora/internal/types"
)

func paddleOCRVLTestPage(t *testing.T, markdown string, layoutTexts ...string) paddleOCRVLPage {
	t.Helper()
	var page paddleOCRVLPage
	page.Markdown.Text = markdown
	if len(layoutTexts) > 0 {
		layout := paddleOCRVLLayout{Width: 1000, Height: 1000}
		for i, text := range layoutTexts {
			layout.ParsingResList = append(layout.ParsingResList, struct {
				BlockContent string    `json:"block_content"`
				BlockBBox    []float64 `json:"block_bbox"`
			}{text, []float64{100, float64(100 + i*100), 900, float64(200 + i*100)}})
		}
		var err error
		page.PrunedResult, err = json.Marshal(layout)
		require.NoError(t, err)
	}
	return page
}

func paddleOCRVLTestMarkdown(pages []paddleOCRVLPage) string {
	var texts []string
	for _, page := range pages {
		if strings.TrimSpace(page.Markdown.Text) != "" {
			texts = append(texts, page.Markdown.Text)
		}
	}
	return strings.Join(texts, "\n\n")
}

func paddleOCRVLTestLocator(t *testing.T, markdown string, blocks []types.SourceBlock, text string, page int, boxed bool) {
	t.Helper()
	at := strings.Index(markdown, text)
	require.NotEqual(t, -1, at)
	start := utf8.RuneCountInString(markdown[:at])
	locs := sourceloc.NewIndex(markdown, blocks).Locators(start, start+utf8.RuneCountInString(text))
	require.Len(t, locs, 1, "%q: %+v", text, blocks)
	require.Equal(t, page, locs[0].Page, "%q", text)
	require.Equal(t, "exact", locs[0].Mapping)
	if boxed {
		require.Len(t, locs[0].BBox, 4, "%q", text)
	} else {
		require.Empty(t, locs[0].BBox, "%q", text)
	}
}

func TestPaddleOCRVLSourceBlocksRepeatedTextStaysOnItsPage(t *testing.T) {
	pages := []paddleOCRVLPage{
		paddleOCRVLTestPage(t, "# Repeated heading\n\nfirst body", "first body"),
		paddleOCRVLTestPage(t, "# Repeated heading\n\nsecond body", "Repeated heading", "second body"),
	}
	md := paddleOCRVLTestMarkdown(pages)
	blocks := paddleOCRVLSourceBlocks(pages, ".pdf")
	require.Len(t, blocks, 4)
	require.Equal(t, 1, blocks[0].Locator.Page)
	require.Empty(t, blocks[0].Locator.BBox)
	require.Equal(t, 2, blocks[2].Locator.Page)
	require.Equal(t, []float64{0.1, 0.1, 0.9, 0.2}, blocks[2].Locator.BBox)
	paddleOCRVLTestLocator(t, md, blocks, "first body", 1, true)
	paddleOCRVLTestLocator(t, md, blocks, "second body", 2, true)
}

func TestPaddleOCRVLSourceBlocksDoesNotBorrowAdjacentPageContent(t *testing.T) {
	pages := []paddleOCRVLPage{
		paddleOCRVLTestPage(t, "first page", "first page", "neighbor heading"),
		paddleOCRVLTestPage(t, "neighbor heading\n\nsecond page", "absent layout text", "second page"),
		paddleOCRVLTestPage(t, "third page has no matching layout", "missing footer", "first page"),
	}
	md := paddleOCRVLTestMarkdown(pages)
	blocks := paddleOCRVLSourceBlocks(pages, "pdf")
	paddleOCRVLTestLocator(t, md, blocks, "first page", 1, true)
	paddleOCRVLTestLocator(t, md, blocks, "neighbor heading", 2, false)
	paddleOCRVLTestLocator(t, md, blocks, "second page", 2, true)
	paddleOCRVLTestLocator(t, md, blocks, "third page has no matching layout", 3, false)
	for _, block := range blocks {
		text := string([]rune(md)[block.Start:block.End])
		require.Contains(t, pages[block.Locator.Page-1].Markdown.Text, text)
	}
}

func TestPaddleOCRVLSourceBlocksRecoversMultipleUnmatchedPages(t *testing.T) {
	pages := []paddleOCRVLPage{
		paddleOCRVLTestPage(t, "  \n"),
		paddleOCRVLTestPage(t, "第一张扫描件的正文", "unmatched table"),
		paddleOCRVLTestPage(t, "第二张扫描件的正文", "unmatched seal"),
		paddleOCRVLTestPage(t, "last page", "last page"),
	}
	md := paddleOCRVLTestMarkdown(pages)
	blocks := paddleOCRVLSourceBlocks(pages, "PDF")
	require.Len(t, blocks, 3)
	paddleOCRVLTestLocator(t, md, blocks, "第一张扫描件的正文", 2, false)
	paddleOCRVLTestLocator(t, md, blocks, "第二张扫描件的正文", 3, false)
	paddleOCRVLTestLocator(t, md, blocks, "last page", 4, true)
	require.Equal(t, 0, blocks[0].Start)
	require.Equal(t, utf8.RuneCountInString(pages[1].Markdown.Text), blocks[0].End)
}

func TestPaddleOCRVLSourceBlocksTableDivergenceIsOrderedAndRemappable(t *testing.T) {
	pages := []paddleOCRVLPage{
		paddleOCRVLTestPage(t, "序言段落\n\n<table><tr><th>项目</th><th>值</th></tr><tr><td>甲</td><td>42</td></tr></table>\n\n末尾段落",
			"序言段落", "项目 值 乙 99", "末尾段落"),
		paddleOCRVLTestPage(t, "next page", "next page"),
	}
	md := paddleOCRVLTestMarkdown(pages)
	blocks := paddleOCRVLSourceBlocks(pages, "pdf")
	require.Len(t, blocks, 4)
	for i := 1; i < len(blocks); i++ {
		require.LessOrEqual(t, blocks[i-1].End, blocks[i].Start, "blocks must be ordered before remapping")
	}
	paddleOCRVLTestLocator(t, md, blocks, "序言段落", 1, true)
	paddleOCRVLTestLocator(t, md, blocks, "42", 1, false)
	paddleOCRVLTestLocator(t, md, blocks, "末尾段落", 1, true)
	paddleOCRVLTestLocator(t, md, blocks, "next page", 2, true)

	final := NormalizeHTMLTables(md)
	require.NotEqual(t, md, final)
	remapped := sourceloc.RemapBlocks(blocks, md, final)
	require.Len(t, remapped, 4)
	paddleOCRVLTestLocator(t, final, remapped, "序言段落", 1, true)
	paddleOCRVLTestLocator(t, final, remapped, "42", 1, false)
	paddleOCRVLTestLocator(t, final, remapped, "末尾段落", 1, true)
	paddleOCRVLTestLocator(t, final, remapped, "next page", 2, true)
	require.Equal(t, blocks[0].Locator.BBox, remapped[0].Locator.BBox)
	require.Equal(t, blocks[2].Locator.BBox, remapped[2].Locator.BBox)
}

func TestPaddleOCRVLSourceBlocksEmptyAndUnpagedInputs(t *testing.T) {
	require.Nil(t, paddleOCRVLSourceBlocks(nil, "pdf"))
	require.Nil(t, paddleOCRVLSourceBlocks([]paddleOCRVLPage{paddleOCRVLTestPage(t, "\n \t")}, "pdf"))
	for _, fileType := range []string{"pdf", ".png"} {
		pages := []paddleOCRVLPage{paddleOCRVLTestPage(t, "x", "missing text")}
		blocks := paddleOCRVLSourceBlocks(pages, fileType)
		require.Len(t, blocks, 1)
		paddleOCRVLTestLocator(t, "x", blocks, "x", 1, false)
	}
	require.Nil(t, paddleOCRVLSourceBlocks([]paddleOCRVLPage{paddleOCRVLTestPage(t, "text")}, ".docx"))
}
