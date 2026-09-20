package core

import (
	"context"
	"fmt"
	"strconv"
	"strings"
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
	merges           []sheetMergeRange
	sheetMergesErr   error
	bitable          [][]string
	bitableTruncated bool
	bitableErr       error
}

func (f fakeReader) readSheetRange(_ context.Context, _ string) ([][]string, bool, error) {
	return f.sheet, f.sheetTruncated, f.sheetErr
}

func (f fakeReader) readBitableRecords(_ context.Context, _ string) ([][]string, bool, error) {
	return f.bitable, f.bitableTruncated, f.bitableErr
}

func (f fakeReader) sheetMerges(_ context.Context, _ string) ([]sheetMergeRange, error) {
	return f.merges, f.sheetMergesErr
}

func TestBlocksToMarkdown_EmbeddedSheetAndFile(t *testing.T) {
	blocks := []DocxBlock{
		{BlockID: "root", BlockType: BlockTypePage},
		{BlockID: "s", BlockType: BlockTypeSheet, Sheet: &BlockTokenRef{Token: "sht_a_0"}},
		{BlockID: "img", BlockType: BlockTypeImage, Image: &BlockTokenRef{Token: "img_t"}},
		{BlockID: "f", BlockType: BlockTypeFile, File: &BlockFileRef{Token: "file_t", Name: "报表.pdf"}},
	}
	fr := fakeReader{sheet: [][]string{{"名称", "数量"}, {"苹果", "3"}}}
	md, atts, imgs, err := blocksToMarkdown(context.Background(), fr, blocks, "")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !strings.Contains(string(md), "| 名称 | 数量 |") || !strings.Contains(string(md), "| 苹果 | 3 |") {
		t.Errorf("sheet not inlined:\n%s", md)
	}
	if !strings.Contains(string(md), "![图片](weknora-img://1)") {
		t.Errorf("numbered image marker missing:\n%s", md)
	}
	if len(imgs) != 1 || imgs[0].N != 1 || imgs[0].Kind != "image" || imgs[0].Token != "img_t" {
		t.Errorf("pendingImages = %+v", imgs)
	}
	if strings.Contains(string(md), "feishu-media") {
		t.Errorf("internal media token leaked into markdown:\n%s", md)
	}
	if len(atts) != 1 || atts[0].FileToken != "file_t" || atts[0].Name != "报表.pdf" {
		t.Errorf("attachments = %+v", atts)
	}
	if !strings.Contains(string(md), "- 报表.pdf") {
		t.Errorf("attachment file-name list missing:\n%s", md)
	}
}

func TestBlocksToMarkdown_SheetMergesFilled(t *testing.T) {
	blocks := []DocxBlock{
		{BlockID: "root", BlockType: BlockTypePage},
		{BlockID: "s", BlockType: BlockTypeSheet, Sheet: &BlockTokenRef{Token: "sht_a_0"}},
	}
	// 3x3 grid: B1:rule merged region spanning rows 0-1 col 1, and the whole
	// bottom row merged across cols 0-2. Indices are 0-based CLOSED.
	sheet := [][]string{
		{"名称", "分类", "备注"},
		{"苹果", "水果", ""},
		{"香蕉", "", ""},
	}
	fr := fakeReader{
		sheet: sheet,
		merges: []sheetMergeRange{
			{StartRow: 0, EndRow: 1, StartCol: 1, EndCol: 1}, // 分类 covers rows 0-1
			{StartRow: 2, EndRow: 2, StartCol: 0, EndCol: 2}, // bottom row merged
		},
	}
	md, _, _, err := blocksToMarkdown(context.Background(), fr, blocks, "")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	s := string(md)
	for _, want := range []string{
		"| 名称 | 分类 | 备注 |",
		// 分类 is the merged region's anchor and fills the covered cell below.
		"| 苹果 | 分类 |  |",
		// Bottom row: the anchor value fills the whole merged region.
		"| 香蕉 | 香蕉 | 香蕉 |",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("merged sheet table missing %q:\n%s", want, s)
		}
	}
}

