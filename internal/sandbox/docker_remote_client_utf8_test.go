package sandbox

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// firstNonEmptyLine output is embedded into SandboxError messages that reach
// agent tool results and API responses (see #4336 for the format in the
// wild). Trimming a stderr line by bytes must not split a multi-byte rune,
// or the surfaced error carries invalid UTF-8.
func TestFirstNonEmptyLineKeepsValidUTF8(t *testing.T) {
	// 80 CJK runes = 240 bytes; the 200-byte cut lands inside rune 67.
	cjk := firstNonEmptyLine(strings.Repeat("错", 80) + "\nsecond line")
	if !utf8.ValidString(cjk) {
		t.Fatalf("firstNonEmptyLine produced invalid UTF-8: %q", cjk)
	}
	if !strings.HasSuffix(cjk, "…") {
		t.Fatalf("expected truncation ellipsis: %q", cjk)
	}

	// Mixed widths: 99 CJK (297 bytes) + two-byte runes; the 200-byte cut
	// lands exactly on a two-byte rune boundary here, so both boundary and
	// mid-rune shapes stay valid.
	mixed := firstNonEmptyLine(strings.Repeat("错", 99) + strings.Repeat("é", 51))
	if !utf8.ValidString(mixed) {
		t.Fatalf("firstNonEmptyLine produced invalid UTF-8: %q", mixed)
	}

	if got := firstNonEmptyLine("short\nlines"); got != "short" {
		t.Fatalf("short lines must pass through unchanged, got %q", got)
	}
}
