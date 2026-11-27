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

func TestConvertPageHTMLKeepsStrikethrough(t *testing.T) {
	// Data Center body.view renders the editor's strikethrough as <s>.
	page := `<p>套餐价格：<s>旧价格 4410 元已作废</s>，现行价格为 3990 元。</p>`
	cell := `<table><tr><th>项目</th></tr><tr><td>A</td></tr></table>`
	for name, input := range map[string]string{"page": page, "page with table": page + cell} {
		got, err := convertPageHTML(input)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(got, "套餐价格：~~旧价格 4410 元已作废~~，现行价格为 3990 元。") {
			t.Errorf("%s: strikethrough was lost:\n%s", name, got)
		}
	}
	got, err := convertPageHTML(`<p><del>删除</del> 与 <strike>作废</strike></p>`)
	if err != nil {
		t.Fatal(err)
	}
	if got != "~~删除~~ 与 ~~作废~~" {
		t.Errorf("unexpected Markdown for del/strike: %q", got)
	}
}

func TestConvertPageHTMLKeepsOnlyUsefulAttachmentMacroContent(t *testing.T) {
	input := `<p>Before</p><table class="attachments aui"><thead><tr><th>Sort by</th></tr></thead>
 <tbody><tr class="attachment-row" data-attachment-id="456">
 <td><a href="https://example.com/download/attachments/123/example.png?api=v2">example.png</a></td>
 <td>2 minutes ago</td></tr>
 <tr class="attachment-summary"><td>Edit Labels</td></tr></tbody></table>
 <table><tr><th>Qty</th><th>Total</th></tr><tr><td>2</td><td>3</td></tr></table><p>After</p>`
	got, err := convertPageHTML(input)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"Before", "[example.png](https://example.com/download/attachments/123/example.png?api=v2)",
		"| Qty", "After",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in %s", want, got)
		}
	}
	for _, unwanted := range []string{"Sort by", "2 minutes ago", "Edit Labels", "attachment-row"} {
		if strings.Contains(got, unwanted) {
			t.Errorf("unexpected %q in %s", unwanted, got)
		}
	}
}
