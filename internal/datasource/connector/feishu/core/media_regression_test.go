package core

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"net/http"
	"strings"
	"testing"
)

func TestTableViewKeepsFileAndBoard(t *testing.T) {
	for _, kind := range []int{BlockTypeView, BlockTypeCallout, BlockTypeQuoteContainer, BlockTypeGrid} {
		t.Run(blockNameForTest(kind), func(t *testing.T) {
			blocks := []DocxBlock{
				{
					BlockID: "table", BlockType: BlockTypeTable, Children: []string{"cell"},
					Table: &BlockTable{Cells: []string{"cell"}, Property: &BlockTableProperty{ColumnSize: 1}},
				},
				{BlockID: "cell", BlockType: BlockTypeTableCell, Children: []string{"view"}},
				{BlockID: "view", BlockType: kind, Children: []string{"file", "board"}},
				{BlockID: "file", BlockType: BlockTypeFile, File: &BlockFileRef{Token: "pdf", Name: "report.pdf"}},
				{BlockID: "board", BlockType: BlockTypeBoard, Board: &BlockBoard{Token: "brd-1"}},
			}
			for _, outer := range []bool{false, true} {
				if outer {
					blocks = append([]DocxBlock{
						{BlockID: "outer", BlockType: BlockTypeView, Children: []string{"table"}},
					}, blocks...)
				}
				md, atts, imgs, _, err := blocksToMarkdown(context.Background(), nil, blocks, "", nil)
				if err != nil {
					t.Fatal(err)
				}
				if len(atts) != 1 || len(imgs) != 1 {
					t.Fatalf("table view media lost: attachments=%d images=%d markdown=%q", len(atts), len(imgs), md)
				}
				if strings.Count(string(md), "weknora-att://pdf") != 1 ||
					strings.Count(string(md), "weknora-img://") != 1 {
					t.Fatalf("media must render exactly once: %s", md)
				}
			}
		})
	}
}

func TestTrimmedBoardKeepsMeaningfulDiagram(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 1600, 1200))
	draw.Draw(img, img.Bounds(), image.NewUniform(color.White), image.Point{}, draw.Src)
	draw.Draw(img, image.Rect(400, 400, 700, 600), image.NewUniform(color.Black), image.Point{}, draw.Src)
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	cropped := trimBoardImage(b.Bytes())
	cfg, _, err := image.DecodeConfig(bytes.NewReader(cropped))
	if err != nil {
		t.Fatal(err)
	}
	if len(cropped) >= MinAttachmentBytes || cfg.Width < 64 || cfg.Height < 64 {
		t.Fatalf("fixture must be a sub-2KB diagram with meaningful dimensions: bytes=%d dims=%dx%d",
			len(cropped), cfg.Width, cfg.Height)
	}
	server := fakeFeishuForBoard(t, boardBlocks(), http.StatusOK, b.Bytes())
	client := NewClient(&Config{AppID: "a", AppSecret: "b", BaseURL: server.URL})
	items, err := FetchDocxWithBlocks(context.Background(), client, boardFetchInput())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(items[len(items)-1].Content), "data:image/png;base64,") {
		t.Fatalf("valid board disappeared: %s", items[len(items)-1].Content)
	}
}
