package mattermost

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// truncateForErr caps response bodies at 512 bytes by slicing. Mattermost error
// bodies are localised, so the cut can land inside a multi-byte rune and the
// error string callers log and surface becomes invalid UTF-8.
func TestTruncateForErrKeepsValidUTF8(t *testing.T) {
	// 511 ASCII bytes put byte 512 inside the next three-byte rune.
	body := strings.Repeat("x", 511) + strings.Repeat("错误", 50)

	got := truncateForErr([]byte(body))

	if !utf8.ValidString(got) {
		t.Fatalf("preview is not valid UTF-8: %q", got)
	}
	if len(got) > 512+len("...") {
		t.Fatalf("preview is %d bytes", len(got))
	}
	if !strings.HasPrefix(body, strings.TrimSuffix(got, "...")) {
		t.Fatalf("preview is not a prefix of the input")
	}
}

func TestTruncateForErrLeavesShortInputAlone(t *testing.T) {
	for _, s := range []string{"", "ok", "服务返回 500"} {
		if got := truncateForErr([]byte(s)); got != s {
			t.Fatalf("truncateForErr(%q) = %q, want the input unchanged", s, got)
		}
	}
}
