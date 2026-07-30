package feishu

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

// txt builds a text-bearing blockText from plain content.
func txt(s string) *blockText {
	return &blockText{Elements: []textElement{{TextRun: &textRun{Content: s}}}}
}

type fakeReader struct {
	sheet            [][]string
	sheetTruncated   bool
	sheetErr         error
	bitable          [][]string
	bitableTruncated bool
	bitableErr       error
}

func (f fakeReader) ReadSheetRange(_ context.Context, _ string) ([][]string, bool, error) {
	return f.sheet, f.sheetTruncated, f.sheetErr
}

func (f fakeReader) ReadBitableRecords(_ context.Context, _ string) ([][]string, bool, error) {
	return f.bitable, f.bitableTruncated, f.bitableErr
}

func TestBlocksToMarkdown_EmbeddedSheetAndFile(t *testing.T) {
	blocks := []docxBlock{
		{BlockID: "root", BlockType: blockTypePage},
		{BlockID: "s", BlockType: blockTypeSheet, Sheet: &blockTokenRef{Token: "sht_a_0"}},
		{BlockID: "img", BlockType: blockTypeImage, Image: &blockTokenRef{Token: "img_t"}},
		{BlockID: "f", BlockType: blockTypeFile, File: &blockFileRef{Token: "file_t", Name: "报表.pdf"}},
	}
	fr := fakeReader{sheet: [][]string{{"名称", "数量"}, {"苹果", "3"}}}
	md, atts, err := blocksToMarkdown(context.Background(), fr, blocks)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !strings.Contains(string(md), "| 名称 | 数量 |") || !strings.Contains(string(md), "| 苹果 | 3 |") {
		t.Errorf("sheet not inlined:\n%s", md)
	}
	if !strings.Contains(string(md), "![图片]()") {
		t.Errorf("image placeholder missing:\n%s", md)
	}
	if strings.Contains(string(md), "feishu-media") {
		t.Errorf("internal media token leaked into markdown:\n%s", md)
	}
	if len(atts) != 1 || atts[0].FileToken != "file_t" || atts[0].Name != "报表.pdf" {
		t.Errorf("attachments = %+v", atts)
	}
	if !strings.Contains(string(md), "📎 附件：报表.pdf") {
		t.Errorf("attachment inline reference missing:\n%s", md)
	}
}

func TestBlocksToMarkdown_SheetTruncatedNote(t *testing.T) {
	blocks := []docxBlock{
		{BlockID: "root", BlockType: blockTypePage},
		{BlockID: "s", BlockType: blockTypeSheet, Sheet: &blockTokenRef{Token: "sht_a_0"}},
	}
	fr := fakeReader{sheet: [][]string{{"h"}, {"1"}}, sheetTruncated: true}
	md, _, err := blocksToMarkdown(context.Background(), fr, blocks)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !strings.Contains(string(md), "表格已截断") {
		t.Errorf("want truncation note, got:\n%s", md)
	}
}

func TestBlocksToMarkdown_SheetPermissionDegrades(t *testing.T) {
	blocks := []docxBlock{
		{BlockID: "root", BlockType: blockTypePage},
		{BlockID: "s", BlockType: blockTypeSheet, Sheet: &blockTokenRef{Token: "sht_a_0"}},
	}
	fr := fakeReader{sheetErr: fmt.Errorf("code=99991672 permission denied")}
	md, _, err := blocksToMarkdown(context.Background(), fr, blocks)
	if err != nil {
		t.Fatalf("should not fail on permission error, got %v", err)
	}
	if !strings.Contains(string(md), "无法读取内嵌电子表格") {
		t.Errorf("want degraded placeholder, got:\n%s", md)
	}
}

func TestBlocksToMarkdown_BitableInlinedAndDegrades(t *testing.T) {
	mk := func(fr fakeReader) string {
		blocks := []docxBlock{
			{BlockID: "root", BlockType: blockTypePage},
			{BlockID: "bt", BlockType: blockTypeBitable, Bitable: &blockTokenRef{Token: "bascabc_tblxyz"}},
		}
		md, _, err := blocksToMarkdown(context.Background(), fr, blocks)
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		return string(md)
	}

	inlined := mk(fakeReader{bitable: [][]string{{"任务", "状态"}, {"写文档", "进行中"}}})
	if !strings.Contains(inlined, "| 任务 | 状态 |") || !strings.Contains(inlined, "| 写文档 | 进行中 |") {
		t.Errorf("bitable not inlined:\n%s", inlined)
	}

	degraded := mk(fakeReader{bitableErr: fmt.Errorf("code=99991672 permission denied")})
	if !strings.Contains(degraded, "无法读取内嵌多维表格") {
		t.Errorf("want bitable degradation note, got:\n%s", degraded)
	}
}

