package chunker

import "strings"

// markdownFence tracks top-level backtick and tilde fences during line scans.
// It deliberately does not parse Markdown container blocks (lists/quotes).
type markdownFence struct {
	marker byte
	length int
}

// consume reports whether line belongs to fenced code, including delimiters.
// The second result identifies opening/closing delimiters, so the profiler can
// count code content without counting fence lines. An unclosed fence remains
// active through the end of the scan.
func (f *markdownFence) consume(line string) (code, delimiter bool) {
	inside := f.length > 0
	// Scanners split on LF; remove the line ending's CR for CRLF documents.
	line = strings.TrimSuffix(line, "\r")
	indent := 0
	for indent < len(line) && line[indent] == ' ' {
		indent++
	}
	if indent > 3 || indent == len(line) {
		return inside, false
	}
	line = line[indent:]
	marker := line[0]
	if marker != '`' && marker != '~' {
		return inside, false
	}
	length := 0
	for length < len(line) && line[length] == marker {
		length++
	}
	if length < 3 {
		return inside, false
	}
	rest := line[length:]
	if inside {
		if marker == f.marker && length >= f.length && strings.Trim(rest, " \t") == "" {
			*f = markdownFence{}
			return true, true
		}
		return true, false
	}
	// Backtick info strings cannot themselves contain backticks.
	if marker == '`' && strings.Contains(rest, "`") {
		return false, false
	}
	f.marker, f.length = marker, length
	return true, true
}
