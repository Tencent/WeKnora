package confluence

import (
	"strings"
	"testing"
)

func TestConvertPageHTMLPreservesTables(t *testing.T) {
	input := `<h2>说明</h2><p>表格之前</p>` +
		`<table><tr><th>接口返回</th><th>集团场景</th></tr>` +
		`<tr><td>code=7001</td><td>不存在该 UID</td></tr></table>` +
		`<p>表格之间</p>` +
		`<table><tr><th>名称</th><td>第一行<br/>第二行</td></tr>` +
		`<tr><th>记录</th><td><ul><li>甲</li><li>乙</li></ul></td></tr></table>` +
		`<p>表格之后</p>`

	got, err := convertPageHTML(input)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"## 说明", "表格之前", "表格之间", "表格之后", "| 接口返回",
		"code=7001", "<table", "第一行", "第二行", "<li>甲</li>",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in converted page:\n%s", want, got)
		}
	}
	if first := strings.SplitN(got, "表格之间", 2)[0]; !strings.Contains(first, "|--") {
		t.Errorf("simple table has no Markdown separator row:\n%s", got)
	}
	if strings.Index(got, "表格之前") > strings.Index(got, "| 接口返回") ||
		strings.Index(got, "| 接口返回") > strings.Index(got, "表格之间") ||
		strings.Index(got, "表格之间") > strings.Index(got, "<table") ||
		strings.Index(got, "<table") > strings.Index(got, "表格之后") {
		t.Errorf("table order changed:\n%s", got)
	}
	if strings.Contains(got, "WEKNORACONFLUENCETABLE") {
		t.Errorf("table placeholder leaked into Markdown:\n%s", got)
	}
}

func TestConvertPageHTMLKeepsMergedCells(t *testing.T) {
	input := `<p>汇总</p><table><tr><td colspan="2">合计</td></tr>` +
		`<tr><td>A</td><td>B</td></tr></table><p>结束</p>`
	got, err := convertPageHTML(input)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, `<td colspan="2">合计</td>`) || !strings.Contains(got, "\n<tr") {
		t.Errorf("merged table structure was lost:\n%s", got)
	}
}

func TestConvertPageHTMLKeepsNestedTables(t *testing.T) {
	input := `<p>开始</p><table><tr><td>外层<table><tr><td>内层</td></tr></table></td></tr></table><p>结束</p>`
	got, err := convertPageHTML(input)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(got, "<table") != 2 || strings.Count(got, "</table>") != 2 || !strings.Contains(got, "\n<tr") ||
		!strings.Contains(got, "外层") || !strings.Contains(got, "内层") {
		t.Errorf("nested table structure was lost:\n%s", got)
	}
}

func TestConvertPageHTMLWithoutTables(t *testing.T) {
	got, err := convertPageHTML(`<p>普通 <strong>正文</strong></p>`)
	if err != nil {
		t.Fatal(err)
	}
	if got != "普通 **正文**" {
		t.Errorf("unexpected non-table Markdown: %q", got)
	}
}