func TestBlocksToMarkdown_SheetMergeFetchFailureDegrades(t *testing.T) {
	blocks := []DocxBlock{
		{BlockID: "root", BlockType: BlockTypePage},
		{BlockID: "s", BlockType: BlockTypeSheet, Sheet: &BlockTokenRef{Token: "sht_a_0"}},
	}
	fr := fakeReader{
		sheet:          [][]string{{"名称", "数量"}, {"苹果", "3"}},
		merges:         nil,
		sheetMergesErr: fmt.Errorf("permission denied"),
	}
	md, _, _, err := blocksToMarkdown(context.Background(), fr, blocks, "")
	if err != nil {
		t.Fatalf("merge fetch failure must not fail the document, got: %v", err)
	}
	if !strings.Contains(string(md), "| 苹果 | 3 |") {
		t.Errorf("raw values must survive merge fetch failure:\n%s", md)
	}
}

func TestBlocksToMarkdown_SheetTruncatedNote(t *testing.T) {
	blocks := []DocxBlock{
		{BlockID: "root", BlockType: BlockTypePage},
		{BlockID: "s", BlockType: BlockTypeSheet, Sheet: &BlockTokenRef{Token: "sht_a_0"}},
	}
	fr := fakeReader{sheet: [][]string{{"h"}, {"1"}}, sheetTruncated: true}
	md, _, _, err := blocksToMarkdown(context.Background(), fr, blocks, "")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !strings.Contains(string(md), "表格已截断") {
		t.Errorf("want truncation note, got:\n%s", md)
	}
}

func TestBlocksToMarkdown_SheetPermissionDegrades(t *testing.T) {
	blocks := []DocxBlock{
		{BlockID: "root", BlockType: BlockTypePage},
		{BlockID: "s", BlockType: BlockTypeSheet, Sheet: &BlockTokenRef{Token: "sht_a_0"}},
	}
	fr := fakeReader{sheetErr: fmt.Errorf("code=99991672 permission denied")}
	md, _, _, err := blocksToMarkdown(context.Background(), fr, blocks, "")
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
		md, _, _, err := blocksToMarkdown(context.Background(), fr, blocks, "")
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
	md, _, _, err := blocksToMarkdown(context.Background(), nil, blocks, "")
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
	_, atts, _, err := blocksToMarkdown(context.Background(), nil, blocks, "")
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
	md, _, _, err := blocksToMarkdown(context.Background(), nil, blocks, "")
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
	md, atts, _, err := blocksToMarkdown(context.Background(), nil, blocks, "")
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
	md, atts, _, err := blocksToMarkdown(context.Background(), nil, blocks, "")
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
	md, _, _, err := blocksToMarkdown(context.Background(), nil, blocks, "")
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
	md, _, _, err := blocksToMarkdown(context.Background(), nil, blocks, "")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if strings.TrimSpace(string(md)) != "" {
		t.Errorf("empty callout should Emit nothing, got:\n%q", md)
	}
}

func TestBlocksToMarkdown_CalloutQuotesChildren(t *testing.T) {
	// A callout container renders its child blocks recursively with "> "
	// prefixes — and must not also Emit them a second time as loose paragraphs.
	blocks := []DocxBlock{
		{BlockID: "root", BlockType: BlockTypePage},
		{BlockID: "c", BlockType: BlockTypeCallout, Children: []string{"h", "p"}},
		{BlockID: "h", BlockType: 4, Heading2: txt("注意")},
		{BlockID: "p", BlockType: BlockTypeText, Text: txt("正文内容")},
	}
	md, _, _, err := blocksToMarkdown(context.Background(), nil, blocks, "")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	s := string(md)
	if !strings.Contains(s, "> ## 注意") || !strings.Contains(s, "> 正文内容") {
		t.Errorf("callout children not quoted:\n%s", s)
	}
	if n := strings.Count(s, "正文内容"); n != 1 {
		t.Errorf("callout child duplicated (count=%d):\n%s", n, s)
	}
}

