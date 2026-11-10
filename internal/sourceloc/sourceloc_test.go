package sourceloc

import (
	"archive/zip"
	"bytes"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xuri/excelize/v2"

	"github.com/Tencent/WeKnora/internal/types"
)

func runeIndex(s, sub string) int {
	i := strings.Index(s, sub)
	if i < 0 {
		return -1
	}
	return len([]rune(s[:i]))
}

func TestRemapperFollowsRewrites(t *testing.T) {
	oldText := "第一段\r\n\r\n<table><tr><td>a</td></tr></table>\r\n\r\n![img](images/x.png)\r\n\r\n最后一段文字"
	newText := "第一段\n\n| a |\n| --- |\n\n![img](https://cdn.example/x.png)\n\n最后一段文字"
	r := NewRemapper(oldText, newText)

	assert.Equal(t, 0, r.Map(0))
	assert.Equal(t, runeIndex(newText, "最后"), r.Map(runeIndex(oldText, "最后")))
	assert.Equal(t, runeIndex(newText, "段文字")+1, r.Map(runeIndex(oldText, "段文字")+1))
	assert.Equal(t, len([]rune(newText)), r.Map(len([]rune(oldText))))

	// Offsets inside the rewritten table stay inside the rewritten table.
	tableOld := runeIndex(oldText, "<table>")
	mapped := r.Map(tableOld + 5)
	assert.GreaterOrEqual(t, mapped, runeIndex(newText, "| a |"))
	assert.Less(t, mapped, runeIndex(newText, "![img]"))
}

func TestRemapBlocksDropsCollapsedBlocks(t *testing.T) {
	blocks := []types.SourceBlock{{Start: 0, End: 3, Locator: types.SourceLocator{Type: "pdf", Page: 1}}}
	out := RemapBlocks(blocks, "abc\ndef", "abc\ndef")
	require.Len(t, out, 1)
	assert.Equal(t, 3, out[0].End)
}

// A scanned PDF is only page images and blank lines, so no line survives the
// image URL rewrite; each page must still land on its own line.
func TestRemapBlocksKeepsRewrittenLinesOnTheirPages(t *testing.T) {
	var oldLines, newLines []string
	var blocks []types.SourceBlock
	offset := 0
	for page := 1; page <= 300; page++ {
		line := fmt.Sprintf("![doc_page_%d.jpg](images/doc_page_%d.jpg)", page, page)
		if page > 1 {
			offset += 2
		}
		blocks = append(blocks, types.SourceBlock{
			Start: offset, End: offset + len(line),
			Locator: types.SourceLocator{Type: types.SourceLocatorPDF, Page: page},
		})
		offset += len(line)
		oldLines = append(oldLines, line)
		newLines = append(newLines, fmt.Sprintf("![doc_page_%d.jpg](local://1/images/0123456789abcdef.jpg)", page))
	}
	oldText, newText := strings.Join(oldLines, "\n\n"), strings.Join(newLines, "\n\n")
	idx := NewIndex(newText, RemapBlocks(blocks, oldText, newText))
	for i, line := range newLines {
		at := strings.Index(newText, line) + strings.Index(line, "local://")
		locs := idx.LocatorsAt(utf8.RuneCountInString(newText[:at]))
		require.Len(t, locs, 1, "page %d", i+1)
		assert.Equal(t, i+1, locs[0].Page)
	}
}

func TestAlignPlacesUnitsInOrder(t *testing.T) {
	md := "# 公路工程\n\n8.14.11 索夹与吊索施工应符合下列规定：\n\n1. 在满足施工需要的前提下，应减小猫道面层开孔面积。\n\n" +
		"![图](data:image/png;base64,QUJDREVGR0g=)\n\n| 列 | 值 |\n| --- | --- |\n| 高度 | 12m |\n"
	units := []Unit{
		{Text: "公路工程", Locator: types.SourceLocator{Type: "docx", Block: 1}},
		{Text: "8.14.11 索夹与吊索施工应符合下列规定:", Locator: types.SourceLocator{Type: "docx", Block: 2}},
		{Text: "在满足施工需要的前提下,应减小猫道面层开孔面积。", Locator: types.SourceLocator{Type: "docx", Block: 3}},
		{Text: "不存在的段落内容不会被放置", Locator: types.SourceLocator{Type: "docx", Block: 4}},
		{Text: "列 值", Locator: types.SourceLocator{Type: "docx", Block: 5}},
		{Text: "高度 12m", Locator: types.SourceLocator{Type: "docx", Block: 5}},
	}
	blocks := Align(md, units)
	require.Len(t, blocks, 5)
	assert.Equal(t, 1, blocks[0].Locator.Block)
	assert.Equal(t, runeIndex(md, "8.14.11"), blocks[1].Start)
	assert.Equal(t, blocks[1].End, blocks[2].Start)
	assert.Equal(t, runeIndex(md, "1. 在满足"), blocks[2].Start, "list numbering stays with its paragraph")
	assert.Equal(t, 5, blocks[4].Locator.Block)
	assert.Equal(t, len([]rune(md)), blocks[4].End)
}

