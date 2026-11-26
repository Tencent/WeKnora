package utils

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// TestSanitizeHTMLDoesNotSplitMultibyteRune reproduces the byte-truncation
// bug: SanitizeHTML cut the input at byte 10000, which can split a
// multi-byte rune and return invalid UTF-8.
func TestSanitizeHTMLDoesNotSplitMultibyteRune(t *testing.T) {
	// '€' is 3 bytes (0xE2 0x82 0xAC). It starts at byte 9999, so the
	// 10000-byte cut lands inside it.
	input := strings.Repeat("a", 9999) + "€" + strings.Repeat("b", 100)
	got := SanitizeHTML(input)
	if !utf8.ValidString(got) {
		t.Fatalf("SanitizeHTML returned invalid UTF-8 (last bytes %q)", got[len(got)-10:])
	}
	if len(got) > 10000 {
		t.Fatalf("got %d bytes, want <= 10000", len(got))
	}
	// The straddling rune must be dropped entirely, not half-kept.
	if want := strings.Repeat("a", 9999); got != want {
		t.Fatalf("got %d bytes, want exactly the 9999-byte 'a' prefix", len(got))
	}
}

func TestSanitizeHTMLTruncationBoundaries(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "rune ending exactly at the limit is kept",
			input: strings.Repeat("a", 9997) + "€" + strings.Repeat("b", 100),
			want:  strings.Repeat("a", 9997) + "€",
		},
		{
			name:  "four-byte emoji straddling the limit is dropped",
			input: strings.Repeat("a", 9998) + "😀" + strings.Repeat("b", 100),
			want:  strings.Repeat("a", 9998),
		},
		{
			name:  "ascii input is cut at exactly 10000 bytes",
			input: strings.Repeat("a", 15000),
			want:  strings.Repeat("a", 10000),
		},
		{
			name:  "short input is unchanged",
			input: "hello <b>world</b>",
			want:  "hello <b>world</b>",
		},
		{
			name:  "empty input stays empty",
			input: "",
			want:  "",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := SanitizeHTML(tc.input)
			if !utf8.ValidString(got) {
				t.Fatalf("SanitizeHTML returned invalid UTF-8")
			}
			if got != tc.want {
				t.Fatalf("got %d bytes, want %d bytes", len(got), len(tc.want))
			}
		})
	}
}

func TestSanitizeHTMLStillEscapesXSS(t *testing.T) {
	got := SanitizeHTML(`<script>alert(1)</script>`)
	if strings.Contains(got, "<script>") {
		t.Fatalf("XSS content was not escaped: %q", got)
	}
}