func TestBlocksToMarkdown_QuoteContainerQuotesChildren(t *testing.T) {
	blocks := []DocxBlock{
		{BlockID: "root", BlockType: BlockTypePage},
		{BlockID: "q", BlockType: BlockTypeQuoteContainer, Children: []string{"p"}},
		{BlockID: "p", BlockType: BlockTypeText, Text: txt("引用内容")},
	}
	md, _, _, err := blocksToMarkdown(context.Background(), nil, blocks, "")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	s := string(md)
	if !strings.Contains(s, "> 引用内容") {
		t.Errorf("quote_container children not quoted:\n%s", s)
	}
	if n := strings.Count(s, "引用内容"); n != 1 {
		t.Errorf("quote_container child duplicated (count=%d):\n%s", n, s)
	}
}

func TestBlocksToMarkdown_GridColumnsJoinedByRule(t *testing.T) {
	// 分栏: each grid_column's content renders in column order, columns
	// separated by ---, and nothing leaks out as stray paragraphs.
	blocks := []DocxBlock{
		{BlockID: "root", BlockType: BlockTypePage},
		{BlockID: "g", BlockType: BlockTypeGrid, Grid: &BlockGrid{ColumnSize: 2}, Children: []string{"c1", "c2"}},
		{BlockID: "c1", BlockType: BlockTypeGridColumn, GridColumn: &BlockGridColumn{WidthRatio: 50}, Children: []string{"p1"}},
		{BlockID: "c2", BlockType: BlockTypeGridColumn, GridColumn: &BlockGridColumn{WidthRatio: 50}, Children: []string{"p2"}},
		{BlockID: "p1", BlockType: BlockTypeText, Text: txt("左栏")},
		{BlockID: "p2", BlockType: BlockTypeText, Text: txt("右栏")},
	}
	md, _, _, err := blocksToMarkdown(context.Background(), nil, blocks, "")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	s := string(md)
	want := "左栏\n\n---\n\n右栏"
	if !strings.Contains(s, want) {
		t.Errorf("grid columns not joined by ---, want substring %q, got:\n%s", want, s)
	}
	for _, p := range []string{"左栏", "右栏"} {
		if n := strings.Count(s, p); n != 1 {
			t.Errorf("grid column content %q duplicated (count=%d):\n%s", p, n, s)
		}
	}
}

func TestBlocksToMarkdown_IframeLink(t *testing.T) {
	blocks := []DocxBlock{
		{BlockID: "root", BlockType: BlockTypePage},
		{BlockID: "f", BlockType: BlockTypeIframe, Iframe: &BlockIframe{
			Component: &BlockIframeComponent{Type: 1, URL: "https://example.com/embed"},
		}},
	}
	md, _, _, err := blocksToMarkdown(context.Background(), nil, blocks, "")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !strings.Contains(string(md), "[内嵌网页](https://example.com/embed)") {
		t.Errorf("iframe not rendered as link:\n%s", md)
	}
}

