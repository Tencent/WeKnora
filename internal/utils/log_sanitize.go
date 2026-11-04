package utils

import (
	"regexp"
	"strconv"
	"unicode/utf8"
)

var imageDataURLPatternForLog = regexp.MustCompile(`data:image\/[a-zA-Z0-9.+-]+;base64,[A-Za-z0-9+/=]+`)

const (
	defaultMaxLogChars       = 12000
	defaultMaxDataURLPreview = 96
)

// CompactImageDataURLForLog shortens large image data URLs for log output.
func CompactImageDataURLForLog(raw string) string {
	masked := imageDataURLPatternForLog.ReplaceAllStringFunc(raw, func(match string) string {
		if len(match) <= defaultMaxDataURLPreview {
			return match
		}
		hidden := len(match) - defaultMaxDataURLPreview
		return match[:defaultMaxDataURLPreview] + "...<omitted " + strconv.Itoa(hidden) + " chars>"
	})

	if len(masked) <= defaultMaxLogChars {
		return masked
	}
	// Back off to a rune boundary: request bodies are often Chinese, and a
	// partial rune leaves invalid UTF-8 in the log line.
	cut := defaultMaxLogChars
	for cut > 0 && !utf8.RuneStart(masked[cut]) {
		cut--
	}
	return masked[:cut] + "... (truncated, total " + strconv.Itoa(len(masked)) + " chars)"
}
