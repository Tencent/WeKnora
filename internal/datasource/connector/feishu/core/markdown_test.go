package core

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
)

// txt builds a text-bearing BlockText from plain content.
func txt(s string) *BlockText {
	return &BlockText{Elements: []TextElement{{TextRun: &TextRun{Content: s}}}}
}

type fakeReader struct {
	sheet            [][]string
	sheetTruncated   bool
	sheetErr         error
	bitable          [][]string
	bitableTruncated bool
	bitableErr       error
	sheetExhausted   bool
	bitableExhausted bool
	sheetBudgets     *[]int
	bitableBudgets   *[]int
}

func (f fakeReader) readSheetRange(_ context.Context, _ string, maxBytes int) ([][]string, bool, bool, error) {
	if f.sheetBudgets != nil {
		*f.sheetBudgets = append(*f.sheetBudgets, maxBytes)
	}
	return f.sheet, f.sheetTruncated, f.sheetExhausted, f.sheetErr
}

func (f fakeReader) readBitableRecords(
	_ context.Context, _ string, maxBytes int,
) ([][]string, bool, bool, error) {
	if f.bitableBudgets != nil {
		*f.bitableBudgets = append(*f.bitableBudgets, maxBytes)
	}
	return f.bitable, f.bitableTruncated, f.bitableExhausted, f.bitableErr
}

