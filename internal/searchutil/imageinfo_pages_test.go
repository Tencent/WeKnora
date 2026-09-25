package searchutil

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

func pageChild(id, parentID string, chunkType types.ChunkType, page int, content string) *types.Chunk {
	return &types.Chunk{
		ID:             id,
		ParentChunkID:  parentID,
		ChunkType:      chunkType,
		IsEnabled:      true,
		Content:        content,
		SourceLocators: types.SourceLocators{{Type: types.SourceLocatorPDF, Page: page}},
	}
}

// A scanned PDF chunk holds only page images, so its page locators have no
// text to match a cited sentence against; the pages' OCR text supplies it.
func TestEnrichSearchResultsGivesScannedPagesTheirOCRText(t *testing.T) {
	repo := childrenByParentRepo{byParent: map[string][]*types.Chunk{
		"scan": {
			pageChild("cap-1", "scan", types.ChunkTypeImageCaption, 1, "A form about working at height"),
			pageChild("ocr-1", "scan", types.ChunkTypeImageOCR, 1, "一、作业人员必须持证上岗。"),
			pageChild("ocr-2", "scan", types.ChunkTypeImageOCR, 2, "五、吊篮每班作业前应进行空载试运行。"),
		},
	}}
	boxed := types.SourceLocators{{Type: types.SourceLocatorPDF, Page: 3, BBox: []float64{0, 0, 1, 1}, Quote: "text"}}
	results := []*types.SearchResult{
		{ID: "scan", SourceLocators: types.SourceLocators{
			{Type: types.SourceLocatorPDF, Page: 1, Quote: "![page_1.jpg](resource://a"},
			{Type: types.SourceLocatorPDF, Page: 2},
		}},
		{ID: "text", SourceLocators: boxed},
	}

	EnrichSearchResultsImageInfo(context.Background(), repo, 1, results)

	got := results[0].SourceLocators
	if got[0].Quote != "一、作业人员必须持证上岗。" || got[1].Quote != "五、吊篮每班作业前应进行空载试运行。" {
		t.Fatalf("page quotes = %q, %q", got[0].Quote, got[1].Quote)
	}
	if results[1].SourceLocators[0].Quote != "text" {
		t.Fatalf("a boxed text locator must keep its quote, got %q", results[1].SourceLocators[0].Quote)
	}
}
