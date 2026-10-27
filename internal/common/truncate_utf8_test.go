package common

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// Error messages are truncated before being persisted or returned to clients.
// A plain byte slice splits UTF-8 runes and leaves invalid text behind.
func TestTruncateUTF8BytesNeverSplitsARune(t *testing.T) {
	// 3 bytes per rune, so 1024 is not a rune boundary.
	msg := strings.Repeat("解析失败", 200)
	got := TruncateUTF8Bytes(msg, 1024)

	if !utf8.ValidString(got) {
		t.Fatalf("truncated message is not valid UTF-8: %q", got[max(0, len(got)-8):])
	}
	if len(got) > 1024 {
		t.Fatalf("truncated message is %d bytes, want at most 1024", len(got))
	}
	if !strings.HasPrefix(msg, got) {
		t.Fatal("truncation must keep a prefix of the original message")
	}
	if len(got) < 1020 {
		t.Fatalf("truncation dropped %d bytes, want at most 2", 1024-len(got))
	}
}

func TestTruncateUTF8BytesKeepsShortAndEmptyInput(t *testing.T) {
	for _, s := range []string{"", "ok", "解析失败", strings.Repeat("错", 341)} {
		if got := TruncateUTF8Bytes(s, 1024); got != s {
			t.Fatalf("TruncateUTF8Bytes(%q) = %q, want the input unchanged", s, got)
		}
	}
}

func TestTruncateUTF8BytesRejectsNonPositiveLimit(t *testing.T) {
	if got := TruncateUTF8Bytes("内容", 0); got != "" {
		t.Fatalf("limit 0 = %q, want an empty string", got)
	}
	if got := TruncateUTF8Bytes("内容", -1); got != "" {
		t.Fatalf("limit -1 = %q, want an empty string", got)
	}
}