func TestBlocksToMarkdown_UnsupportedBlocksPlaceholdersNoTokenLeak(t *testing.T) {
	blocks := []DocxBlock{
		{BlockID: "root", BlockType: BlockTypePage},
		{BlockID: "mn", BlockType: BlockTypeMindnote, Mindnote: &BlockTokenRef{Token: "bmnbSECRET"}},
		{BlockID: "bd", BlockType: BlockTypeBoard, Board: &BlockBoard{Token: "brdSECRET"}},
		{BlockID: "dg", BlockType: BlockTypeDiagram, Diagram: &BlockDiagram{DiagramType: 1}},
		{BlockID: "cc", BlockType: BlockTypeChatCard, ChatCard: &BlockChatCard{ChatID: "oc_SECRET"}},
		{BlockID: "jira", BlockType: BlockTypeJiraIssue, JiraIssue: &BlockJiraIssue{ID: "1", Key: "AB-1"}},
		// view (33) is no longer a placeholder: an empty view emits nothing.
		{BlockID: "v", BlockType: BlockTypeView},
		{BlockID: "unk", BlockType: 999},
	}
	md, atts, imgs, err := blocksToMarkdown(context.Background(), nil, blocks, "")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	s := string(md)
	for _, want := range []string{
		"> [飞书块: 思维笔记]", "> [飞书块: 流程图&UML]",
		"> [飞书块: 会话卡片]", "> [飞书块: Jira问题]", "> [飞书块: 未知类型(999)]",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("missing placeholder %q, got:\n%s", want, s)
		}
	}
	// Removed from the placeholder set: view renders its children (none here).
	if strings.Contains(s, "飞书块: 视图") {
		t.Errorf("empty view must not emit a placeholder:\n%s", s)
	}
	// Board (43) now rides the image pipeline: a token-bearing board renders a
	// numbered weknora-img:// marker and records a pendingImage for the
	// connector's whiteboard download.
	if !strings.Contains(s, "![图片](weknora-img://1)") {
		t.Errorf("board marker missing:\n%s", s)
	}
	if len(imgs) != 1 || imgs[0].Kind != "board" || imgs[0].Token != "brdSECRET" {
		t.Errorf("pendingImages = %+v", imgs)
	}
	// No-leak: internal media tokens must never reach the markdown.
	for _, tok := range []string{"SECRET", "bmnb", "brd", "oc_"} {
		if strings.Contains(s, tok) {
			t.Errorf("token fragment %q leaked into markdown:\n%s", tok, s)
		}
	}
	if len(atts) != 0 {
		t.Errorf("placeholders must not collect attachments, got %+v", atts)
	}
}

// TestBlocksToMarkdown_ViewContainerRendersChildren anchors the view (33)
// handling: the view is the file-list container seen in real smoke tests, so
// its children (file blocks etc.) must render inline, exactly once, and an
// empty view must contribute nothing — no `> [飞书块: 视图]` placeholder.
func TestBlocksToMarkdown_ViewContainerRendersChildren(t *testing.T) {
	blocks := []DocxBlock{
		{BlockID: "root", BlockType: BlockTypePage},
		{BlockID: "view", BlockType: BlockTypeView, Children: []string{"f1", "f2"}},
		{BlockID: "f1", BlockType: BlockTypeFile, File: &BlockFileRef{Token: "ftok1", Name: "报表.pdf"}},
		{BlockID: "f2", BlockType: BlockTypeFile, File: &BlockFileRef{Token: "ftok2", Name: "手册.pdf"}},
	}
	md, atts, _, err := blocksToMarkdown(context.Background(), nil, blocks, "")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	s := string(md)
	if strings.Contains(s, "飞书块: 视图") {
		t.Errorf("view must not render a placeholder:\n%s", s)
	}
	if n := strings.Count(s, "- 报表.pdf"); n != 1 {
		t.Errorf("file child rendered %d times, want exactly 1:\n%s", n, s)
	}
	if !strings.Contains(s, "- 报表.pdf\n- 手册.pdf") {
		t.Errorf("view children not rendered as one list:\n%s", s)
	}
	if len(atts) != 2 {
		t.Errorf("view children must still be collected as attachments, got %+v", atts)
	}
}

