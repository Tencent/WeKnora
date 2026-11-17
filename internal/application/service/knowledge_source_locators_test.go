package service

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/infrastructure/docparser"
	"github.com/Tencent/WeKnora/internal/models/asr"
	"github.com/Tencent/WeKnora/internal/types"
)

func TestAttachStructureBlocksForPassThroughText(t *testing.T) {
	content := "# 标题\n\n正文一段\n\n正文二段"
	result := &types.ReadResult{MarkdownContent: content}
	attachStructureBlocks(context.Background(), "md", []byte(content), result)
	require.Len(t, result.SourceBlocks, 3)
	assert.Equal(t, types.SourceLocatorText, result.SourceBlocks[1].Locator.Type)

	// A parser that rewrote the text gets no text locators.
	rewritten := &types.ReadResult{MarkdownContent: "different"}
	attachStructureBlocks(context.Background(), "txt", []byte(content), rewritten)
	assert.Empty(t, rewritten.SourceBlocks)

	// Blocks a parser already reported are kept.
	kept := &types.ReadResult{MarkdownContent: content, SourceBlocks: []types.SourceBlock{{Start: 0, End: 1}}}
	attachStructureBlocks(context.Background(), "md", []byte(content), kept)
	assert.Len(t, kept.SourceBlocks, 1)

	// Viewers decode without the BOM, so text offsets start after it while
	// the blocks still cover the markdown that holds it.
	withBOM := "\ufeff正文一段\n\n正文二段"
	bom := &types.ReadResult{MarkdownContent: withBOM}
	attachStructureBlocks(context.Background(), "txt", []byte(withBOM), bom)
	require.Len(t, bom.SourceBlocks, 2)
	assert.Equal(t, 1, bom.SourceBlocks[0].Start)
	assert.Equal(t, 0, bom.SourceBlocks[0].Locator.Start)
	assert.Equal(t, 6, bom.SourceBlocks[1].Locator.Start)
	assert.Equal(t, 7, bom.SourceBlocks[1].Start)
}

func TestTranscriptWithSegments(t *testing.T) {
	text, blocks := transcriptWithSegments(&asr.TranscriptionResult{
		Text: "你好世界。再见。",
		Segments: []asr.Segment{
			{Start: 0, End: 1.5, Text: " 你好世界。"},
			{Start: 1.5, End: 2.25, Text: "再见。"},
		},
	})
	assert.Equal(t, "你好世界。\n再见。", text)
	require.Len(t, blocks, 2)
	assert.Equal(t, 6, blocks[1].Start)
	assert.Equal(t, int64(1500), blocks[1].Locator.StartMs)
	assert.Equal(t, int64(2250), blocks[1].Locator.EndMs)

	text, blocks = transcriptWithSegments(&asr.TranscriptionResult{Text: "plain"})
	assert.Equal(t, "plain", text)
	assert.Nil(t, blocks)
}

func TestBuildSourceIndexFollowsRewritesAndPlacesImages(t *testing.T) {
	parsed := "第一页文字\r\n\r\n![p2](images/p2.jpg)"
	final := "第一页文字\n\n![p2](local://tenant/p2.jpg)"
	blocks := []types.SourceBlock{
		{Start: 0, End: 5, Locator: types.SourceLocator{Type: types.SourceLocatorPDF, Page: 1}},
		{Start: 9, End: len([]rune(parsed)), Locator: types.SourceLocator{Type: types.SourceLocatorPDF, Page: 2}},
	}
	images := []docparser.StoredImage{{ServingURL: "local://tenant/p2.jpg"}}
	idx := buildSourceIndex(parsed, blocks, final, images, nil)
	require.NotNil(t, idx)
	locs := idx.Locators(0, 5)
	require.Len(t, locs, 1)
	assert.Equal(t, "第一页文字", locs[0].Quote)
	require.Len(t, images[0].SourceLocators, 1)
	assert.Equal(t, 2, images[0].SourceLocators[0].Page)

	assert.Nil(t, buildSourceIndex("x", nil, "x", nil, nil))
}

