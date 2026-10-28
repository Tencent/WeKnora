package api

import (
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

// JSONFieldExtractor extracts a specific string field value from streaming JSON fragments.
// It processes incremental JSON argument chunks from LLM tool calls.
//
// Example: for fieldName="answer", expected JSON format: {"answer":"...content..."}
// The extractor uses a simple state machine to skip the JSON prefix and extract the string value.
type JSONFieldExtractor struct {
	fieldName  string // the JSON field name to extract (e.g. "answer", "thought")
	buffer     string // accumulated full arguments string
	valueStart int    // byte offset where the field value starts (-1 if not found yet)
	lastEmit   int    // byte offset of the last emitted position within the value
	done       bool   // whether we've seen the closing quote
}

// NewJSONFieldExtractor creates a new extractor instance for the given field name
func NewJSONFieldExtractor(fieldName string) *JSONFieldExtractor {
	return &JSONFieldExtractor{
		fieldName:  fieldName,
		valueStart: -1,
		lastEmit:   0,
	}
}

// Feed processes a new argument delta and returns any new content to emit.
// Returns empty string if no new content is available yet.
func (e *JSONFieldExtractor) Feed(argsDelta string) string {
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
func (e *JSONFieldExtractor) IsDone() bool {
	return e.done
}

// findFieldValueStart finds the byte offset where the field's string value content begins
// (after the opening quote of the value). Returns -1 if not found.
func findFieldValueStart(buf string, fieldName string) int {
	key := `"` + fieldName + `"`
	for offset := 0; offset < len(buf); {
		idx := strings.Index(buf[offset:], key)
		if idx < 0 {
			return -1
		}
		pos := offset + idx + len(key)
		offset = pos
		for pos < len(buf) && strings.ContainsRune(" \t\n\r", rune(buf[pos])) {
			pos++
		}
		if pos >= len(buf) {
			return -1
		}
		if buf[pos] != ':' {
			continue
		}
		pos++
		for pos < len(buf) && strings.ContainsRune(" \t\n\r", rune(buf[pos])) {
			pos++
		}
		if pos >= len(buf) {
			return -1
		}
		if buf[pos] == '"' {
			return pos + 1
		}
	}
	return -1
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
				if isHighSurrogate(unicodeEscape(value[i+2 : i+6])) {
					rest := value[i+6:]
					// A high surrogate may pair with the next streamed escape.
					if rest == "" || rest == `\` || (strings.HasPrefix(rest, `\u`) && len(rest) < 6) {
						return i, false
					}
					if len(rest) >= 6 && strings.HasPrefix(rest, `\u`) && isLowSurrogate(unicodeEscape(rest[2:6])) {
						i += 12
						continue
					}
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
					codepoint := unicodeEscape(s[i+2 : i+6])
					if isHighSurrogate(codepoint) && i+11 < len(s) && s[i+6:i+8] == `\u` {
						low := unicodeEscape(s[i+8 : i+12])
						if isLowSurrogate(low) {
							b.WriteRune(utf16.DecodeRune(codepoint, low))
							i += 12
							continue
						}
					}
					b.WriteRune(codepoint)
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

func unicodeEscape(hex string) rune {
	var codepoint rune
	for _, h := range hex {
		codepoint <<= 4
		switch {
		case h >= '0' && h <= '9':
			codepoint += h - '0'
		case h >= 'a' && h <= 'f':
			codepoint += h - 'a' + 10
		case h >= 'A' && h <= 'F':
			codepoint += h - 'A' + 10
		}
	}
	return codepoint
}

func isHighSurrogate(r rune) bool { return r >= 0xd800 && r <= 0xdbff }
func isLowSurrogate(r rune) bool  { return r >= 0xdc00 && r <= 0xdfff }
