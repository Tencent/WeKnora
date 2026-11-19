package service

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/infrastructure/chunker"
	"github.com/Tencent/WeKnora/internal/infrastructure/docparser"
	"github.com/Tencent/WeKnora/internal/models/asr"
	"github.com/Tencent/WeKnora/internal/types"
)

func TestCSVSourceRowsSurviveChunking(t *testing.T) {
	data := []byte("\xef\xbb\xbfitem,note,qty\n\nItem,--,3\n" +
		"苹果,\"说明一\n说明二\",\"备注一\n备注二\"\n\nItem,--,3\nx,,1\n")
	result, err := (&docparser.SimpleFormatReader{}).Read(context.Background(), &types.ReadRequest{
		FileType: "csv", FileContent: data,
	})
	require.NoError(t, err)
	blocks := append([]types.SourceBlock(nil), result.SourceBlocks...)
	require.Len(t, blocks, 5)
	attachStructureBlocks(context.Background(), "csv", data, result)
	require.Equal(t, blocks, result.SourceBlocks, "keep converter mappings instead of text alignment")
	final := docparser.NormalizeHTMLTables(chunker.NormalizeLineEndings(result.MarkdownContent))
	index := buildSourceIndex(result.MarkdownContent, result.SourceBlocks, final, nil)
	require.NotNil(t, index)
	chunks := chunker.Split(final, chunker.SplitterConfig{ChunkSize: 64, ChunkOverlap: 0})
	require.Greater(t, len(chunks), 1)
	seen := make(map[int]bool)
	for _, chunk := range chunks {
		locators := index.Locators(chunk.Start, chunk.End)
		require.NotEmpty(t, locators, "chunk %q lost its CSV source", chunk.Content)
		for _, locator := range locators {
			require.Equal(t, types.SourceLocatorSheet, locator.Type)
			require.Equal(t, "exact", locator.Mapping)
			require.False(t, locator.Partial, "chunk %q has unmapped content", chunk.Content)
			for row := locator.RowStart; row <= locator.RowEnd; row++ {
				seen[row] = true
			}
			if strings.Contains(locator.Quote, "说明一") {
				require.LessOrEqual(t, locator.RowStart, 4)
				require.GreaterOrEqual(t, locator.RowEnd, 4)
			}
		}
	}
	require.Equal(t, map[int]bool{1: true, 3: true, 4: true, 6: true, 7: true}, seen)
}

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
	idx := buildSourceIndex(parsed, blocks, final, images)
	require.NotNil(t, idx)
	locs := idx.Locators(0, 5)
	require.Len(t, locs, 1)
	assert.Equal(t, "第一页文字", locs[0].Quote)
	require.Len(t, images[0].SourceLocators, 1)
	assert.Equal(t, 2, images[0].SourceLocators[0].Page)

	assert.Nil(t, buildSourceIndex("x", nil, "x", nil))
}