func TestBlocksToMarkdown_EmbeddedSheetAndFile(t *testing.T) {
	blocks := []DocxBlock{
		{BlockID: "root", BlockType: BlockTypePage},
		{BlockID: "s", BlockType: BlockTypeSheet, Sheet: &BlockTokenRef{Token: "sht_a_0"}},
		{BlockID: "img", BlockType: BlockTypeImage, Image: &BlockTokenRef{Token: "img_t"}},
		{BlockID: "f", BlockType: BlockTypeFile, File: &BlockFileRef{Token: "file_t", Name: "报表.pdf"}},
	}
	fr := fakeReader{sheet: [][]string{{"名称", "数量"}, {"苹果", "3"}}}
	md, atts, _, err := blocksToMarkdown(context.Background(), fr, blocks)
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

func TestBlocksToMarkdown_SheetReadBoundaryNote(t *testing.T) {
	blocks := []DocxBlock{
		{BlockID: "root", BlockType: BlockTypePage},
		{BlockID: "s", BlockType: BlockTypeSheet, Sheet: &BlockTokenRef{Token: "sht_a_0"}},
	}
	fr := fakeReader{sheet: [][]string{{"h"}, {"1"}}, sheetTruncated: true}
	md, _, _, err := blocksToMarkdown(context.Background(), fr, blocks)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !strings.Contains(string(md), "未完整读取") || strings.Contains(string(md), "500") {
		t.Errorf("want an incomplete-read note without an incorrect row count, got:\n%s", md)
	}
}

func TestBlocksToMarkdown_SheetPermissionDegrades(t *testing.T) {
	blocks := []DocxBlock{
		{BlockID: "root", BlockType: BlockTypePage},
		{BlockID: "s", BlockType: BlockTypeSheet, Sheet: &BlockTokenRef{Token: "sht_a_0"}},
	}
	fr := fakeReader{sheetErr: fmt.Errorf("code=99991672 permission denied")}
	md, _, _, err := blocksToMarkdown(context.Background(), fr, blocks)
	if err != nil {
		t.Fatalf("should not fail on permission error, got %v", err)
	}
	if !strings.Contains(string(md), "无法读取内嵌电子表格") {
		t.Errorf("want degraded placeholder, got:\n%s", md)
	}
}

func TestBlocksToMarkdown_BitableInlinedAndDegrades(t *testing.T) {
	mk := func(fr fakeReader) string {
		blocks := []DocxBlock{
			{BlockID: "root", BlockType: BlockTypePage},
			{BlockID: "bt", BlockType: BlockTypeBitable, Bitable: &BlockTokenRef{Token: "bascabc_tblxyz"}},
		}
		md, _, _, err := blocksToMarkdown(context.Background(), fr, blocks)
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
	// text from those children AND must not also Emit them as loose paragraphs.
	blocks := []DocxBlock{
		{BlockID: "root", BlockType: BlockTypePage},
		tableBlk("t", 2, "c1", "c2", "c3", "c4"),
		cellBlk("c1"), cellBlk("c2"), cellBlk("c3"), cellBlk("c4"),
		cellTextBlk("c1", "姓名"), cellTextBlk("c2", "分数"),
		cellTextBlk("c3", "张三"), cellTextBlk("c4", "95"),
	}
	md, _, _, err := blocksToMarkdown(context.Background(), nil, blocks)
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
	blocks := []DocxBlock{
		{BlockID: "root", BlockType: BlockTypePage},
		tableBlk("t", 1, "c1"),
		{BlockID: "c1", BlockType: BlockTypeTableCell, Children: []string{"f1"}},
		{BlockID: "f1", BlockType: BlockTypeFile, File: &BlockFileRef{Token: "tok-in-cell", Name: "内嵌.pdf"}},
	}
	_, atts, _, err := blocksToMarkdown(context.Background(), nil, blocks)
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
	// passes a naive empty guard, but cols==0 would Emit a malformed GFM table
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
	blocks := []DocxBlock{
		{BlockID: "root", BlockType: BlockTypePage},
		// tableBlk sets Property (renderable); here we want an UNrenderable one.
		{BlockID: "t", BlockType: BlockTypeTable, Table: &BlockTable{Cells: []string{"c1"}}},
		{BlockID: "c1", BlockType: BlockTypeTableCell, Children: []string{"c1_txt"}},
		cellTextBlk("c1", "重要内容"),
	}
	md, _, _, err := blocksToMarkdown(context.Background(), nil, blocks)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !strings.Contains(string(md), "重要内容") {
		t.Errorf("cell text of an unrenderable table was dropped:\n%s", md)
	}
}

func TestBlocksToMarkdown_TextConstructs(t *testing.T) {
	blocks := []DocxBlock{
		{BlockID: "root", BlockType: BlockTypePage, Children: []string{"h", "p", "b"}},
		{BlockID: "h", BlockType: BlockTypeHeading1, Heading1: txt("标题")},
		{BlockID: "p", BlockType: BlockTypeText, Text: txt("一段正文")},
		{BlockID: "b", BlockType: BlockTypeBullet, Bullet: txt("要点")},
	}
	md, atts, _, err := blocksToMarkdown(context.Background(), nil, blocks)
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
	// must render to empty Markdown. FetchDocxWithBlocks relies on this: an empty
	// render triggers the export fallback instead of emitting a content-less main
	// item that would wrongly ingest the login-gated wiki URL. Any File/Image
	// block writes a placeholder, so a truly empty render also implies no
	// attachment/image downdrill is lost by that fallback.
	blocks := []DocxBlock{
		{BlockID: "root", BlockType: BlockTypePage, Children: []string{"p"}},
		{BlockID: "p", BlockType: BlockTypeText, Text: txt("")},
	}
	md, atts, _, err := blocksToMarkdown(context.Background(), nil, blocks)
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
	blocks := []DocxBlock{
		{BlockID: "root", BlockType: BlockTypePage},
		{BlockID: "t", BlockType: BlockTypeTodo, Todo: txt("买牛奶")},
		{BlockID: "c", BlockType: BlockTypeCallout, Callout: txt("注意事项")},
	}
	md, _, _, err := blocksToMarkdown(context.Background(), nil, blocks)
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
	// A callout with no direct text (container form) must Emit nothing itself.
	blocks := []DocxBlock{
		{BlockID: "root", BlockType: BlockTypePage},
		{BlockID: "c", BlockType: BlockTypeCallout},
	}
	md, _, _, err := blocksToMarkdown(context.Background(), nil, blocks)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if strings.TrimSpace(string(md)) != "" {
		t.Errorf("empty callout should Emit nothing, got:\n%q", md)
	}
}

func TestInlineTable_TruncationBeforeFirstRowIsVisible(t *testing.T) {
	for _, kind := range []string{"sheet", "bitable"} {
		reader := fakeReader{sheetTruncated: true, bitableTruncated: true}
		got, exhausted, _ := inlineTable(context.Background(), reader, "token", kind, maxTableBytes)
		if exhausted {
			t.Fatal("full table budget was reported as exhausted")
		}
		if !strings.Contains(got, "未完整读取") || strings.Contains(got, "500") {
			t.Fatalf("lost incomplete-table note: %q", got)
		}
	}
}

func TestBlocksToMarkdown_DocumentBudgetStopsFurtherTableReads(t *testing.T) {
	const budget = 256
	var sheetBudgets, bitableBudgets []int
	reader := fakeReader{
		sheet:          [][]string{{"header"}, {"kept row"}},
		bitable:        [][]string{{"header"}, {"must not be fetched"}},
		sheetBudgets:   &sheetBudgets,
		bitableBudgets: &bitableBudgets,
	}
	blocks := []DocxBlock{
		{BlockID: "root", BlockType: BlockTypePage},
		{BlockID: "before", BlockType: BlockTypeText, Text: txt("kept prefix")},
		{BlockID: "sheet", BlockType: BlockTypeSheet, Sheet: &BlockTokenRef{Token: "sht_a_0"}},
		{BlockID: "large", BlockType: BlockTypeText, Text: txt(strings.Repeat("x", budget))},
		{BlockID: "bitable", BlockType: BlockTypeBitable, Bitable: &BlockTokenRef{Token: "app_table"}},
		{BlockID: "file", BlockType: BlockTypeFile, File: &BlockFileRef{Token: "file_t", Name: "report.pdf"}},
	}

	md, atts, _, err := blocksToMarkdownWithin(context.Background(), reader, blocks, budget)
	if err != nil {
		t.Fatalf("blocksToMarkdownWithin: %v", err)
	}
	if len(md) > budget {
		t.Fatalf("document length = %d, want <= %d", len(md), budget)
	}
	if !strings.Contains(string(md), "kept prefix") || !strings.Contains(string(md), documentTruncatedNote) {
		t.Fatalf("document must preserve the prefix and mark truncation:\n%s", md)
	}
	if strings.Contains(string(md), "must not be fetched") || len(bitableBudgets) != 0 {
		t.Fatalf("later embedded tables must not be fetched after exhaustion: budgets=%v\n%s", bitableBudgets, md)
	}
	if len(sheetBudgets) != 1 || sheetBudgets[0] >= budget {
		t.Fatalf("sheet budget = %v, want one remaining-document budget below %d", sheetBudgets, budget)
	}
	if len(atts) != 1 || atts[0].FileToken != "file_t" {
		t.Fatalf("attachments after the Markdown boundary must still be collected: %+v", atts)
	}
}

func TestBlocksToMarkdown_DocumentBudgetBoundary(t *testing.T) {
	const budget = 256
	contentLimit := budget - len(documentTruncatedNote) - 2
	for _, delta := range []int{0, 1} {
		t.Run(fmt.Sprintf("over-by-%d", delta), func(t *testing.T) {
			textBytes := contentLimit - 2 + delta
			blocks := []DocxBlock{
				{BlockID: "root", BlockType: BlockTypePage},
				{BlockID: "text", BlockType: BlockTypeText, Text: txt(strings.Repeat("x", textBytes))},
			}

			md, _, _, err := blocksToMarkdownWithin(context.Background(), nil, blocks, budget)
			if err != nil {
				t.Fatalf("blocksToMarkdownWithin: %v", err)
			}
			if len(md) > budget {
				t.Fatalf("document length = %d, want <= %d", len(md), budget)
			}
			truncated := strings.Contains(string(md), documentTruncatedNote)
			if truncated != (delta == 1) {
				t.Fatalf("truncated=%v for delta=%d, output=%q", truncated, delta, md)
			}
		})
	}
}

func TestBlocksToMarkdown_EmbeddedTablesShareDocumentBudget(t *testing.T) {
	const budget = 512
	var sheetBudgets, bitableBudgets []int
	reader := fakeReader{
		sheet:          [][]string{{"sheet"}, {"first"}},
		bitable:        [][]string{{"bitable"}, {"second"}},
		sheetBudgets:   &sheetBudgets,
		bitableBudgets: &bitableBudgets,
	}
	blocks := []DocxBlock{
		{BlockID: "root", BlockType: BlockTypePage},
		{BlockID: "sheet", BlockType: BlockTypeSheet, Sheet: &BlockTokenRef{Token: "sht_0"}},
		{BlockID: "bitable", BlockType: BlockTypeBitable, Bitable: &BlockTokenRef{Token: "app_table"}},
	}

	md, _, _, err := blocksToMarkdownWithin(context.Background(), reader, blocks, budget)
	if err != nil {
		t.Fatalf("blocksToMarkdownWithin: %v", err)
	}
	if len(sheetBudgets) != 1 || len(bitableBudgets) != 1 {
		t.Fatalf("reader budgets: sheet=%v bitable=%v", sheetBudgets, bitableBudgets)
	}
	if bitableBudgets[0] >= sheetBudgets[0] {
		t.Fatalf("second table budget = %d, want less than first table budget %d",
			bitableBudgets[0], sheetBudgets[0])
	}
	if !strings.Contains(string(md), "first") || !strings.Contains(string(md), "second") || len(md) > budget {
		t.Fatalf("tables were not rendered within their shared document budget:\n%s", md)
	}
}

func TestBlocksToMarkdown_NoRoomForTableNoteMarksDocumentTruncated(t *testing.T) {
	const budget = 256
	tableNote := fmt.Sprintf("> [%s未完整读取，已保留可读取内容]", "内嵌电子表格")
	contentLimit := budget - len(documentTruncatedNote) - 2
	prefixBytes := contentLimit - 4 - (len(tableNote) - 1)
	var sheetBudgets []int
	reader := fakeReader{sheetBudgets: &sheetBudgets}
	blocks := []DocxBlock{
		{BlockID: "root", BlockType: BlockTypePage},
		{BlockID: "prefix", BlockType: BlockTypeText, Text: txt(strings.Repeat("x", prefixBytes))},
		{BlockID: "sheet", BlockType: BlockTypeSheet, Sheet: &BlockTokenRef{Token: "sht_0"}},
	}

	md, _, _, err := blocksToMarkdownWithin(context.Background(), reader, blocks, budget)
	if err != nil {
		t.Fatalf("blocksToMarkdownWithin: %v", err)
	}
	if len(sheetBudgets) != 0 {
		t.Fatalf("table reader was called without room for its status note: %v", sheetBudgets)
	}
	if len(md) > budget || !strings.Contains(string(md), documentTruncatedNote) {
		t.Fatalf("document did not expose budget exhaustion: bytes=%d output=%q", len(md), md)
	}
}

func TestBlocksToMarkdown_TableBudgetExhaustionMarksDocumentTruncated(t *testing.T) {
	reader := fakeReader{
		sheet:          [][]string{{"header"}, {"kept row"}},
		sheetTruncated: true,
		sheetExhausted: true,
	}
	blocks := []DocxBlock{
		{BlockID: "root", BlockType: BlockTypePage},
		{BlockID: "sheet", BlockType: BlockTypeSheet, Sheet: &BlockTokenRef{Token: "sht_0"}},
		{BlockID: "later", BlockType: BlockTypeText, Text: txt("must not render")},
	}

	md, _, _, err := blocksToMarkdownWithin(context.Background(), reader, blocks, 512)
	if err != nil {
		t.Fatalf("blocksToMarkdownWithin: %v", err)
	}
	if !strings.Contains(string(md), "kept row") || !strings.Contains(string(md), documentTruncatedNote) {
		t.Fatalf("budget-limited table must retain rows and mark the document incomplete:\n%s", md)
	}
	if strings.Contains(string(md), "must not render") {
		t.Fatalf("blocks after document exhaustion must not render:\n%s", md)
	}
}

func TestBlocksToMarkdown_DocumentBudgetsAreIndependent(t *testing.T) {
	const (
		budget    = 256
		documents = 16
	)
	blocks := []DocxBlock{
		{BlockID: "root", BlockType: BlockTypePage},
		{BlockID: "text", BlockType: BlockTypeText, Text: txt(strings.Repeat("x", budget))},
	}
	results := make([][]byte, documents)
	errs := make([]error, documents)
	var wg sync.WaitGroup
	for i := range documents {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i], _, _, errs[i] = blocksToMarkdownWithin(context.Background(), nil, blocks, budget)
		}()
	}
	wg.Wait()

	for i := range documents {
		if errs[i] != nil {
			t.Fatalf("document %d: %v", i, errs[i])
		}
		if len(results[i]) > budget || !strings.Contains(string(results[i]), documentTruncatedNote) {
			t.Fatalf("document %d crossed or lost its independent budget: bytes=%d output=%q",
				i, len(results[i]), results[i])
		}
	}
}

func TestBlocksToMarkdown_TableLimitDoesNotDiscardFollowingContent(t *testing.T) {
	reader := fakeReader{
		sheet: [][]string{{"header"}, {"retained"}}, sheetTruncated: true, sheetExhausted: true,
		bitable: [][]string{{"header"}, {"later record"}},
	}
	blocks := []DocxBlock{
		{BlockID: "sheet", BlockType: BlockTypeSheet, Sheet: &BlockTokenRef{Token: "sht_0"}},
		{BlockID: "bitable", BlockType: BlockTypeBitable, Bitable: &BlockTokenRef{Token: "app_table"}},
		{BlockID: "tail", BlockType: BlockTypeText, Text: txt("document tail")},
	}
	md, _, _, err := blocksToMarkdownWithin(context.Background(), reader, blocks, 2*maxTableBytes)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"retained", "未完整读取", "later record", "document tail"} {
		if !strings.Contains(string(md), want) {
			t.Errorf("missing %q after single-table truncation", want)
		}
	}
	if strings.Contains(string(md), documentTruncatedNote) {
		t.Error("single table exhaustion incorrectly marked the document exhausted")
	}
}
