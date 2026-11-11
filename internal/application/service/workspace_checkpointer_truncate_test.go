package service

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestTruncateForLogCutsOnRuneBoundary(t *testing.T) {
	// Two ASCII bytes, then three-byte characters: byte 256 falls inside the
	// 85th character.
	stderr := "ab" + strings.Repeat("问", 100)
	got := truncateForLog(stderr)
	if !utf8.ValidString(got) {
		t.Fatalf("truncated log is not valid UTF-8: %q", got)
	}
	if want := "ab" + strings.Repeat("问", 84) + "…"; got != want {
		t.Fatalf("want the 84 whole characters before the ellipsis, got %q", got)
	}
}

func TestTruncateForLogKeepsShortText(t *testing.T) {
	if got := truncateForLog("  致命错误: 不是 git 仓库  "); got != "致命错误: 不是 git 仓库" {
		t.Fatalf("short text changed: %q", got)
	}
}
