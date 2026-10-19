package docparser

import (
	"strings"
	"testing"
)

func TestNormalizeHTMLTables_ConvertsStyledTableToMarkdown(t *testing.T) {
	// Mirrors PaddleOCR-VL output: a data table where every cell carries a
	// text-align style that wastes tokens.
	input := `# 报告

<table><tr><td style="text-align:center;">指标</td><td style="text-align:center;">数值</td></tr>` +
		`<tr><td style="text-align:center;">营收</td><td style="text-align:right;">10亿</td></tr>` +
		`<tr><td style="text-align:center;">利润</td><td style="text-align:right;">2.3亿</td></tr></table>

结尾。`

	got := NormalizeHTMLTables(input)

	if strings.Contains(got, "<table") {
		t.Fatalf("expected HTML table to be converted away, got:\n%s", got)
	}
	if strings.Contains(got, "text-align") {
		t.Fatalf("expected style attributes removed, got:\n%s", got)
	}
	if !markdownTableSeparatorPattern.MatchString(got) {
		t.Fatalf("expected a Markdown table separator row, got:\n%s", got)
	}
	for _, want := range []string{"指标", "数值", "营收", "10亿", "利润", "2.3亿"} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected converted table to retain %q, got:\n%s", want, got)
		}
	}
	if !strings.Contains(got, "# 报告") || !strings.Contains(got, "结尾。") {
		t.Fatalf("expected surrounding markdown to be preserved, got:\n%s", got)
	}
}

func TestNormalizeHTMLTables_NoTableUnchanged(t *testing.T) {
	input := "# 标题\n\n普通段落，没有表格。\n\n| a | b |\n| --- | --- |\n| 1 | 2 |"
	if got := NormalizeHTMLTables(input); got != input {
		t.Fatalf("expected content without HTML tables to be unchanged, got:\n%s", got)
	}
}

func TestNormalizeHTMLTables_SpanValueOneIsConvertible(t *testing.T) {
	// rowspan=1 / colspan=1 merge nothing, so the table must still become GFM.
	// Covers quoted, unquoted and space-padded attribute forms.
	input := `<table><tr>` +
		`<td rowspan="1" colspan="1" style="text-align:center;">指标</td>` +
		`<td rowspan=1 colspan=1>数值</td></tr>` +
		`<tr><td rowspan="1" colspan='1'>营收</td>` +
		`<td rowspan = 1 colspan = 1>10亿</td></tr></table>`

	got := NormalizeHTMLTables(input)

	if strings.Contains(got, "<table") {
		t.Fatalf("expected span=1 table to be converted away, got:\n%s", got)
	}
	if !markdownTableSeparatorPattern.MatchString(got) {
		t.Fatalf("expected a Markdown table separator row, got:\n%s", got)
	}
	for _, want := range []string{"指标", "数值", "营收", "10亿"} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected converted table to retain %q, got:\n%s", want, got)
		}
	}
}

func TestNormalizeHTMLTables_RealSpanExpandsToGFM(t *testing.T) {
	// rowspan/colspan are expanded into a rectangular grid so every output row
	// remains self-contained and can be split independently.
	input := `<table><tr><td colspan="6" style="text-align:center;">合计</td></tr>` +
		`<tr><td rowspan="3">A</td><td>1</td></tr>` +
		`<tr><td rowspan = 2>B</td><td>2</td></tr></table>`

	got := NormalizeHTMLTables(input)

	if strings.Contains(got, "<table") {
		t.Fatalf("expected merged-cell table to be expanded into GFM, got:\n%s", got)
	}
	if !markdownTableSeparatorPattern.MatchString(got) {
		t.Fatalf("expected a Markdown table separator row, got:\n%s", got)
	}
	for _, want := range []string{"合计", "A", "B", "1", "2"} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected expanded table to retain %q, got:\n%s", want, got)
		}
	}
}

