package agent

// Rune budgets for the debug log previews below. They are counted in runes,
// not bytes, for the same reason tools.TruncateToolOutput is: a byte cap is a
// character cap in English and a syntax error in Chinese, where the 100th byte
// of a CJK string usually lands inside a three-byte character and the logged
// line ends in a replacement character.
const (
	// messagePreviewRunes caps the mid-conversation message preview.
	messagePreviewRunes = 100
	// llmContentPreviewRunes caps the per-round model content preview.
	llmContentPreviewRunes = 300
	// toolOutputPreviewRunes caps the tool result preview.
	toolOutputPreviewRunes = 500
)

// previewForLog caps s to at most n runes and appends marker when it cuts.
// Slicing on a rune boundary keeps the returned string valid UTF-8, which the
// previous byte slicing did not: 100 bytes into "文档解析失败" repeated is
// "解\xe6", and json-encoding that line replaces the tail with U+FFFD.
func previewForLog(s string, n int, marker string) string {
	if n <= 0 || len(s) <= n {
		// Byte length is never smaller than rune count, so a string that fits
		// in n bytes already fits in n runes.
		return s
	}
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n]) + marker
}
