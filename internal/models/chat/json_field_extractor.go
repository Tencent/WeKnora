package chat

import (
	"strings"
	"unicode/utf8"
)

// jsonFieldExtractor extracts a specific string field value from streaming JSON fragments.
// It processes incremental JSON argument chunks from LLM tool calls.
//
// Example: for fieldName="answer", expected JSON format: {"answer":"...content..."}
// The extractor uses a simple state machine to skip the JSON prefix and extract the string value.
type jsonFieldExtractor struct {
	fieldName  string // the JSON field name to extract (e.g. "answer", "thought")
	buffer     string // accumulated full arguments string
	valueStart int    // byte offset where the field value starts (-1 if not found yet)
	lastEmit   int    // byte offset of the last emitted position within the value
	done       bool   // whether we've seen the closing quote
}

// newJSONFieldExtractor creates a new extractor instance for the given field name
func newJSONFieldExtractor(fieldName string) *jsonFieldExtractor {
	return &jsonFieldExtractor{
		fieldName:  fieldName,
		valueStart: -1,
		lastEmit:   0,
	}
}

// Feed processes a new argument delta and returns any new content to emit.
// Returns empty string if no new content is available yet.
func (e *jsonFieldExtractor) Feed(argsDelta string) string {
	if e.done {
		return ""
	}

	e.buffer += argsDelta

	// If we haven't found the value start yet, try to find it
	if e.valueStart < 0 {
		idx := findFieldValueStart(e.buffer, e.fieldName)
		if idx < 0 {
			return "" // Haven't seen the value start yet
		}
		e.valueStart = idx
		e.lastEmit = 0
	}

	// Extract new content from the value portion
	valueContent := e.buffer[e.valueStart:]

	// Find how far we can safely emit (stop before potential incomplete escape at the end)
	safeEnd, finished := findSafeEnd(valueContent, e.lastEmit)

	if safeEnd <= e.lastEmit {
		if finished {
			e.done = true
		}
		return ""
	}

	// Extract the new chunk and unescape JSON string escapes
	rawChunk := valueContent[e.lastEmit:safeEnd]
	unescaped := unescapeJSONString(rawChunk)

	e.lastEmit = safeEnd
	if finished {
		e.done = true
	}

	return unescaped
}

// IsDone returns whether the extractor has finished (closing quote found)
func (e *jsonFieldExtractor) IsDone() bool {
	return e.done
}

// findFieldValueStart finds the byte offset where the field's string value content begins
// (after the opening quote of the value). Returns -1 if not found.
func findFieldValueStart(buf string, fieldName string) int {
	// Look for "fieldName" key followed by colon and opening quote
	key := `"` + fieldName + `"`
	idx := strings.Index(buf, key)
	if idx < 0 {
		return -1
	}

	// Skip past the key
	pos := idx + len(key)

	// Skip whitespace and colon
	for pos < len(buf) {
		ch := buf[pos]
		if ch == ':' {
			pos++
			continue
		}
		if ch == ' ' || ch == '\t' || ch == '\n' || ch == '\r' {
			pos++
			continue
		}
		if ch == '"' {
			// Found the opening quote of the value
			return pos + 1
		}
		// Unexpected character
		return -1
	}

	return -1 // Haven't seen the opening quote yet
}

// findSafeEnd finds the safe end position for emission within the value content.
// It scans from lastEmit forward, handling escape sequences.
// Returns (safeEnd, finished) where finished=true if the closing quote was found.
func findSafeEnd(value string, from int) (int, bool) {
	i := from
	for i < len(value) {
		ch := value[i]
		if ch == '\\' {
			// Escape sequence - need at least 2 bytes
			if i+1 >= len(value) {
				// Incomplete escape at end, stop before it
				return i, false
			}
			nextCh := value[i+1]
			if nextCh == 'u' {
				// Unicode escape \uXXXX - need 6 bytes total
				if i+5 >= len(value) {
					return i, false
				}
				i += 6
			} else {
				// Simple escape: \", \\, \n, \t, \r, \/, \b, \f
				i += 2
			}
		} else if ch == '"' {
			// Closing quote of the JSON string value
			return i, true
		} else {
			// Regular character - handle multi-byte UTF-8
			_, size := utf8.DecodeRuneInString(value[i:])
			if size == 0 {
				size = 1
			}
			i += size
		}
	}
	return i, false
}

// unescapeJSONString converts JSON string escape sequences to their actual characters
func unescapeJSONString(s string) string {
	if !strings.ContainsRune(s, '\\') {
		return s
	}

	var b strings.Builder
	b.Grow(len(s))

	i := 0
	for i < len(s) {
		if s[i] == '\\' && i+1 < len(s) {
			switch s[i+1] {
			case '"':
				b.WriteByte('"')
				i += 2
			case '\\':
				b.WriteByte('\\')
				i += 2
			case '/':
				b.WriteByte('/')
				i += 2
			case 'n':
				b.WriteByte('\n')
				i += 2
			case 'r':
				b.WriteByte('\r')
				i += 2
			case 't':
				b.WriteByte('\t')
				i += 2
			case 'b':
				b.WriteByte('\b')
				i += 2
			case 'f':
				b.WriteByte('\f')
				i += 2
			case 'u':
				// Unicode escape \uXXXX
				if i+5 < len(s) {
					// Parse hex digits
					hexStr := s[i+2 : i+6]
					var codepoint int
					for _, h := range hexStr {
						codepoint <<= 4
						switch {
						case h >= '0' && h <= '9':
							codepoint += int(h - '0')
						case h >= 'a' && h <= 'f':
							codepoint += int(h-'a') + 10
						case h >= 'A' && h <= 'F':
							codepoint += int(h-'A') + 10
						}
					}
					b.WriteRune(rune(codepoint))
					i += 6
				} else {
					b.WriteByte(s[i])
					i++
				}
			default:
				b.WriteByte(s[i])
				i++
			}
		} else {
			b.WriteByte(s[i])
			i++
		}
	}

	return b.String()
}
