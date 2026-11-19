package web_search

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// readErrorPreview stops on a byte count, so a Chinese error body can end
// mid-rune. Seven providers use it; the preview must stay valid UTF-8.
func TestReadErrorPreviewKeepsValidUTF8(t *testing.T) {
	preview := readErrorPreview(strings.NewReader(strings.Repeat("错", maxErrorPreviewBytes)))
	if !utf8.ValidString(preview) {
		t.Fatalf("preview is not valid UTF-8: %q", preview)
	}
	if len(preview) > maxErrorPreviewBytes {
		t.Fatalf("preview is %d bytes, want at most %d", len(preview), maxErrorPreviewBytes)
	}
	if preview == "" {
		t.Fatal("preview must keep the readable prefix")
	}
}

// trimPartialUTF8 must leave ordinary and already-valid text untouched.
func TestTrimPartialUTF8LeavesValidTextAlone(t *testing.T) {
	for _, s := range []string{"", "ok", "服务返回 500"} {
		if got := trimPartialUTF8(s); got != s {
			t.Fatalf("trimPartialUTF8(%q) = %q", s, got)
		}
	}
	if got := trimPartialUTF8("错\xe9"); got != "错" {
		t.Fatalf("trimPartialUTF8() = %q, want %q", got, "错")
	}
}