func assertHTMLRowsSplittable(t *testing.T, got string) {
	t.Helper()
	if !strings.Contains(got, "<tr") {
		t.Fatalf("expected remaining HTML table rows, got:\n%s", got)
	}
	for i := 0; ; {
		idx := strings.Index(got[i:], "<tr")
		if idx < 0 {
			return
		}
		abs := i + idx
		if abs == 0 || got[abs-1] != '\n' {
			t.Fatalf("remaining <tr> at offset %d is not preceded by a newline:\n%s", abs, got)
		}
		i = abs + 1
	}
}

func compactMarkdownTableRow(row string) string {
	cells := strings.Split(strings.Trim(strings.TrimSpace(row), "|"), "|")
	for i := range cells {
		cells[i] = strings.TrimSpace(cells[i])
	}
	return strings.Join(cells, "|")
}

func TestNormalizeHTMLTables_SpanTableExpandsToGFM(t *testing.T) {
	// A colspan is expanded into empty cells and the original presentational
	// attributes are dropped when the table is converted to Markdown.
	input := `<table><tr>` +
		`<td colspan="2" style="text-align:center;" class="hdr">合计</td></tr>` +
		`<tr><td style="text-align:left;">A</td><td width="80">B</td></tr></table>`

	got := NormalizeHTMLTables(input)

	if strings.Contains(got, "<table") {
		t.Fatalf("expected span table to be expanded into GFM, got:\n%s", got)
	}
	if !markdownTableSeparatorPattern.MatchString(got) {
		t.Fatalf("expected a Markdown table separator row, got:\n%s", got)
	}
	for _, want := range []string{"合计", "A", "B"} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected expanded table to retain %q, got:\n%s", want, got)
		}
	}
	for _, banned := range []string{"text-align", "class=", "width="} {
		if strings.Contains(got, banned) {
			t.Fatalf("expected %q to be stripped, got:\n%s", banned, got)
		}
	}
}

func TestNormalizeHTMLTables_BRCellsStaySplittableHTML(t *testing.T) {
	// <br> inside a cell makes the table plugin give up (it emits a newline).
	// The HTML fallback must still put each <tr> on its own line.
	input := `<table><tr><td>指标<br/>名称</td><td>数值<br/>单位</td></tr>` +
		`<tr><td>拉伸强度<br/>MPa</td><td>合格<br/>A级</td></tr></table>`

	got := NormalizeHTMLTables(input)

	if !strings.Contains(got, "<table") {
		t.Fatalf("expected <br> table to remain HTML rather than flattened text, got:\n%s", got)
	}
	assertHTMLRowsSplittable(t, got)
	for _, want := range []string{"指标", "名称", "拉伸强度", "合格"} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected fallback HTML to retain %q, got:\n%s", want, got)
		}
	}
}

func TestNormalizeHTMLTables_ListInCellFallsBackToSplittableHTML(t *testing.T) {
	input := `<table><tr><td><ul><li>a</li><li>b</li></ul></td><td>x</td></tr>` +
		`<tr><td>c</td><td>d</td></tr></table>`

	got := NormalizeHTMLTables(input)

	if !strings.Contains(got, "<table") {
		t.Fatalf("expected list-in-cell table to remain HTML, got:\n%s", got)
	}
	assertHTMLRowsSplittable(t, got)
}

func TestNormalizeHTMLTables_SingleColumnConvertsToGFM(t *testing.T) {
	input := `<table><tr><td>目录</td></tr><tr><td>第一章</td></tr><tr><td>第二章</td></tr></table>`

	got := NormalizeHTMLTables(input)

	if strings.Contains(got, "<table") {
		t.Fatalf("expected single-column table to convert to GFM, got:\n%s", got)
	}
	if !markdownTableSeparatorPattern.MatchString(got) {
		t.Fatalf("expected a Markdown table separator row, got:\n%s", got)
	}
	for _, want := range []string{"目录", "第一章", "第二章"} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected converted table to retain %q, got:\n%s", want, got)
		}
	}
}

