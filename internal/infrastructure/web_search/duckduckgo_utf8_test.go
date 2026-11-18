package web_search

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// extractTitle caps the first line of a DuckDuckGo RelatedTopics/Results text at
// 100 bytes. Those texts carry page titles, which are often Chinese, so the cut
// can land inside a multi-byte rune; the truncated title then becomes a
// WebSearchResult.Title, and the search results are indexed into a knowledge
// base whose title column PostgreSQL validates as UTF-8 (the same rule #4555 and
// #4583 applied elsewhere).
func TestExtractTitleKeepsValidUTF8(t *testing.T) {
	// 99 ASCII bytes put byte 100 inside the next three-byte rune.
	for _, text := range []string{
		strings.Repeat("错", 200),
		strings.Repeat("x", 99) + strings.Repeat("错误", 50),
		strings.Repeat("x", 98) + strings.Repeat("错误", 50),
	} {
		got := extractTitle(text)

		if !utf8.ValidString(got) {
			t.Fatalf("extractTitle: title is not valid UTF-8 for input %d bytes", len(text))
		}
		if len(got) > 100+len("...") {
			t.Fatalf("extractTitle: title is %d bytes, want at most %d", len(got), 100+len("..."))
		}
		if !strings.HasPrefix(text, strings.TrimSuffix(got, "...")) {
			t.Fatalf("extractTitle: title is not a prefix of the input")
		}
	}
}

// A long first line is what gets capped; the cap must not change behavior for
// text that already fits, for ASCII, or for text whose first line is empty.
func TestExtractTitleLeavesShortInputAlone(t *testing.T) {
	for _, text := range []string{
		"",
		"ok",
		"服务返回 500",
		strings.Repeat("x", 100),
		strings.Repeat("错", 33), // 99 bytes: under the cap
	} {
		if got := extractTitle(text); got != strings.TrimSpace(text) {
			t.Fatalf("extractTitle(%q) = %q, want the input unchanged", text, got)
		}
	}
}

// Only the first line becomes the title, before the cap applies.
func TestExtractTitleUsesOnlyTheFirstLine(t *testing.T) {
	got := extractTitle("Title line\nSnippet line")

	if got != "Title line" {
		t.Fatalf("extractTitle = %q, want %q", got, "Title line")
	}
}

// A title that is exactly at the cap is not truncated, so no ellipsis is added.
func TestExtractTitleAtTheCapIsNotTruncated(t *testing.T) {
	text := strings.Repeat("a", 100)

	if got := extractTitle(text); got != text {
		t.Fatalf("extractTitle(100 ASCII bytes) = %q, want it unchanged", got)
	}
}
