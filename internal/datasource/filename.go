package datasource

import (
	"strings"
	"unicode/utf8"
)

var fileNameReplacer = strings.NewReplacer(
	"/", "_", "\\", "_", ":", "_", "*", "_",
	"?", "_", "\"", "_", "<", "_", ">", "_", "|", "_",
)

// SanitizeFileName replaces filesystem-hostile punctuation and limits a source
// title to 200 bytes without splitting a UTF-8 rune. Empty titles use "untitled".
// Whitespace, control characters and extension handling remain the caller's policy;
// this helper does not validate filenames or repair malformed input.
func SanitizeFileName(name string) string {
	if name == "" {
		return "untitled"
	}
	result := fileNameReplacer.Replace(name)
	const maxBytes = 200
	if len(result) > maxBytes {
		result = result[:maxBytes]
		for len(result) > 0 {
			r, size := utf8.DecodeLastRuneInString(result)
			if r != utf8.RuneError || size != 1 {
				break
			}
			result = result[:len(result)-1]
		}
	}
	return result
}