func TestNormalizeHTMLTables_ImgInSingleColumnBecomesGFM(t *testing.T) {
	input := `<table><tr><td><img src="local://images/profile.png" alt="profile"/></td></tr></table>`

	got := NormalizeHTMLTables(input)

	if strings.Contains(got, "<table") {
		t.Fatalf("expected single-column image table to convert to GFM, got:\n%s", got)
	}
	if !strings.Contains(got, "![profile](local://images/profile.png)") {
		t.Fatalf("expected markdown image to be preserved, got:\n%s", got)
	}
}

func TestNormalizeHTMLTables_MarkdownImageInCellNotEscaped(t *testing.T) {
	input := `<table><tr><td>![cover](local://images/cover.png)</td><td>说明</td></tr></table>`

	got := NormalizeHTMLTables(input)

	if strings.Contains(got, `!\[`) {
		t.Fatalf("markdown image syntax escaped:\n%s", got)
	}
	if !strings.Contains(got, "![cover](local://images/cover.png)") {
		t.Fatalf("expected markdown image to survive conversion, got:\n%s", got)
	}
}

func TestNormalizeHTMLTables_CodeFenceTableLeftAlone(t *testing.T) {
	input := "示例：\n\n```html\n<table><tr><td>a</td><td>b</td></tr></table>\n```\n"

	got := NormalizeHTMLTables(input)

	if got != input {
		t.Fatalf("expected fenced HTML table to be unchanged, got:\n%s", got)
	}
}

func TestNormalizeHTMLTables_SpanTableIdempotent(t *testing.T) {
	input := `<table><tr><td colspan="6">汇总</td></tr><tr><td colspan="6">备注</td></tr></table>`
	once := NormalizeHTMLTables(input)
	twice := NormalizeHTMLTables(once)
	if once != twice {
		t.Fatalf("NormalizeHTMLTables is not idempotent for span tables:\n1=%q\n2=%q", once, twice)
	}
}

func TestNormalizeHTMLTables_MergedCellsExpandToGFM(t *testing.T) {
	input := `<table>` +
		`<tr><th>序号</th><th>设备</th><th>数量</th><th>单价</th><th>合计</th></tr>` +
		`<tr><td rowspan="2">1</td><td rowspan="2">防火墙</td><td>1</td><td>100.00</td><td>100.00</td></tr>` +
		`<tr><td>2</td><td>200.00</td><td>200.00</td></tr>` +
		`<tr><td colspan="4">合计</td><td>300.00</td></tr>` +
		`</table>`

	got := NormalizeHTMLTables(input)

	if strings.Contains(got, "<table") {
		t.Fatalf("expected merged-cell table to be expanded into GFM, got:\n%s", got)
	}
	if !markdownTableSeparatorPattern.MatchString(got) {
		t.Fatalf("expected a Markdown table separator row, got:\n%s", got)
	}
	wantRows := map[string]bool{
		"1|防火墙|1|100.00|100.00": false,
		"1|防火墙|2|200.00|200.00": false,
		"合计||||300.00":          false,
	}
	for _, line := range strings.Split(got, "\n") {
		compacted := compactMarkdownTableRow(line)
		if _, ok := wantRows[compacted]; ok {
			wantRows[compacted] = true
		}
	}
	for row, found := range wantRows {
		if !found {
			t.Fatalf("expected expanded row %q, got:\n%s", row, got)
		}
	}
}

func TestNormalizeHTMLTables_NestedSpanTableFallsBack(t *testing.T) {
	input := `<table><tr><td rowspan="2">outer<table><tr><td>inner</td></tr></table></td><td>x</td></tr><tr><td>y</td></tr></table>`

	got := NormalizeHTMLTables(input)

	if !strings.Contains(got, "<table") || !strings.Contains(got, `rowspan="2"`) {
		t.Fatalf("expected nested-table expansion to fall back to HTML, got:\n%s", got)
	}
	if !strings.Contains(got, "inner") || !strings.Contains(got, "outer") {
		t.Fatalf("expected fallback to preserve table content, got:\n%s", got)
	}
}
