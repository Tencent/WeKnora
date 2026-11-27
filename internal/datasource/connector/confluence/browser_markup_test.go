package confluence

import "testing"

func TestConvertPageHTMLDropsDrawioMacroData(t *testing.T) {
	input := `<p>开始</p><div class="conf-macro output-block" data-macro-name="drawio">
<div class="drawio-macro" id="drawio-macro-content-1234"></div>
<div id="drawio-macro-data-1234" style="display:none">
eyJjcmVhdG9yTmFtZSI6IkV4YW1wbGUgVXNlciIsIm91dHB1dFR5cGUiOiJibG9jayJ9</div>
</div><p>结束</p>`
	got, err := convertPageHTML(input)
	if err != nil {
		t.Fatal(err)
	}
	if got != "开始\n\n结束" {
		t.Errorf("draw.io macro data leaked into Markdown: %q", got)
	}
}

func TestConvertPageHTMLDropsJiraLoadingPlaceholders(t *testing.T) {
	input := `<p>问题：<span class="jira-issue conf-macro output-block" data-jira-key="DEMO-1" data-macro-name="jira">
<a href="https://jira.example.com/browse/DEMO-1" class="jira-issue-key"><span
class="aui-icon aui-icon-wait issue-placeholder"></span>DEMO-1</a>
- <span class="summary">Getting issue details...</span>
<span class="aui-lozenge aui-lozenge-subtle aui-lozenge-default issue-placeholder">STATUS</span>
</span></p>
<div class="refresh-module-id jira-table conf-macro output-block" data-macro-name="jira">
<div class="refresh-macro"></div>
<div class="jira-issues"><table class="aui"><tbody><tr></tr>
<tr><th>Key</th><th>Summary</th><th>Status</th></tr></tbody></table></div>
<div class="refresh-issues-bottom"><span class="refresh-action-group">
<div class="button-spinner refresh-issues-loading"><span class="aui-icon aui-icon-wait">Loading...</span></div>
<a class="icon icon-refresh refresh-action refresh-issues-button" rel="nofollow"></a>
<a class="refresh-action refresh-issues-link" rel="nofollow">Refresh</a>
<input type="hidden" class="refresh-wiki" value="{jira:key = DEMO-1}"/>
</span></div>
</div><p>结束</p>`
	got, err := convertPageHTML(input)
	if err != nil {
		t.Fatal(err)
	}
	want := "问题：[DEMO-1](https://jira.example.com/browse/DEMO-1)\n\n结束"
	if got != want {
		t.Errorf("Jira placeholders were not removed:\ngot  %q\nwant %q", got, want)
	}
}

func TestConvertPageHTMLKeepsRenderedJiraIssue(t *testing.T) {
	input := `<p><span class="jira-issue" data-jira-key="DEMO-2">
<a href="https://jira.example.com/browse/DEMO-2" class="jira-issue-key">DEMO-2</a> -
<span class="summary">Fix login</span> <span class="aui-lozenge">DONE</span></span></p>`
	got, err := convertPageHTML(input)
	if err != nil {
		t.Fatal(err)
	}
	want := "[DEMO-2](https://jira.example.com/browse/DEMO-2) - Fix login DONE"
	if got != want {
		t.Errorf("rendered Jira issue changed:\ngot  %q\nwant %q", got, want)
	}
}
