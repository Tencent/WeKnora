package docparser

import (
	"strings"

	"github.com/Tencent/WeKnora/internal/types"
)

type markdownImageTargetSpan struct {
	TargetStart int
	TargetEnd   int
}

func scanMarkdownImageTargets(markdown string) []markdownImageTargetSpan {
	var spans []markdownImageTargetSpan
	for i := 0; i+1 < len(markdown); i++ {
		if markdown[i] != '!' || markdown[i+1] != '[' || isEscaped(markdown, i) {
			continue
		}

		altEnd := findMarkdownImageAltEnd(markdown, i+2)
		if altEnd == -1 {
			continue
		}

		targetStart := altEnd + 2
		targetEnd, ok := findMarkdownImageTargetEnd(markdown, targetStart)
		if !ok {
			i = altEnd
			continue
		}
		spans = append(spans, markdownImageTargetSpan{
			TargetStart: targetStart,
			TargetEnd:   targetEnd,
		})
		i = targetEnd
	}
	return spans
}

func findMarkdownImageAltEnd(markdown string, start int) int {
	for i := start; i+1 < len(markdown); i++ {
		if markdown[i] == ']' && markdown[i+1] == '(' && !isEscaped(markdown, i) {
			return i
		}
	}
	return -1
}

func findMarkdownImageTargetEnd(markdown string, start int) (int, bool) {
	parenDepth := 1
	inAngleDestination := false
	seenNonSpace := false
	var inQuote byte

	for i := start; i < len(markdown); i++ {
		ch := markdown[i]
		if ch == '\\' {
			i++
			continue
		}

		if !seenNonSpace && !isMarkdownSpace(ch) {
			seenNonSpace = true
			if ch == '<' {
				inAngleDestination = true
				continue
			}
		}

		if inAngleDestination {
			if ch == '>' {
				inAngleDestination = false
			}
			continue
		}

		if inQuote != 0 {
			if ch == inQuote {
				inQuote = 0
			}
			continue
		}

		if (ch == '"' || ch == '\'') && i > start && isMarkdownSpace(markdown[i-1]) {
			inQuote = ch
			continue
		}

		switch ch {
		case '(':
			parenDepth++
		case ')':
			parenDepth--
			if parenDepth == 0 {
				return i, true
			}
		}
	}
	return 0, false
}

func splitMarkdownImageTarget(
	raw string,
	refMap map[string]types.ImageRef,
) (path string, pathStart int, pathEnd int, ok bool) {
	start, end := trimMarkdownSpaceBounds(raw, 0, len(raw))
	if start >= end {
		return "", 0, 0, false
	}

	trimmed := raw[start:end]
	if _, found := refMap[trimmed]; found {
		return trimmed, start, end, true
	}

	if raw[start] == '<' {
		return splitAngleMarkdownImageTarget(raw, start, end, refMap)
	}

	titleStart, found := parseMarkdownImageTitleSuffix(trimmed)
	if !found {
		return "", 0, 0, false
	}

	pathEnd = start + titleStart
	for pathEnd > start && isMarkdownSpace(raw[pathEnd-1]) {
		pathEnd--
	}
	if pathEnd == start {
		return "", 0, 0, false
	}

	path = raw[start:pathEnd]
	if _, found := refMap[path]; !found {
		return "", 0, 0, false
	}
	return path, start, pathEnd, true
}

func splitAngleMarkdownImageTarget(
	raw string,
	start int,
	end int,
	refMap map[string]types.ImageRef,
) (path string, pathStart int, pathEnd int, ok bool) {
	closeIdx := -1
	for i := start + 1; i < end; i++ {
		if raw[i] == '>' && !isEscaped(raw, i) {
			closeIdx = i
			break
		}
	}
	if closeIdx == -1 {
		return "", 0, 0, false
	}

	path = raw[start+1 : closeIdx]
	if _, found := refMap[path]; !found {
		return "", 0, 0, false
	}
	if !isEmptyOrMarkdownImageTitleSuffix(raw[closeIdx+1 : end]) {
		return "", 0, 0, false
	}
	return path, start + 1, closeIdx, true
}

func parseMarkdownImageTitleSuffix(raw string) (titleStart int, ok bool) {
	_, end := trimMarkdownSpaceBounds(raw, 0, len(raw))
	if end == 0 {
		return 0, false
	}

	switch raw[end-1] {
	case '"', '\'':
		quote := raw[end-1]
		for i := end - 2; i >= 0; i-- {
			if raw[i] != quote || isEscaped(raw, i) {
				continue
			}
			if i == 0 || !isMarkdownSpace(raw[i-1]) {
				return 0, false
			}
			if markdownTitleHasBlankLine(raw[i+1 : end-1]) {
				return 0, false
			}
			return i, true
		}
	case ')':
		depth := 0
		for i := end - 2; i >= 0; i-- {
			if isEscaped(raw, i) {
				continue
			}
			switch raw[i] {
			case ')':
				depth++
			case '(':
				if depth == 0 {
					if i == 0 || !isMarkdownSpace(raw[i-1]) {
						return 0, false
					}
					if markdownTitleHasBlankLine(raw[i+1 : end-1]) {
						return 0, false
					}
					return i, true
				}
				depth--
			}
		}
	}
	return 0, false
}

func isEmptyOrMarkdownImageTitleSuffix(raw string) bool {
	start, end := trimMarkdownSpaceBounds(raw, 0, len(raw))
	if start == end {
		return true
	}
	titleStart, ok := parseMarkdownImageTitleSuffix(raw[:end])
	return ok && isAllMarkdownSpace(raw[:titleStart])
}

func markdownTitleHasBlankLine(title string) bool {
	for _, line := range strings.Split(title, "\n") {
		if strings.Trim(line, " \t") == "" {
			return true
		}
	}
	return false
}

func isAllMarkdownSpace(raw string) bool {
	for i := 0; i < len(raw); i++ {
		if !isMarkdownSpace(raw[i]) {
			return false
		}
	}
	return true
}

func trimMarkdownSpaceBounds(raw string, start int, end int) (int, int) {
	for start < end && isMarkdownSpace(raw[start]) {
		start++
	}
	for end > start && isMarkdownSpace(raw[end-1]) {
		end--
	}
	return start, end
}

func isMarkdownSpace(b byte) bool {
	return b == ' ' || b == '\t' || b == '\n' || b == '\r'
}

func isEscaped(s string, pos int) bool {
	backslashes := 0
	for i := pos - 1; i >= 0 && s[i] == '\\'; i-- {
		backslashes++
	}
	return backslashes%2 == 1
}
