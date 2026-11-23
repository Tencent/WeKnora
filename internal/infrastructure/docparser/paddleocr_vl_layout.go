package docparser

import (
	"encoding/json"
	"strings"

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

// paddleOCRVLPageUnits lists a page's layout blocks, each boxed in page
// fractions, for aligning against that page's markdown. Without layout
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

// paddleOCRVLSourceBlocks aligns each page independently, so repeated text
// and omitted layout blocks cannot affect another page's matches. Unmatched
// markdown has a known page but no reliable box. Offsets follow the readers'
// nonempty-page join; callers remap these blocks after rewriting the markdown.
func paddleOCRVLSourceBlocks(pages []paddleOCRVLPage, fileType string) []types.SourceBlock {
	ft := strings.ToLower(strings.TrimPrefix(fileType, "."))
	if ft != "pdf" && !IsImageFormat(ft) {
		return nil
	}
	var blocks []types.SourceBlock
	offset := 0
	for i, page := range pages {
		if strings.TrimSpace(page.Markdown.Text) == "" {
			continue
		}
		if offset > 0 {
			offset += 2 // strings.Join(texts, "\n\n")
		}
		runes := []rune(page.Markdown.Text)
		appendGap := func(start, end int) {
			if strings.TrimSpace(string(runes[start:end])) != "" {
				blocks = append(blocks, types.SourceBlock{
					Start: offset + start,
					End:   offset + end,
					Locator: types.SourceLocator{
						Type: types.SourceLocatorPDF, Page: i + 1, Mapping: "exact",
					},
				})
			}
		}
		end := 0
		for _, block := range sourceloc.Align(page.Markdown.Text, paddleOCRVLPageUnits(i+1, page)) {
			appendGap(end, block.Start)
			end = block.End
			block.Start += offset
			block.End += offset
			blocks = append(blocks, block)
		}
		appendGap(end, len(runes))
		offset += len(runes)
	}
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