func TestBlocksToMarkdown_NativeTableFromCellChildren(t *testing.T) {
	// Real Feishu shape: a table_cell (block_type 32) is a container whose text
	// lives in child text blocks, not on the cell. The renderer must read cell
	// text from those children AND must not also emit them as loose paragraphs.
	blocks := []docxBlock{
		{BlockID: "root", BlockType: blockTypePage},
		tableBlk("t", 2, "c1", "c2", "c3", "c4"),
		cellBlk("c1"), cellBlk("c2"), cellBlk("c3"), cellBlk("c4"),
		cellTextBlk("c1", "姓名"), cellTextBlk("c2", "分数"),
		cellTextBlk("c3", "张三"), cellTextBlk("c4", "95"),
	}
	md, _, err := blocksToMarkdown(context.Background(), nil, blocks)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	s := string(md)
	if !strings.Contains(s, "| 姓名 | 分数 |") || !strings.Contains(s, "| 张三 | 95 |") {
		t.Errorf("native table not rendered from cell children:\n%s", s)
	}
	// Cell text must appear ONLY inside the table, never as a stray paragraph.
	if n := strings.Count(s, "姓名"); n != 1 {
		t.Errorf("cell text leaked outside the table (count=%d, want 1):\n%s", n, s)
	}
}

func TestBlocksToMarkdown_AttachmentInsideTableCellStillCollected(t *testing.T) {
	// A file block nested inside a table cell must still be collected as an
	// attachment — the table "consumed" set marks a cell's text structure but
	// must NOT swallow attachment/media blocks, or embedded files silently vanish.
	blocks := []docxBlock{
		{BlockID: "root", BlockType: blockTypePage},
		tableBlk("t", 1, "c1"),
		{BlockID: "c1", BlockType: blockTypeTableCell, Children: []string{"f1"}},
		{BlockID: "f1", BlockType: blockTypeFile, File: &blockFileRef{Token: "tok-in-cell", Name: "内嵌.pdf"}},
	}
	_, atts, err := blocksToMarkdown(context.Background(), nil, blocks)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(atts) != 1 || atts[0].FileToken != "tok-in-cell" {
		t.Fatalf("attachment nested in a table cell was not collected: %+v", atts)
	}
}

func TestMarkdownTable_RaggedRowClampedToHeader(t *testing.T) {
	// Header declares 2 columns; a data row with 4 cells (trailing populated
	// cells beyond the header range) must be clamped to 2, or the emitted table
	// has more body cells than header/separator columns → malformed GFM.
	rows := [][]string{
		{"名称", "数量"},
		{"苹果", "3", "多余1", "多余2"},
	}
	out := markdownTable(rows)
	lines := strings.Split(out, "\n")
	if len(lines) != 3 {
		t.Fatalf("want 3 lines (header, separator, 1 row), got %d:\n%s", len(lines), out)
	}
	// Every row must have the same pipe count as the 2-column header.
	want := strings.Count(lines[0], "|")
	for i, ln := range lines {
		if got := strings.Count(ln, "|"); got != want {
			t.Errorf("line %d %q has %d pipes, want %d (ragged row not clamped)", i, ln, got, want)
		}
	}
	if strings.Contains(out, "多余") {
		t.Errorf("overflow cells must be dropped, got:\n%s", out)
	}
}

func TestMarkdownTable_ZeroColumnRendersNothing(t *testing.T) {
	// An embedded sheet/bitable with a header row but no columns (e.g. a bitable
	// whose fields were all deleted) yields rows == [][]string{{}}. len(rows)==1
	// passes a naive empty guard, but cols==0 would emit a malformed GFM table
	// ("|  |" header + a bare "|" separator). It must render nothing instead.
	for _, rows := range [][][]string{
		{{}},         // one empty header row, no data
		{{}, {}, {}}, // empty header + empty data rows
	} {
		out := markdownTable(rows)
		if out != "" {
			t.Errorf("zero-column table must render nothing, got %q for rows=%+v", out, rows)
		}
	}
}

