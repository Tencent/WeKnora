package core

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// truncate caps a connector error preview with a plain byte slice. Connector error
// text is often Chinese, so the cut can land inside a multi-byte rune; these
// previews reach sync_logs and data_sources through the sync error message, and
// PostgreSQL rejects the invalid UTF-8 that a partial rune produces.
func TestTruncateKeepsValidUTF8AcrossCaps(t *testing.T) {
	body := strings.Repeat("错", 400)
	for _, maxLen := range []int{499, 500, 501, 1000, 1} {
		got := truncate(body, maxLen)

		if !utf8.ValidString(got) {
			t.Fatalf("%s(%d): preview is not valid UTF-8", "truncate", maxLen)
		}
		if len(got) > maxLen+len("...") {
			t.Fatalf("%s(%d): preview is %d bytes", "truncate", maxLen, len(got))
		}
		if !strings.HasPrefix(body, strings.TrimSuffix(got, "...")) {
			t.Fatalf("%s(%d): preview is not a prefix of the input", "truncate", maxLen)
		}
	}
}

func TestTruncateLeavesShortAndASCIIInputAlone(t *testing.T) {
	for _, s := range []string{"", "ok", "服务返回 500", "短"} {
		if got := truncate(s, 500); got != s {
			t.Fatalf("%s(%q, 500) = %q, want the input unchanged", "truncate", s, got)
		}
	}
	if got := truncate("abcdef", 3); got != "abc..." {
		t.Fatalf("truncate keeps the ASCII cut: %q", got)
	}
}