// sheetMarkdown builds a sheet whose only text is one caption block, followed
// by count image references: the shape that put 274 images of the 5-2 volume on
// page 26. Each image reference is rewritten to a serving URL, as ingestion does
// before the chunker runs.
func sheetMarkdown(count int) (parsed, final string, images []docparser.StoredImage,
	locators map[string]types.SourceLocator,
) {
	var parsedB, finalB strings.Builder
	parsedB.WriteString("图签文字")
	finalB.WriteString("图签文字")
	locators = make(map[string]types.SourceLocator, count)
	images = make([]docparser.StoredImage, 0, count)
	for i := 0; i < count; i++ {
		ref := fmt.Sprintf("images/page_%d_image_0.jpg", i+2)
		serving := fmt.Sprintf("local://tenant/%d.jpg", i)
		parsedB.WriteString("\n\n![](" + ref + ")")
		finalB.WriteString("\n\n![](" + serving + ")")
		images = append(images, docparser.StoredImage{OriginalRef: ref, ServingURL: serving})
		locators[ref] = types.SourceLocator{
			Type:     types.SourceLocatorPDF,
			Page:     i + 2,
			SourceID: fmt.Sprintf("mineru:2:%d:8/0", i+1),
			BBox:     []float64{0.1, 0.2, 0.3, 0.4},
		}
	}
	return parsedB.String(), finalB.String(), images, locators
}

// One caption block followed by a hundred figures: inheriting the page of the
// text above puts all of them on page 1, which is what the gallery showed. The
// page the layout recorded for each image has to win.
func TestBuildSourceIndexPlacesEachLaidOutImageOnItsOwnPage(t *testing.T) {
	const count = 100
	parsed, final, images, locators := sheetMarkdown(count)
	// The single text block covers the caption and nothing else: every image
	// below it has no containing block at all.
	blocks := []types.SourceBlock{{
		Start:   0,
		End:     4,
		Locator: types.SourceLocator{Type: types.SourceLocatorPDF, Page: 1, SourceID: "mineru:2:25:7"},
	}}

	idx := buildSourceIndex(parsed, blocks, final, images, locators)
	require.NotNil(t, idx)

	pages := make(map[int]int, count)
	sourceIDs := make(map[string]int, count)
	for _, image := range images {
		require.Len(t, image.SourceLocators, 1, image.OriginalRef)
		loc := image.SourceLocators[0]
		require.False(t, loc.Partial, "a layout-placed image knows its page and region")
		require.Len(t, loc.BBox, 4, image.OriginalRef)
		pages[loc.Page]++
		sourceIDs[loc.SourceID]++
	}
	require.Len(t, pages, count, "every image keeps its own page")
	require.Len(t, sourceIDs, count, "every image keeps its own block")
	require.NotContains(t, pages, 1, "no image inherits the caption page")
	require.Equal(t, 2, images[0].SourceLocators[0].Page)
	require.Equal(t, count+1, images[count-1].SourceLocators[0].Page)
}

// A parser that reports no image geometry keeps the old behaviour: the image
// takes the page of the text it follows rather than losing its page.
func TestBuildSourceIndexFallsBackToPrecedingBlockWithoutLayoutLocator(t *testing.T) {
	parsed, final, images, _ := sheetMarkdown(3)
	blocks := []types.SourceBlock{{
		Start:   0,
		End:     4,
		Locator: types.SourceLocator{Type: types.SourceLocatorPDF, Page: 1, SourceID: "mineru:2:25:7"},
	}}

	buildSourceIndex(parsed, blocks, final, images, nil)

	for _, image := range images {
		require.Len(t, image.SourceLocators, 1, image.OriginalRef)
		require.Equal(t, 1, image.SourceLocators[0].Page)
		require.Equal(t, "mineru:2:25:7", image.SourceLocators[0].SourceID,
			"the page still comes from the text block, not from an image block")
	}
}

// A layout locator is keyed by the image reference, so an image the layout did
// not report keeps the offset-based fallback even when its siblings have one.
func TestBuildSourceIndexMixesLayoutAndFallbackPlacement(t *testing.T) {
	parsed, final, images, locators := sheetMarkdown(3)
	delete(locators, images[1].OriginalRef)
	blocks := []types.SourceBlock{{
		Start:   0,
		End:     4,
		Locator: types.SourceLocator{Type: types.SourceLocatorPDF, Page: 1, SourceID: "mineru:2:25:7"},
	}}

	buildSourceIndex(parsed, blocks, final, images, locators)

	require.Equal(t, 2, images[0].SourceLocators[0].Page)
	require.Equal(t, "mineru:2:1:8/0", images[0].SourceLocators[0].SourceID)
	require.Equal(t, 1, images[1].SourceLocators[0].Page)
	require.Equal(t, "mineru:2:25:7", images[1].SourceLocators[0].SourceID,
		"the image the layout did not report keeps the offset-based fallback")
	require.Equal(t, 4, images[2].SourceLocators[0].Page)
	require.Equal(t, "mineru:2:3:8/0", images[2].SourceLocators[0].SourceID)
}