func TestIndexLocatorsFoldsAndQuotes(t *testing.T) {
	md := "第一页正文。\n\n第二页正文开始\n\n第二页正文继续\n\n第三页"
	p1 := runeIndex(md, "第二页正文开始")
	p2 := runeIndex(md, "第二页正文继续")
	p3 := runeIndex(md, "第三页")
	blocks := []types.SourceBlock{
		{Start: 0, End: p1, Locator: types.SourceLocator{Type: "pdf", Page: 1}},
		{Start: p1, End: p2, Locator: types.SourceLocator{Type: "pdf", Page: 2}},
		{Start: p2, End: p3, Locator: types.SourceLocator{Type: "pdf", Page: 2}},
		{Start: p3, End: len([]rune(md)), Locator: types.SourceLocator{
			Type: "pdf", Page: 3, BBox: []float64{0.1, 0.1, 0.9, 0.2},
		}},
	}
	idx := NewIndex(md, blocks)
	locs := idx.Locators(p1+2, len([]rune(md)))
	require.Len(t, locs, 2)
	assert.Equal(t, 2, locs[0].Page)
	assert.Equal(t, "页正文开始 第二页正文继续", locs[0].Quote)
	assert.Equal(t, 3, locs[1].Page)
	assert.Equal(t, []float64{0.1, 0.1, 0.9, 0.2}, locs[1].BBox)
	assert.Nil(t, idx.Locators(5, 5))
}

func TestAppendLocatorMergesSheetRows(t *testing.T) {
	var list types.SourceLocators
	list = AppendLocator(list, types.SourceLocator{Type: "sheet", Sheet: "S1", RowStart: 3, RowEnd: 3, Quote: "a"})
	list = AppendLocator(list, types.SourceLocator{Type: "sheet", Sheet: "S1", RowStart: 4, RowEnd: 4, Quote: "b"})
	list = AppendLocator(list, types.SourceLocator{Type: "sheet", Sheet: "S1", RowStart: 9, RowEnd: 9, Quote: "c"})
	require.Len(t, list, 2)
	assert.Equal(t, 3, list[0].RowStart)
	assert.Equal(t, 4, list[0].RowEnd)
	assert.Equal(t, "a b", list[0].Quote)
}

func TestMergeLocatorsDedupes(t *testing.T) {
	a := types.SourceLocators{{Type: "slide", Slide: 1}}
	b := types.SourceLocators{{Type: "slide", Slide: 1}, {Type: "slide", Slide: 2}}
	assert.Len(t, MergeLocators(a, b), 2)
}

func TestCleanQuote(t *testing.T) {
	got := CleanQuote("见 ![图1](https://x/y.png) 和 [链接](https://a.b)\n<b>加粗</b>  <!-- Slide number: 3 -->")
	assert.Equal(t, "见 和 链接 加粗", got)
}

func TestTextBlocks(t *testing.T) {
	content := "# 标题\n\n第一段\n第一段续\n\n\n第二段"
	blocks := TextBlocks(content)
	require.Len(t, blocks, 3)
	assert.Equal(t, 0, blocks[0].Start)
	assert.Equal(t, runeIndex(content, "第一段"), blocks[1].Start)
	assert.Equal(t, runeIndex(content, "第二段"), blocks[2].Start)
	assert.Equal(t, len([]rune(content)), blocks[2].End)
	assert.Equal(t, blocks[1].Start, blocks[1].Locator.Start)
}

func zipOf(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range files {
		w, err := zw.Create(name)
		require.NoError(t, err)
		_, err = w.Write([]byte(body))
		require.NoError(t, err)
	}
	require.NoError(t, zw.Close())
	return buf.Bytes()
}

func TestDocxUnits(t *testing.T) {
	doc := `<?xml version="1.0"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body>
<w:p><w:r><w:t>标题</w:t></w:r></w:p>
<w:sdt><w:sdtPr><w:alias w:val="x"/></w:sdtPr><w:sdtContent>
<w:p><w:r><w:t>控件内</w:t></w:r><w:r><w:delText>删除</w:delText></w:r></w:p>
</w:sdtContent></w:sdt>
<w:tbl><w:tr><w:tc><w:p><w:r><w:t>A1</w:t></w:r></w:p></w:tc><w:tc><w:p><w:r><w:t>B1</w:t></w:r></w:p></w:tc></w:tr>
<w:tr><w:tc><w:p><w:r><w:t>A2</w:t></w:r></w:p></w:tc></w:tr></w:tbl>
<w:p/>
<w:p><w:r><w:t xml:space="preserve">末段 </w:t></w:r><w:r><w:tab/><w:t>文字</w:t></w:r></w:p>
<w:sectPr/></w:body></w:document>`
	units, err := docxUnits(zipOf(t, map[string]string{"word/document.xml": doc}))
	require.NoError(t, err)
	var got []string
	for _, u := range units {
		got = append(got, u.Text+"@"+string(rune('0'+u.Locator.Block)))
	}
	assert.Equal(t, []string{"标题@1", "控件内@2", "A1 B1@3", "A2@3", "末段  文字@5"}, got)
}

