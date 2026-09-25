package service

import (
	"context"
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
