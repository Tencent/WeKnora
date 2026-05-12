package text

import "github.com/mattn/go-runewidth"

const ellipsis = "…"

// Truncate cuts s to at most maxWidth display columns (CJK / emoji 占 2 列;
// ASCII 占 1 列), appending "…" if truncated. Display width, not rune count.
//
// Edge cases:
//
//	maxWidth ≤ 0 → ""
//	s 已 ≤ maxWidth → s 原样返回
//	ellipsis 占 1 列, 所以 truncate 时 budget = maxWidth - 1
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
