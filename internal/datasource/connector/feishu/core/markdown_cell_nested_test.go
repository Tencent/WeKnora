package core

import (
	"context"
	"strings"
	"testing"
)

func textBlk(id, text string, children ...string) DocxBlock {
	return DocxBlock{BlockID: id, BlockType: BlockTypeText, Text: txt(text), Children: children}
}

// A table cell is a container: list or callout blocks nested in it are
// consumed by tableDescendants, so the cell must render their text.
func TestNativeTablePreservesNestedCellTextOnce(t *testing.T) {
	blocks := []DocxBlock{
		tableBlk("table", 1, "cell"),
		{BlockID: "cell", BlockType: BlockTypeTableCell, Children: []string{"callout", "after"}},
		{BlockID: "callout", BlockType: BlockTypeCallout, Children: []string{"item"}},
		{BlockID: "item", BlockType: BlockTypeBullet, Bullet: txt("初始化参数"), Children: []string{"nested"}},
		textBlk("nested", "appKey 必填"),
		textBlk("after", "最后启动"),
	}
	md, _, _, _, err := blocksToMarkdown(context.Background(), nil, blocks, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(md), "| 初始化参数 appKey 必填 最后启动 |") {
		t.Fatalf("nested cell text missing or reordered:\n%s", md)
	}
	for _, text := range []string{"初始化参数", "appKey 必填", "最后启动"} {
		if strings.Count(string(md), text) != 1 {
			t.Fatalf("cell text %q duplicated:\n%s", text, md)
		}
	}
}

func TestNativeTableNestedCellTextHandlesRepeatedAndCyclicReferences(t *testing.T) {
	blocks := []DocxBlock{
		tableBlk("table", 1, "cell"),
		{BlockID: "cell", BlockType: BlockTypeTableCell, Children: []string{"container", "leaf", "missing"}},
		{BlockID: "container", BlockType: BlockTypeCallout, Children: []string{"leaf", "cell", "container"}},
		textBlk("leaf", "保留一次"),
	}
	md, _, _, _, err := blocksToMarkdown(context.Background(), nil, blocks, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(md), "保留一次") != 1 {
		t.Fatalf("repeated or cyclic reference duplicated text:\n%s", md)
	}
}

// Media keeps its own handling in the flat loop, and so does text nested
// under it; the cell must not render it a second time.
func TestNativeTableNestedCellTextSkipsMediaSubtrees(t *testing.T) {
	for _, kind := range []int{BlockTypeFile, BlockTypeImage, BlockTypeBoard} {
		blocks := []DocxBlock{
			tableBlk("table", 1, "cell"),
			{BlockID: "cell", BlockType: BlockTypeTableCell, Children: []string{"media"}},
			{BlockID: "media", BlockType: kind, Children: []string{"caption"}},
			textBlk("caption", "媒体说明"),
		}
		md, _, _, _, err := blocksToMarkdown(context.Background(), nil, blocks, "", nil)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(md), "| 媒体说明 |") {
			t.Fatalf("media subtree rendered inside the cell for block type %d:\n%s", kind, md)
		}
	}
}
