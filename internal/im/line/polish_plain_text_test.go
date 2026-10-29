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
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := polishPlainText(tt.in); got != tt.want {
				t.Fatalf("polishPlainText(%q)\n got: %q\nwant: %q", tt.in, got, tt.want)
			}
		})
	}
}
