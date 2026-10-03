package datasource

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestSanitizeFileName(t *testing.T) {
	cases := []struct{ name, input, want string }{
		{"empty", "", "untitled"},
		{"punctuation", `a/b\c:d*e?f"g<h>i|j`, "a_b_c_d_e_f_g_h_i_j"},
		{"whitespace retained", " \t标题\n\r ", " \t标题\n\r "},
		{"dots retained", "..", ".."},
		{"controls retained", "a\x00b\x1fc", "a\x00b\x1fc"},
		{"ascii at limit", strings.Repeat("a", 200), strings.Repeat("a", 200)},
		{"ascii over limit", strings.Repeat("a", 201), strings.Repeat("a", 200)},
		{"CJK boundary", strings.Repeat("测", 67), strings.Repeat("测", 66)},
		{"two byte boundary", strings.Repeat("a", 199) + "é", strings.Repeat("a", 199)},
		{"emoji boundary", strings.Repeat("a", 198) + "🙂", strings.Repeat("a", 198)},
		{"complete emoji", strings.Repeat("🙂", 51), strings.Repeat("🙂", 50)},
		{"replacement rune", strings.Repeat("a", 197) + "�x", strings.Repeat("a", 197) + "�"},
		{"no extension reservation", strings.Repeat("a", 200) + ".pdf", strings.Repeat("a", 200)},
		{"malformed short input retained", "a\xff", "a\xff"},
		{"malformed interior retained", "\xff" + strings.Repeat("a", 200), "\xff" + strings.Repeat("a", 199)},
		{"malformed cut boundary removed", strings.Repeat("a", 199) + "\xffx", strings.Repeat("a", 199)},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			got := SanitizeFileName(tt.input)
			if got != tt.want {
				t.Fatalf("SanitizeFileName(%q) = %q, want %q", tt.input, got, tt.want)
			}
			if len(got) > 200 {
				t.Fatalf("result exceeds 200 bytes: %d", len(got))
			}
			if utf8.ValidString(tt.input) && !utf8.ValidString(got) {
				t.Fatalf("split a UTF-8 rune: %q", got)
			}
		})
	}
}