func TestBlocksToMarkdown_RichTextElements(t *testing.T) {
	blocks := []DocxBlock{
		{BlockID: "root", BlockType: BlockTypePage},
		{BlockID: "p", BlockType: BlockTypeText, Text: &BlockText{Elements: []TextElement{
			{TextRun: &TextRun{Content: "加粗", TextElementStyle: &TextElementStyle{Bold: true}}},
			{TextRun: &TextRun{Content: "code", TextElementStyle: &TextElementStyle{InlineCode: true}}},
			{TextRun: &TextRun{Content: "删除", TextElementStyle: &TextElementStyle{Strikethrough: true, Italic: true}}},
			{TextRun: &TextRun{Content: "链接", TextElementStyle: &TextElementStyle{Link: &TextElementLink{URL: "https://x.cn"}}}},
			{MentionDoc: &MentionDoc{URL: "https://x.cn/doc"}},
			{MentionUser: &MentionUser{UserID: "ou_1"}},
			{Equation: &Equation{Content: "E=mc^2"}},
		}}},
	}
	md, _, _, err := blocksToMarkdown(context.Background(), nil, blocks, "")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	s := string(md)
	for _, want := range []string{
		"**加粗**", "`code`", "~~*删除*~~", "[链接](https://x.cn)",
		"[https://x.cn/doc](https://x.cn/doc)", "@成员", "$E=mc^2$",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("missing rich text %q, got:\n%s", want, s)
		}
	}
}

func TestBlocksToMarkdown_CodeBlockLanguage(t *testing.T) {
	mk := func(lang int) string {
		blocks := []DocxBlock{
			{BlockID: "root", BlockType: BlockTypePage},
			{BlockID: "c", BlockType: BlockTypeCode,
				Code: &BlockText{Style: &BlockTextStyle{Language: lang}, Elements: []TextElement{{TextRun: &TextRun{Content: "x"}}}}},
		}
		md, _, _, err := blocksToMarkdown(context.Background(), nil, blocks, "")
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		return string(md)
	}
	cases := map[int]string{
		22: "```go\nx\n```",         // Go
		9:  "```cpp\nx\n```",        // C++
		8:  "```c#\nx\n```",         // CSharp
		46: "```powershell\nx\n```", // Power Shell
		1:  "```\nx\n```",           // PlainText → untagged
		99: "```\nx\n```",           // unknown → untagged
	}
	for lang, want := range cases {
		if got := mk(lang); !strings.Contains(got, want) {
			t.Errorf("language %d: want %q in output, got:\n%s", lang, want, got)
		}
	}
}

func TestBlocksToMarkdown_OrderedListRealSequence(t *testing.T) {
	ord := func(seq string, content string) DocxBlock {
		b := DocxBlock{BlockID: content, BlockType: BlockTypeOrdered}
		b.Ordered = txt(content)
		if seq != "" {
			b.Ordered.Style = &BlockTextStyle{Sequence: seq}
		}
		return b
	}
	blocks := []DocxBlock{
		{BlockID: "root", BlockType: BlockTypePage},
		ord("1", "甲"), ord("auto", "乙"), ord("auto", "丙"),
		{BlockID: "sep", BlockType: BlockTypeText, Text: txt("分隔")},
		ord("5", "戊"), ord("auto", "己"),
		ord("", "无序号"), ord("", "续"),
		{BlockID: "sep2", BlockType: BlockTypeText, Text: txt("第二段")},
		ord("", "新列表"),
	}
	md, _, _, err := blocksToMarkdown(context.Background(), nil, blocks, "")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	s := string(md)
	for _, want := range []string{"1. 甲", "2. 乙", "3. 丙", "5. 戊", "6. 己", "7. 无序号", "8. 续", "1. 新列表"} {
		if !strings.Contains(s, want) {
			t.Errorf("missing ordered item %q, got:\n%s", want, s)
		}
	}
}

