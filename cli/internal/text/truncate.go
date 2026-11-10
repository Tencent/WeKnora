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
	if runewidth.StringWidth(s) <= maxWidth {
		return s
	}
	if maxWidth == 1 {
		return ellipsis
	}
	budget := maxWidth - 1 // reserve for ellipsis
	w := 0
	for i, r := range s {
		rw := runewidth.RuneWidth(r)
		if w+rw > budget {
			return s[:i] + ellipsis
		}
		w += rw
	}
	return s + ellipsis
}
