package line

import "testing"

// Table-driven checks for the non-Flex plain-text polish path. Each case is
// hand-traced against polishPlainText: blank lines collapse to a single gap,
// headings gain a 📌 marker, list markers become •, rules render via
// dividerRule(), and fenced blocks are wrapped in the 代碼區塊 label.
func TestPolishPlainText(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "inline bold lifts into brackets",
			in:   "This is **bold** text",
			want: "This is 【bold】 text",
		},
		{
			name: "multiple bold spans",
			in:   "**A** and **B**",
			want: "【A】 and 【B】",
		},
		{
			name: "backticks dropped",
			in:   "see `x := 1` here",
			want: "see x := 1 here",
		},
		{
			name: "heading gains marker",
			in:   "### Heading",
			want: "📌 Heading",
		},
		{
			name: "list bullets normalised",
			in:   "- one\n- two",
			want: "• one\n• two",
		},
		{
			name: "divider via dividerRule",
			in:   "a\n\n---\n\nb",
			want: "a\n\n" + dividerRule() + "\n\nb",
		},
		{
			name: "code block wrapped",
			in:   "```go\nline1\n```\nafter",
			want: "[代碼區塊]\nline1\n\nafter",
		},
		{
			name: "open code block flushed at EOF",
			in:   "```go\nline1",
			want: "[代碼區塊]\nline1",
		},
		{
			name: "plain text passes through",
			in:   "just text",
			want: "just text",
		},
		{
			name: "combined layout",
			in:   "## Title\n\n- item\n\ntext",
			want: "📌 Title\n\n• item\n\ntext",
		},
		{
			name: "markdown table to key-value list",
			in:   "| 規格 | 描述 |\n| --- | --- |\n| 處理器 | Intel Xeon |\n| 記憶體 | 64GB DDR5 |",
			want: "📊 【詳情】\n  • 規格：處理器\n    描述：Intel Xeon\n  • 規格：記憶體\n    描述：64GB DDR5",
		},
		{
			name: "three column table",
			in:   "| 產品 | 規格 | 價格 |\n| --- | --- | --- |\n| 手機 | 8GB | $20000 |",
			want: "📊 【詳情】\n  • 產品：手機\n    規格：8GB\n    價格：$20000",
		},
		{
			name: "escaped pipe in cell",
			in:   "| 欄位 | 說明 |\n| --- | --- |\n| 運算 | 1 \\| 2 相加 |",
			want: "📊 【詳情】\n  • 欄位：運算\n    說明：1 | 2 相加",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := polishPlainText(tt.in); got != tt.want {
				t.Fatalf("polishPlainText(%q)\n got: %q\nwant: %q", tt.in, got, tt.want)
			}
		})
	}
}