func TestBlocksToMarkdown_NativeTableMergedCellsFilled(t *testing.T) {
	// merge_info[0] anchors at cell 0 with row_span=2, col_span=2: the anchor
	// value must fill all four covered cells (GFM has no cell span).
	blocks := []DocxBlock{
		{BlockID: "root", BlockType: BlockTypePage},
		tableBlk("t", 3, "c1", "c2", "c3", "c4", "c5", "c6"),
		cellBlk("c1"), cellBlk("c2"), cellBlk("c3"),
		cellBlk("c4"), cellBlk("c5"), cellBlk("c6"),
		cellTextBlk("c1", "合并"), cellTextBlk("c2", ""), cellTextBlk("c3", "B"),
		cellTextBlk("c4", "C"), cellTextBlk("c5", "D"), cellTextBlk("c6", "E"),
	}
	blocks[1].Table.Property.MergeInfo = []BlockTableMergeInfo{{RowSpan: 2, ColSpan: 2}}
	md, _, _, err := blocksToMarkdown(context.Background(), nil, blocks, "")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	s := string(md)
	// Cells covered by the merge (c2, c4, c5) take the anchor value; uncovered
	// cells (c3=B, c6=E) keep their own.
	if !strings.Contains(s, "| 合并 | 合并 | B |") || !strings.Contains(s, "| 合并 | 合并 | E |") {
		t.Errorf("merged cells not filled with anchor value:\n%s", s)
	}
	if strings.Contains(s, "C") || strings.Contains(s, "D") {
		t.Errorf("covered cells must show the anchor value, not their own:\n%s", s)
	}
}

func TestBlocksToMarkdown_DocumentTruncationNote(t *testing.T) {
	// A block array at the maxDocumentBlocks cap means listDocumentBlocks
	// dropped content; the markdown must end with an explicit note (aligned
	// with the embedded-table truncation note) instead of failing silently.
	blocks := make([]DocxBlock, maxDocumentBlocks)
	for i := range blocks {
		blocks[i] = DocxBlock{BlockID: strconv.Itoa(i), BlockType: BlockTypeText, Text: txt("块")}
	}
	md, _, _, err := blocksToMarkdown(context.Background(), nil, blocks, "")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !strings.Contains(string(md), "文档已截断") {
		t.Errorf("missing document truncation note:\n...%s", md[len(md)-200:])
	}
}

func TestBlocksToMarkdown_ImageNumberingStableOrder(t *testing.T) {
	// Marker sequence numbers must follow document order across image, board,
	// and file blocks interleaved — shared.go fans sub-items out from the same
	// pendingImage list, so renderer-side numbering is the single source of truth.
	blocks := []DocxBlock{
		{BlockID: "root", BlockType: BlockTypePage},
		{BlockID: "p", BlockType: BlockTypeText, Text: txt("引言")},
		{BlockID: "i1", BlockType: BlockTypeImage, Image: &BlockTokenRef{Token: "tok-a"}},
		{BlockID: "f1", BlockType: BlockTypeFile, File: &BlockFileRef{Token: "ft-1", Name: "附录.pdf"}},
		{BlockID: "i2", BlockType: BlockTypeImage, Image: &BlockTokenRef{Token: "tok-b"}},
		{BlockID: "bd", BlockType: BlockTypeBoard, Board: &BlockBoard{Token: "brd-9"}},
	}
	md, _, imgs, err := blocksToMarkdown(context.Background(), nil, blocks, "")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	s := string(md)
	for _, want := range []string{"![图片](weknora-img://1)", "![图片](weknora-img://2)", "![图片](weknora-img://3)"} {
		if !strings.Contains(s, want) {
			t.Errorf("marker %q missing:\n%s", want, s)
		}
	}
	wantList := []pendingImage{
		{N: 1, Kind: "image", Token: "tok-a"},
		{N: 2, Kind: "image", Token: "tok-b"},
		{N: 3, Kind: "board", Token: "brd-9"},
	}
	if len(imgs) != len(wantList) {
		t.Fatalf("imgs = %+v, want %+v", imgs, wantList)
	}
	for i, w := range wantList {
		if imgs[i] != w {
			t.Errorf("imgs[%d] = %+v, want %+v", i, imgs[i], w)
		}
	}
}
