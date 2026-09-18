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

func TestNormalizeHTMLTables_StripsAttrsOnSpanTables(t *testing.T) {
	// rowspan/colspan cannot be expressed in Markdown, so the table stays HTML
	// but its presentational attributes are stripped.
	input := `<table><tr><td colspan="2" style="text-align:center;" class="hdr">合计</td></tr>` +
		`<tr><td style="text-align:left;">A</td><td width="80">B</td></tr></table>`

	got := NormalizeHTMLTables(input)

	if !strings.Contains(got, "<table") {
		t.Fatalf("expected span table to remain HTML, got:\n%s", got)
	}
	if !strings.Contains(got, `colspan="2"`) {
		t.Fatalf("expected colspan to be preserved, got:\n%s", got)
	}
	for _, banned := range []string{"text-align", "class=", "width="} {
		if strings.Contains(got, banned) {
			t.Fatalf("expected %q to be stripped, got:\n%s", banned, got)
		}
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
	input := `<table><tr><td rowspan="1" colspan="1" style="text-align:center;">指标</td><td rowspan=1 colspan=1>数值</td></tr>` +
		`<tr><td rowspan="1" colspan='1'>营收</td><td rowspan = 1 colspan = 1>10亿</td></tr></table>`

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

func TestNormalizeHTMLTables_RealSpanKeepsHTMLWithRowNewlines(t *testing.T) {
	// Genuine merges (colspan>1 / rowspan>1) cannot be GFM, so the table stays
	// HTML — but each row must start on its own line so the chunker can split it.
	input := `<table><tr><td colspan="6" style="text-align:center;">合计</td></tr>` +
		`<tr><td rowspan="3">A</td><td>1</td></tr>` +
		`<tr><td rowspan = 2>B</td><td>2</td></tr></table>`

	got := NormalizeHTMLTables(input)

	if !strings.Contains(got, "<table") {
		t.Fatalf("expected real span table to remain HTML, got:\n%s", got)
	}
	if !strings.Contains(got, `colspan="6"`) || !strings.Contains(got, `rowspan="3"`) {
		t.Fatalf("expected structural span attrs preserved, got:\n%s", got)
	}
	if !strings.Contains(got, "\n<tr") {
		t.Fatalf("expected each <tr> to start on a new line, got:\n%q", got)
	}
	if !strings.Contains(got, "\n\n<table") {
		t.Fatalf("expected block to be padded at the head with blank lines, got:\n%q", got)
	}
	if !strings.HasSuffix(got, "</table>\n\n") {
		t.Fatalf("expected block to be padded at the tail with blank lines, got:\n%q", got)
	}
	if n := strings.Count(got, "\n<tr"); n != 3 {
		t.Fatalf("expected 3 rows each preceded by a newline, got %d in:\n%q", n, got)
	}
}
