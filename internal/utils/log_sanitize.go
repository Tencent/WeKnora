package utils

import (
	"regexp"
	"strconv"
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
	return masked[:defaultMaxLogChars] + "... (truncated, total " + strconv.Itoa(len(masked)) + " chars)"
}
