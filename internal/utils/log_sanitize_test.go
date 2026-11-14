package utils

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestCompactImageDataURLForLogCutsOnRuneBoundary(t *testing.T) {
	// One ASCII byte, then three-byte characters: byte 12000 falls inside the
	// 4,000th character.
	raw := "a" + strings.Repeat("问", 4001)
	got := CompactImageDataURLForLog(raw)
	if !utf8.ValidString(got) {
		t.Fatalf("truncated log is not valid UTF-8: %q", got[len(got)-60:])
	}
	kept, _, ok := strings.Cut(got, "... (truncated, total ")
	if !ok || kept != "a"+strings.Repeat("问", 3999) {
		t.Fatalf("want the 3,999 whole characters before the truncation note, got %d bytes", len(kept))
	}
}

func TestCompactImageDataURLForLogKeepsShortTextAndCompactsDataURLs(t *testing.T) {
	if got := CompactImageDataURLForLog("你好"); got != "你好" {
		t.Fatalf("short text changed: %q", got)
	}
	url := "data:image/png;base64," + strings.Repeat("A", 200)
	got := CompactImageDataURLForLog(`{"image":"` + url + `"}`)
	want := `{"image":"` + url[:defaultMaxDataURLPreview] + `...<omitted 126 chars>"}`
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}
