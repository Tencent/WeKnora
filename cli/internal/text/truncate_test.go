package text_test

import (
	"testing"

	"github.com/Tencent/WeKnora/cli/internal/text"
)

func TestTruncate(t *testing.T) {
	tests := []struct {
		name     string
		maxWidth int
		s        string
		want     string
	}{
		{"no truncate ASCII", 20, "hello", "hello"},
		{"truncate ASCII", 5, "hello world", "hell…"},
		{"no truncate CJK", 8, "中文测试", "中文测试"},
		{"truncate CJK", 5, "中文测试", "中文…"},
		{"empty", 5, "", ""},
		{"single char fits", 1, "a", "a"},
		{"truncate to width 1", 1, "abc", "…"},
		{"maxWidth zero", 0, "x", ""},
	}
	for _, tt := range tests {
		got := text.Truncate(tt.maxWidth, tt.s)
		if got != tt.want {
			t.Errorf("Truncate(%d, %q) = %q, want %q", tt.maxWidth, tt.s, got, tt.want)
		}
	}
}
