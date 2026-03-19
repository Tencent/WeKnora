package tools

import (
	"fmt"
	"unicode/utf8"
)

const (
	// DefaultMaxToolOutput is the default maximum character (rune) count for tool output.
	// Outputs exceeding this limit are truncated with head + tail preservation.
	// This counts Unicode characters (runes), not bytes, so CJK text is treated fairly.
	DefaultMaxToolOutput = 16000

	// headRatio controls the head/tail split when truncating (70% head, 30% tail).
	headRatio = 0.7

	// truncationMarkerReserve is the rune budget reserved for the truncation marker itself.
	truncationMarkerReserve = 200
)

// TruncateToolOutput truncates tool output that exceeds maxChars runes, preserving
// the head (70%) and tail (30%) sections with a truncation marker in between.
// This prevents large tool results from consuming the entire LLM context window
// while keeping the most useful parts (headers/summaries at the start, conclusions at the end).
//
// maxChars is counted in Unicode characters (runes), not bytes, ensuring consistent
// behavior across English, Chinese, and other multi-byte character sets.
//
// If maxChars <= 0 or the output is within the limit, it is returned unchanged.
func TruncateToolOutput(output string, maxChars int) string {
	runeCount := utf8.RuneCountInString(output)
	if maxChars <= 0 || runeCount <= maxChars {
		return output
	}

	usable := maxChars - truncationMarkerReserve
	if usable <= 0 {
		return string([]rune(output)[:maxChars])
	}

	headSize := int(float64(usable) * headRatio)
	tailSize := usable - headSize
	if tailSize <= 0 {
		tailSize = 0
	}

	runes := []rune(output)
	marker := fmt.Sprintf(
		"\n\n... [output truncated: %d → %d chars, showing first %d + last %d] ...\n\n",
		runeCount, maxChars, headSize, tailSize,
	)

	if tailSize == 0 {
		return string(runes[:headSize]) + marker
	}
	return string(runes[:headSize]) + marker + string(runes[len(runes)-tailSize:])
}
