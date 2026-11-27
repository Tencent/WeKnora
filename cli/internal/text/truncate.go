package text

import "github.com/mattn/go-runewidth"

const ellipsis = "…"

// Truncate cuts s to at most maxWidth display columns (CJK / emoji occupy
// 2 columns; ASCII 1), appending "…" if truncated. Display width, not rune count.
//
// Edge cases:
//
//	maxWidth <= 0   -> ""
//	s already fits  -> s returned unchanged
//	ellipsis is 1 column, so truncate budget = maxWidth - 1
func Truncate(maxWidth int, s string) string {
	if maxWidth <= 0 {
		return ""
	}
	return runewidth.Truncate(s, maxWidth, ellipsis)
}
