package docparser

import (
	"encoding/json"
	"strings"
	"unicode/utf8"

	"github.com/Tencent/WeKnora/internal/sourceloc"
	"github.com/Tencent/WeKnora/internal/types"
)

// paddleOCRVLPage is one page of a PaddleOCR-VL layout-parsing result.
type paddleOCRVLPage struct {
	Markdown struct {
		Text   string            `json:"text"`
		Images map[string]string `json:"images"`
	} `json:"markdown"`
	// PrunedResult carries the page's layout blocks; decoded lazily and
	// only for the fields source locators need.
	PrunedResult json.RawMessage `json:"prunedResult"`
}

type paddleOCRVLLayout struct {
	Width          float64 `json:"width"`
	Height         float64 `json:"height"`
	ParsingResList []struct {
		BlockContent string    `json:"block_content"`
		BlockBBox    []float64 `json:"block_bbox"`
	} `json:"parsing_res_list"`
}

const paddleOCRVLPageSeparator = "\n\n"

type paddleOCRVLPageLayout struct {
	markdown string
	units    []sourceloc.Unit
}

// paddleOCRVLPageUnits lists a page's layout blocks, each boxed in page
// fractions, for aligning against the merged markdown. Without layout
// blocks the page's markdown paragraphs are listed with the page alone.
func paddleOCRVLPageUnits(pageNumber int, page paddleOCRVLPage) []sourceloc.Unit {
	pageOnly := types.SourceLocator{Type: types.SourceLocatorPDF, Page: pageNumber}
	var layout paddleOCRVLLayout
	if len(page.PrunedResult) > 0 && json.Unmarshal(page.PrunedResult, &layout) == nil &&
		layout.Width > 0 && layout.Height > 0 && len(layout.ParsingResList) > 0 {
		units := make([]sourceloc.Unit, 0, len(layout.ParsingResList))
		for _, b := range layout.ParsingResList {
			loc := pageOnly
			if len(b.BlockBBox) == 4 && b.BlockBBox[2] > b.BlockBBox[0] && b.BlockBBox[3] > b.BlockBBox[1] {
				loc.BBox = []float64{
					clampUnit(b.BlockBBox[0] / layout.Width), clampUnit(b.BlockBBox[1] / layout.Height),
					clampUnit(b.BlockBBox[2] / layout.Width), clampUnit(b.BlockBBox[3] / layout.Height),
				}
			}
			units = append(units, sourceloc.Unit{
				Text:    minerUHTMLTagRe.ReplaceAllString(b.BlockContent, " "),
				Locator: loc,
			})
		}
		return units
	}
	var units []sourceloc.Unit
	for _, para := range strings.Split(page.Markdown.Text, "\n\n") {
		if strings.TrimSpace(para) != "" {
			units = append(units, sourceloc.Unit{Text: para, Locator: pageOnly})
		}
	}
	return units
}

// paddleOCRVLSourceBlocks aligns units within verified page boundaries.
func paddleOCRVLSourceBlocks(markdown string, pages []paddleOCRVLPageLayout, fileType string) []types.SourceBlock {
	ft := strings.ToLower(strings.TrimPrefix(fileType, "."))
	if ft != "pdf" && !IsImageFormat(ft) {
		return nil
	}

	var blocks []types.SourceBlock
	remaining := markdown
	offset := 0
	first := 0
	for ; first < len(pages); first++ {
		page := pages[first]
		if strings.TrimSpace(page.markdown) == "" {
			continue
		}
		text := strings.Trim(NormalizeHTMLTables(page.markdown), "\n")
		beforePage := strings.TrimLeft(remaining, "\n")
		afterPage, found := strings.CutPrefix(beforePage, text)
		if !found || (afterPage != "" && afterPage[0] != '\n') {
			break
		}
		offset += len(remaining) - len(beforePage)
		blocks = append(blocks, alignPaddleOCRVLPage(text, page.units, first+1, offset)...)
		offset += utf8.RuneCountInString(text)
		remaining = afterPage
	}
	if first == len(pages) && strings.Trim(remaining, "\n") == "" {
		return blocks
	}

	// A cross-page rewrite can obscure the boundaries of just a few pages.
	// Recover the unaffected trailing pages before falling back for the middle.
	endOffset := offset + utf8.RuneCountInString(remaining)
	last := len(pages)
	for ; last > first; last-- {
		page := pages[last-1]
		if strings.TrimSpace(page.markdown) == "" {
			continue
		}
		text := strings.Trim(NormalizeHTMLTables(page.markdown), "\n")
		afterPage := strings.TrimRight(remaining, "\n")
		beforePage, found := strings.CutSuffix(afterPage, text)
		if !found || (beforePage != "" && beforePage[len(beforePage)-1] != '\n') {
			break
		}
		endOffset -= len(remaining) - len(afterPage) + utf8.RuneCountInString(text)
		blocks = append(blocks, alignPaddleOCRVLPage(text, page.units, last, endOffset)...)
		remaining = beforePage
	}

	var units []sourceloc.Unit
	for _, page := range pages[first:last] {
		units = append(units, page.units...)
	}
	for _, block := range sourceloc.Align(remaining, units) {
		block.Start += offset
		block.End += offset
		blocks = append(blocks, block)
	}
	sourceloc.SortBlocks(blocks)
	return blocks
}

// A known page can still be cited when its OCR text does not match a box.
func alignPaddleOCRVLPage(text string, units []sourceloc.Unit, page, offset int) []types.SourceBlock {
	runes := []rune(text)
	locator := types.SourceLocator{Type: types.SourceLocatorPDF, Page: page, Mapping: "exact"}
	var blocks []types.SourceBlock
	appendGap := func(start, end int) {
		if start < end && strings.TrimSpace(string(runes[start:end])) != "" {
			blocks = append(blocks, types.SourceBlock{Start: offset + start, End: offset + end, Locator: locator})
		}
	}

	cursor := 0
	for _, block := range sourceloc.Align(text, units) {
		appendGap(cursor, block.Start)
		cursor = block.End
		block.Start += offset
		block.End += offset
		blocks = append(blocks, block)
	}
	appendGap(cursor, len(runes))
	return blocks
}

func clampUnit(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}
