package core

import (
	"bytes"
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestContainerPreservesNestedListContent(t *testing.T) {
	for _, kind := range []int{BlockTypeCallout, BlockTypeQuoteContainer, BlockTypeGrid, BlockTypeView} {
		t.Run(blockNameForTest(kind), func(t *testing.T) {
			blocks := []DocxBlock{
				{BlockID: "container", BlockType: kind, Children: []string{"list"}},
				{
					BlockID: "list", ParentID: "container", BlockType: BlockTypeBullet,
					Bullet: txt("parent"), Children: []string{"nested"},
				},
				{
					BlockID: "nested", ParentID: "list", BlockType: BlockTypeOrdered,
					Ordered: txt("nested"), Children: []string{"text", "image", "file"},
				},
				{BlockID: "text", ParentID: "nested", BlockType: BlockTypeText, Text: txt("important nested content")},
				{BlockID: "image", ParentID: "nested", BlockType: BlockTypeImage, Image: &BlockTokenRef{Token: "img"}},
				{
					BlockID: "file", ParentID: "nested", BlockType: BlockTypeFile,
					File: &BlockFileRef{Token: "pdf", Name: "report.pdf"},
				},
			}
			if kind == BlockTypeGrid {
				blocks[0].Children = []string{"column"}
				blocks = append(blocks, DocxBlock{
					BlockID: "column", BlockType: BlockTypeGridColumn, Children: []string{"list"},
				})
			}
			md, atts, imgs, _, err := blocksToMarkdown(context.Background(), nil, blocks, "", nil)
			require.NoError(t, err)
			for _, text := range []string{"parent", "nested content", "weknora-att://pdf", "weknora-img://"} {
				require.Equal(t, 1, strings.Count(string(md), text), "content must appear exactly once: %s", md)
			}
			require.Len(t, atts, 1)
			require.Len(t, imgs, 1)
			require.Contains(t, string(md), "  1. nested")
		})
	}
}

func blockNameForTest(kind int) string {
	switch kind {
	case BlockTypeCallout:
		return "callout"
	case BlockTypeQuoteContainer:
		return "quote"
	case BlockTypeGrid:
		return "grid"
	default:
		return "view"
	}
}

func TestContainerNestedOrderedListKeepsParentSequence(t *testing.T) {
	blocks := []DocxBlock{
		{BlockID: "container", BlockType: BlockTypeView, Children: []string{"first", "second"}},
		{
			BlockID: "first", ParentID: "container", BlockType: BlockTypeOrdered,
			Ordered: txt("parent one"), Children: []string{"nested"},
		},
		{BlockID: "nested", ParentID: "first", BlockType: BlockTypeOrdered, Ordered: txt("child one")},
		{BlockID: "second", ParentID: "container", BlockType: BlockTypeOrdered, Ordered: txt("parent two")},
	}
	md, _, _, _, err := blocksToMarkdown(context.Background(), nil, blocks, "", nil)
	require.NoError(t, err)
	require.Contains(t, string(md), "1. parent one\n\n   1. child one\n2. parent two")
}

func TestBoardInNativeTableIsDownloaded(t *testing.T) {
	for _, inContainer := range []bool{false, true} {
		t.Run(map[bool]string{false: "table", true: "table in callout"}[inContainer], func(t *testing.T) {
			blocks := []DocxBlock{
				{
					BlockID: "table", BlockType: BlockTypeTable, Children: []string{"cell"},
					Table: &BlockTable{Cells: []string{"cell"}, Property: &BlockTableProperty{ColumnSize: 1}},
				},
				{BlockID: "cell", BlockType: BlockTypeTableCell, Children: []string{"text", "board"}},
				{BlockID: "text", BlockType: BlockTypeText, Text: txt("board caption")},
				{BlockID: "board", BlockType: BlockTypeBoard, Board: &BlockBoard{Token: "brd-1"}},
			}
			if inContainer {
				blocks = append([]DocxBlock{
					{BlockID: "callout", BlockType: BlockTypeCallout, Children: []string{"table"}},
				}, blocks...)
			}
			png := append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte("x"), MinAttachmentBytes)...)
			server := fakeFeishuForBoard(t, blocks, http.StatusOK, png)
			client := NewClient(&Config{AppID: "a", AppSecret: "b", BaseURL: server.URL})
			items, err := FetchDocxWithBlocks(context.Background(), client, boardFetchInput())
			require.NoError(t, err)
			require.Len(t, items, 1)
			md := string(items[0].Content)
			require.Equal(t, 1, strings.Count(md, "data:image/png;base64,"))
			require.Equal(t, 1, strings.Count(md, "board caption"))
			require.NotContains(t, md, "weknora-img://")
		})
	}
}

func TestContainerPreservesMalformedTableChildren(t *testing.T) {
	blocks := []DocxBlock{
		{BlockID: "view", BlockType: BlockTypeView, Children: []string{"table"}},
		{BlockID: "table", BlockType: BlockTypeTable, Children: []string{"cell"}},
		{BlockID: "cell", BlockType: BlockTypeTableCell, Children: []string{"text"}},
		{BlockID: "text", BlockType: BlockTypeText, Text: txt("retained content")},
	}
	md, _, _, _, err := blocksToMarkdown(context.Background(), nil, blocks, "", nil)
	require.NoError(t, err)
	require.Equal(t, 1, strings.Count(string(md), "retained content"))
}

func TestContainerCyclicChildrenTerminate(t *testing.T) {
	blocks := []DocxBlock{
		{BlockID: "view", BlockType: BlockTypeView, Children: []string{"list"}},
		{BlockID: "list", BlockType: BlockTypeBullet, Bullet: txt("cyclic content"), Children: []string{"list"}},
	}
	md, _, _, _, err := blocksToMarkdown(context.Background(), nil, blocks, "", nil)
	require.NoError(t, err)
	require.Equal(t, 1, strings.Count(string(md), "cyclic content"))
}