func TestBlocksToMarkdown_UnrenderableTablePreservesCellText(t *testing.T) {
	// A native table block with no column property (Table!=nil, Property==nil)
	// cannot be rendered as a Markdown table. Its cells must NOT be consumed, so
	// their text still reaches the output as loose paragraphs rather than
	// vanishing entirely.
	blocks := []docxBlock{
		{BlockID: "root", BlockType: blockTypePage},
		// tableBlk sets Property (renderable); here we want an UNrenderable one.
		{BlockID: "t", BlockType: blockTypeTable, Table: &blockTable{Cells: []string{"c1"}}},
		{BlockID: "c1", BlockType: blockTypeTableCell, Children: []string{"c1_txt"}},
		cellTextBlk("c1", "重要内容"),
	}
	md, _, err := blocksToMarkdown(context.Background(), nil, blocks)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !strings.Contains(string(md), "重要内容") {
		t.Errorf("cell text of an unrenderable table was dropped:\n%s", md)
	}
}

func TestBlocksToMarkdown_TextConstructs(t *testing.T) {
	blocks := []docxBlock{
		{BlockID: "root", BlockType: blockTypePage, Children: []string{"h", "p", "b"}},
		{BlockID: "h", BlockType: blockTypeHeading1, Heading1: txt("标题")},
		{BlockID: "p", BlockType: blockTypeText, Text: txt("一段正文")},
		{BlockID: "b", BlockType: blockTypeBullet, Bullet: txt("要点")},
	}
	md, atts, err := blocksToMarkdown(context.Background(), nil, blocks)
	if err != nil {
		t.Fatalf("blocksToMarkdown: %v", err)
	}
	if len(atts) != 0 {
		t.Errorf("want 0 attachments, got %d", len(atts))
	}
	want := "# 标题\n\n一段正文\n\n- 要点\n"
	if string(md) != want {
		t.Errorf("md =\n%q\nwant\n%q", md, want)
	}
}

func TestBlocksToMarkdown_BlankDocRendersEmpty(t *testing.T) {
	// A page with no renderable children (or only empty-text/container blocks)
	// must render to empty Markdown. fetchDocxWithBlocks relies on this: an empty
	// render triggers the export fallback instead of emitting a content-less main
	// item that would wrongly ingest the login-gated wiki URL. Any File/Image
	// block writes a placeholder, so a truly empty render also implies no
	// attachment/image downdrill is lost by that fallback.
	blocks := []docxBlock{
		{BlockID: "root", BlockType: blockTypePage, Children: []string{"p"}},
		{BlockID: "p", BlockType: blockTypeText, Text: txt("")},
	}
	md, atts, err := blocksToMarkdown(context.Background(), nil, blocks)
	if err != nil {
		t.Fatalf("blocksToMarkdown: %v", err)
	}
	if strings.TrimSpace(string(md)) != "" {
		t.Errorf("blank doc should render empty Markdown, got:\n%q", md)
	}
	if len(atts) != 0 {
		t.Errorf("blank doc should collect no attachments, got %d", len(atts))
	}
}

func TestBlocksToMarkdown_TodoAndCallout(t *testing.T) {
	blocks := []docxBlock{
		{BlockID: "root", BlockType: blockTypePage},
		{BlockID: "t", BlockType: blockTypeTodo, Todo: txt("买牛奶")},
		{BlockID: "c", BlockType: blockTypeCallout, Callout: txt("注意事项")},
	}
	md, _, err := blocksToMarkdown(context.Background(), nil, blocks)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !strings.Contains(string(md), "- [ ] 买牛奶") {
		t.Errorf("todo not rendered:\n%s", md)
	}
	if !strings.Contains(string(md), "> 注意事项") {
		t.Errorf("callout direct text not rendered:\n%s", md)
	}
}

func TestBlocksToMarkdown_CalloutContainerNoOp(t *testing.T) {
	// A callout with no direct text (container form) must emit nothing itself.
	blocks := []docxBlock{
		{BlockID: "root", BlockType: blockTypePage},
		{BlockID: "c", BlockType: blockTypeCallout},
	}
	md, _, err := blocksToMarkdown(context.Background(), nil, blocks)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if strings.TrimSpace(string(md)) != "" {
		t.Errorf("empty callout should emit nothing, got:\n%q", md)
	}
}