func TestPptxUnits(t *testing.T) {
	const (
		pNS = `xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main"`
		rNS = `xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"`
	)
	files := map[string]string{
		"ppt/presentation.xml": `<p:presentation ` + pNS + ` ` + rNS + `><p:sldIdLst>` +
			`<p:sldId id="256" r:id="rId3"/><p:sldId id="257" r:id="rId2"/></p:sldIdLst></p:presentation>`,
		"ppt/_rels/presentation.xml.rels": `<Relationships>` +
			`<Relationship Id="rId2" Type="http://x/slide" Target="slides/slide1.xml"/>` +
			`<Relationship Id="rId3" Type="http://x/slide" Target="slides/slide2.xml"/></Relationships>`,
		"ppt/slides/slide1.xml": `<p:sld xmlns:a="a"><a:p><a:r><a:t>第二张</a:t></a:r></a:p></p:sld>`,
		"ppt/slides/slide2.xml": `<p:sld xmlns:a="a"><a:p><a:r><a:t>封面</a:t></a:r>` +
			`<a:r><a:t>标题</a:t></a:r></a:p><a:p/></p:sld>`,
		"ppt/slides/_rels/slide2.xml.rels": `<Relationships><Relationship Id="rId1" ` +
			`Type="http://x/notesSlide" Target="../notesSlides/notesSlide1.xml"/></Relationships>`,
		"ppt/notesSlides/notesSlide1.xml": `<p:notes xmlns:a="a"><a:p><a:r><a:t>备注</a:t></a:r></a:p></p:notes>`,
	}
	units, err := pptxUnits(zipOf(t, files))
	require.NoError(t, err)
	require.Len(t, units, 3)
	assert.Equal(t, "封面标题", units[0].Text)
	assert.Equal(t, 1, units[0].Locator.Slide)
	assert.Equal(t, "备注", units[1].Text)
	assert.Equal(t, 1, units[1].Locator.Slide)
	assert.Equal(t, 2, units[2].Locator.Slide)
}

func TestXlsxUnits(t *testing.T) {
	f := excelize.NewFile()
	require.NoError(t, f.SetCellValue("Sheet1", "A1", "名称"))
	require.NoError(t, f.SetCellValue("Sheet1", "B1", "数量"))
	require.NoError(t, f.SetCellValue("Sheet1", "A3", "螺栓"))
	require.NoError(t, f.SetCellValue("Sheet1", "B3", 12))
	_, err := f.NewSheet("汇总")
	require.NoError(t, err)
	require.NoError(t, f.SetCellValue("汇总", "A2", "合计"))
	buf, err := f.WriteToBuffer()
	require.NoError(t, err)

	units, err := xlsxUnits(buf.Bytes())
	require.NoError(t, err)
	require.Len(t, units, 3)
	assert.Equal(t, "螺栓 12", units[1].Text)
	assert.Equal(t, 3, units[1].Locator.RowStart)
	assert.Equal(t, "汇总", units[2].Locator.Sheet)
	assert.Equal(t, 2, units[2].Locator.RowStart)

	md := "## Sheet1\n| 名称 | 数量 |\n| --- | --- |\n| 螺栓 | 12 |\n\n## 汇总\n| 合计 |\n"
	blocks := Align(md, units)
	require.Len(t, blocks, 3)
	assert.Equal(t, runeIndex(md, "| 螺栓"), blocks[1].Start, "a table row starts at its line")
}

func TestEpubUnits(t *testing.T) {
	files := map[string]string{
		"META-INF/container.xml": `<container><rootfiles>` +
			`<rootfile full-path="OEBPS/content.opf"/></rootfiles></container>`,
		"OEBPS/content.opf": `<package><manifest><item id="c1" href="ch1.xhtml"/>` +
			`<item id="c2" href="text/ch2.xhtml"/></manifest>` +
			`<spine><itemref idref="c1"/><itemref idref="c2"/></spine></package>`,
		"OEBPS/ch1.xhtml":      `<html><head><title>x</title></head><body><h1>第一章</h1><p>开篇<b>正文</b></p></body></html>`,
		"OEBPS/text/ch2.xhtml": `<html><body><h2>第二章</h2><p>后文</p><script>var a=1</script></body></html>`,
	}
	units, err := epubUnits(zipOf(t, files))
	require.NoError(t, err)
	require.Len(t, units, 4)
	assert.Equal(t, "开篇 正文", units[1].Text)
	assert.Equal(t, "第一章", units[1].Locator.Title)
	assert.Equal(t, 2, units[3].Locator.Section)
}

func TestCSVUnits(t *testing.T) {
	units, err := csvUnits([]byte("name,qty\nbolt,12\n\nnut,3\n"))
	require.NoError(t, err)
	require.Len(t, units, 3)
	assert.Equal(t, 3, units[2].Locator.RowStart)
}
