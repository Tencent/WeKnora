package service

import (
	"fmt"
	"regexp"
	"strings"
	"testing"
)

func TestSanitizeOCRText(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "empty string",
			input: "",
			want:  "",
		},
		{
			name:  "whitespace only",
			input: "   \n\t  ",
			want:  "",
		},
		{
			name:  "pure HTML skeleton with no text",
			input: `<html><body><div class="image"><img/></div></body></html>`,
			want:  "",
		},
		{
			name:  "HTML with only whitespace text",
			input: "<html><body>  \n  </body></html>",
			want:  "",
		},
		{
			name:  "valid markdown passes through",
			input: "# 标题\n\n这是一段正文，包含一些内容。\n\n| 列1 | 列2 |\n| --- | --- |\n| 数据1 | 数据2 |",
			want:  "# 标题\n\n这是一段正文，包含一些内容。\n\n| 列1 | 列2 |\n| --- | --- |\n| 数据1 | 数据2 |",
		},
		{
			name:  "code block wrapper stripped",
			input: "```markdown\n# 文档标题\n\n正文内容在这里。\n```",
			want:  "# 文档标题\n\n正文内容在这里。",
		},
		{
			name:  "html code block wrapper stripped",
			input: "```html\n<p>这是一段内容</p>\n```",
			want:  "这是一段内容",
		},
		{
			name:  "HTML document converted to markdown",
			input: "<html><body><h1>标题</h1><p>这是一段很长的正文内容，用来测试 HTML 到 Markdown 的转换。</p></body></html>",
			want:  "# 标题\n\n这是一段很长的正文内容，用来测试 HTML 到 Markdown 的转换。",
		},
		{
			name:  "known empty reply - Chinese",
			input: "无文字内容",
			want:  "",
		},
		{
			name:  "known empty reply - no text",
			input: "No text",
			want:  "",
		},
		{
			name:  "known empty reply - 图片中没有文字",
			input: "图片中没有文字",
			want:  "",
		},
		{
			name:  "plain text with minimal HTML not converted",
			input: "这是一段正常文本，价格 <100 元。",
			want:  "这是一段正常文本，价格 <100 元。",
		},
		{
			name:  "multiple blank lines collapsed",
			input: "段落一\n\n\n\n\n段落二",
			want:  "段落一\n\n段落二",
		},
		{
			name:  "HTML with substantial text content is converted",
			input: "<div><h2>报告摘要</h2><p>本季度营收同比增长 15%，净利润达到 2.3 亿元。</p><table><tr><th>指标</th><th>数值</th></tr><tr><td>营收</td><td>10亿</td></tr></table></div>",
			want:  "", // placeholder; will be checked for non-empty
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := sanitizeOCRText(tt.input)

			if tt.name == "HTML with substantial text content is converted" {
				if got == "" {
					t.Errorf("sanitizeOCRText() returned empty for substantial HTML content")
				}
				if got == tt.input {
					t.Errorf("sanitizeOCRText() did not convert HTML, got original")
				}
				return
			}

			if got != tt.want {
				t.Errorf("sanitizeOCRText() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestStripMarkdownCodeBlock(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "no code block",
			input: "just normal text",
			want:  "just normal text",
		},
		{
			name:  "markdown code block",
			input: "```markdown\n# Title\nContent here\n```",
			want:  "# Title\nContent here",
		},
		{
			name:  "html code block",
			input: "```html\n<p>hello</p>\n```",
			want:  "<p>hello</p>",
		},
		{
			name:  "plain code block",
			input: "```\nsome text\n```",
			want:  "some text",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := stripMarkdownCodeBlock(tt.input)
			if got != tt.want {
				t.Errorf("stripMarkdownCodeBlock() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestLooksLikeHTML(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  bool
	}{
		{
			name:  "HTML document",
			input: "<html><body><p>text</p></body></html>",
			want:  true,
		},
		{
			name:  "DOCTYPE",
			input: "<!DOCTYPE html><html><body></body></html>",
			want:  true,
		},
		{
			name:  "body tag",
			input: "<body><p>content</p></body>",
			want:  true,
		},
		{
			name:  "plain markdown",
			input: "# Title\n\nSome paragraph text",
			want:  false,
		},
		{
			name:  "text with minor HTML",
			input: "This is mostly text with a <b>bold</b> word.",
			want:  false,
		},
		{
			name:  "heavy HTML tags",
			input: "<div><p><span>x</span></p></div><div><p><span>y</span></p></div>",
			want:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := looksLikeHTML(tt.input)
			if got != tt.want {
				t.Errorf("looksLikeHTML() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestSanitizeOCRText_ConvertsInlineHTMLTable guards against a regression where
// an HTML <table> embedded in a markdown body survived sanitization untouched:
// the mixed content does not satisfy looksLikeHTML, so the old HTML-to-markdown
// path never ran and the raw <table> markup reached the chunker.
func TestSanitizeOCRText_ConvertsInlineHTMLTable(t *testing.T) {
	para := "本季度公司整体经营情况保持稳定，营收与利润两项核心指标均实现同比增长。" +
		"为了便于管理层快速了解经营成果，下表汇总了报告期内的主要财务数据，" +
		"供后续经营分析、预算编制以及年度考核等工作参考使用，" +
		"请结合实际业务情况综合判断，切勿脱离业务背景单独解读其中任何一项数字。"
	tail := "以上数据均来自财务部门审核后的正式报表，统计口径与上一报告期保持一致，" +
		"未发生会计政策变更或追溯调整。如有疑问请与财务部门联系确认，" +
		"最终解释权归公司财务部门所有。"
	input := "# 报告\n\n" + para + "\n\n" +
		`<table><tr><th>指标</th><th>数值</th></tr>` +
		`<tr><td>营收</td><td>10亿</td></tr>` +
		`<tr><td>利润</td><td>2.3亿</td></tr></table>` +
		"\n\n" + tail + "\n\n"

	if looksLikeHTML(input) {
		t.Fatalf("test input unexpectedly looks like HTML; the regression test must exercise the mixed-content path")
	}

	got := sanitizeOCRText(input)

	if strings.Contains(got, "<table") {
		t.Fatalf("expected inline HTML table to be converted, got:\n%s", got)
	}
	if !strings.Contains(got, "# 报告") || !strings.Contains(got, "最终解释权归公司财务部门所有。") {
		t.Fatalf("expected surrounding markdown to be preserved, got:\n%s", got)
	}
	for _, want := range []string{"指标", "数值", "营收", "10亿", "利润", "2.3亿"} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected converted table to retain %q, got:\n%s", want, got)
		}
	}
	if !regexp.MustCompile(`\|[-:]{3,}\|`).MatchString(got) {
		t.Fatalf("expected a GFM table separator row, got:\n%s", got)
	}
}

func TestIsKnownEmptyReply(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{"无文字内容", true},
		{"无法识别", true},
		{"no text", true},
		{"No Text", true},
		{"NO CONTENT", true},
		{"empty", true},
		{"这是正常内容", false},
		{"", false},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := isKnownEmptyReply(tt.input)
			if got != tt.want {
				t.Errorf("isKnownEmptyReply(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestValidateOCRTextRejectsDegenerateOutput(t *testing.T) {
	for _, tt := range []struct {
		name, input, reason string
	}{
		{"recorded empty cells", strings.Repeat("|  ", 2500), "no_readable_content"},
		{"empty response", " \n\t", "empty_content"},
		{"markdown skeleton", "| --- | --- |\n| | |", "no_readable_content"},
		{"repeated phrase", strings.Repeat("传球按钮 ", 200), "repetitive_content"},
		{"repeated suffix after real text", "传球\n" + strings.Repeat("|  ", 2500), "repetitive_content"},
		{
			"split delimiter loops", strings.Repeat("|  ", 1250) + "传球\n" + strings.Repeat("|  ", 1250),
			"repetitive_content",
		},
		{
			"different repeated periods", strings.Repeat("| ", 600) + "传球\n" + strings.Repeat("取消施法 ", 240),
			"repetitive_content",
		},
		{"repeated wrapped lines", strings.Repeat("开始\n锁定目标\n", 100), "repetitive_content"},
		{"empty HTML skeleton", "<html><body><div><img/></div></body></html>", "empty_content"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			text, err := validateOCRText(tt.input)
			invalid, ok := err.(*ocrValidationError)
			if text != "" || !ok || invalid.reason != tt.reason {
				t.Fatalf("text=%q error=%v; want rejection %s", text, err, tt.reason)
			}
			if got := sanitizeOCRText(tt.input); got != "" {
				t.Fatal("shared sanitizer must also discard rejected OCR")
			}
		})
	}
}

func TestValidateOCRTextPreservesRealContent(t *testing.T) {
	var table strings.Builder
	table.WriteString("| 项目 | 数值 |\n| --- | --- |\n")
	for i := 0; i < 100; i++ {
		fmt.Fprintf(&table, "| 项目%d | %d |\n", i, i)
	}
	for _, input := range []string{
		"A", "3", "传球", "A\nB\nL1\nL2\n传球\n传球", "0:0\n58.29\n取消施法", "±", "$x^2 + y^2 = z^2$",
		table.String(), strings.Repeat("同一个按钮\n", 5),
		// A run matches many periods; its overlapping ranges must count once.
		table.String() + strings.Repeat("X", 600),
	} {
		got, err := validateOCRText(input)
		if err != nil || got != strings.TrimSpace(input) {
			t.Fatalf("valid OCR rejected or changed: input=%q got=%q err=%v", input, got, err)
		}
	}
	for _, input := range []string{
		"No text content.", "无文字内容", "```\nNo text content.\n```",
		"<p>No text content.</p>", "<html><body>No text content.</body></html>",
		"<p><strong>No text content.</strong></p>",
		"```html\n<p>No text content.</p>\n```", "<p>无文字内容</p>",
		"<p>empty</p>", "<p>no text</p>", "<p>No text content&#46;</p>",
	} {
		if got, err := validateOCRText(input); err != nil || got != "" {
			t.Fatalf("explicit no-text answer should succeed without text: %q %v", got, err)
		}
	}
}
