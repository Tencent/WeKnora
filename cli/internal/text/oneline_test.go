package text_test

import (
	"testing"

	"github.com/Tencent/WeKnora/cli/internal/text"
)

func TestOneLine(t *testing.T) {
	cases := []struct {
		name     string
		max      int
		in, want string
	}{
		{"empty", 10, "", ""},
		{"no-collapse", 10, "hello", "hello"},
		{"collapse-newline", 20, "hello\nworld", "hello world"},
		{"collapse-cr-tab", 20, "hello\r\tworld", "hello  world"},
		{"truncate", 8, "hello world long", "hello w…"},
		{"truncate-after-collapse", 8, "hello\nworld\nlong", "hello w…"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := text.OneLine(c.max, c.in); got != c.want {
				t.Errorf("OneLine(%d, %q) = %q, want %q", c.max, c.in, got, c.want)
			}
		})
	}
}
