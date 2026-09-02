package tools

import (
	"strings"
	"unicode"
)

// RepairJSON attempts to fix common JSON malformations from LLM outputs.
// LLMs sometimes produce:
//   - Truncated JSON (missing closing brackets/braces)
//   - Trailing commas before closing brackets
//   - Single quotes instead of double quotes
//   - Unquoted keys
//   - Invalid backslash escapes inside strings (e.g. regex patterns like
//     "C\+\+", "\d+", "\.log$") where the LLM forgot to double-escape.
//   - Extra tokens after the top-level value (e.g. `{}""`, `{"a":1}{"a":1}`)
//
// Returns the repaired JSON string. If repair is not possible,
// returns the original string unchanged (caller should handle parse errors).
func RepairJSON(s string) string {
	repaired, _ := RepairJSONDetail(s)
	return repaired
}

// RepairJSONDetail is RepairJSON that also reports whether the payload had to
// be closed off to parse — an unterminated string or an unbalanced bracket.
// That only happens when the provider stopped emitting mid-argument, so the
// values now parse but are partial: `content` holds half a file, `query` holds
// half a sentence. Callers must refuse such a call rather than run it.
// Escape and trailing-comma fixes are ordinary malformations, not truncation,
// and do not set the flag.
func RepairJSONDetail(s string) (string, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "{}", false
	}

	// Must start with { for object
	if s[0] != '{' {
		// Maybe the LLM returned just key=value pairs without braces
		if strings.Contains(s, ":") || strings.Contains(s, "=") {
			s = "{" + s + "}"
		} else {
			return s, false
		}
	}

	// Fix invalid backslash escapes before anything else, because unbalanced
	// strings confuse the comma/bracket trackers below.
	s = fixInvalidEscapes(s)

	// Drop anything after the first complete top-level value
	s = trimAfterTopLevelValue(s)

	// Fix trailing commas: ,} or ,]
	s = fixTrailingCommas(s)

	// Balance brackets and braces
	return balanceBrackets(s)
}

// fixInvalidEscapes turns invalid JSON string escapes into literal backslash
// sequences. JSON only allows the escape letters \" \\ \/ \b \f \n \r \t \u;
// any other backslash inside a string (e.g. \+ \d \w \. \| \( \)) is a parse
// error. LLMs frequently emit these when they meant to pass a regex pattern
// without double-escaping — rewriting "\+" to "\\+" recovers the LLM's intent
// (the unmarshalled Go string becomes "\+", which is what the regex engine
// expects) and is idempotent on already-valid JSON.
func fixInvalidEscapes(s string) string {
	var out strings.Builder
	out.Grow(len(s) + 8)
	runes := []rune(s)
	inString := false
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		if !inString {
			out.WriteRune(r)
			if r == '"' {
				inString = true
			}
			continue
		}
		if r == '"' {
			out.WriteRune(r)
			inString = false
			continue
		}
		if r != '\\' {
			out.WriteRune(r)
			continue
		}
		// Found a backslash inside a string. Peek at the next rune to decide
		// whether this is a valid JSON escape.
		if i+1 >= len(runes) {
			// Dangling backslash at EOF — escape it so we don't leave the
			// string in a "pending escape" state for downstream parsers.
			out.WriteString(`\\`)
			continue
		}
		next := runes[i+1]
		switch next {
		case '"', '\\', '/', 'b', 'f', 'n', 'r', 't', 'u':
			// Valid JSON escape — pass through unchanged.
			out.WriteRune(r)
			out.WriteRune(next)
			i++
		default:
			// Invalid escape — most likely a regex metachar that the LLM
			// forgot to double-escape. Emit a literal backslash + the next
			// rune, so the final Go string preserves the \X sequence.
			out.WriteString(`\\`)
			out.WriteRune(next)
			i++
		}
	}
	return out.String()
}

// trimAfterTopLevelValue cuts everything that follows the first balanced
// top-level object/array. Streaming providers occasionally emit a stray
// argument fragment after the arguments are already complete — a lone `""`
// or a repeat of the whole payload — and the concatenated result (`{}""`)
// fails to parse even though the leading value is perfectly good.
// Input that never closes its top-level value is returned unchanged so that
// balanceBrackets can still recover a truncated payload.
func trimAfterTopLevelValue(s string) string {
	depth := 0
	inString := false
	escaped := false

	for i, r := range s {
		if escaped {
			escaped = false
			continue
		}
		if inString {
			switch r {
			case '\\':
				escaped = true
			case '"':
				inString = false
			}
			continue
		}
		switch r {
		case '"':
			inString = true
		case '{', '[':
			depth++
		case '}', ']':
			depth--
			if depth == 0 {
				end := i + len(string(r))
				if strings.TrimSpace(s[end:]) == "" {
					return s
				}
				return s[:end]
			}
		}
	}
	return s
}

// fixTrailingCommas removes trailing commas before closing brackets/braces.
func fixTrailingCommas(s string) string {
	// Simple state machine to handle strings
	var result strings.Builder
	result.Grow(len(s))
	inString := false
	escaped := false
	runes := []rune(s)

	for i, r := range runes {
		if escaped {
			escaped = false
			result.WriteRune(r)
			continue
		}
		if r == '\\' && inString {
			escaped = true
			result.WriteRune(r)
			continue
		}
		if r == '"' {
			inString = !inString
			result.WriteRune(r)
			continue
		}
		if inString {
			result.WriteRune(r)
			continue
		}

		// Outside string: check for trailing comma
		if r == ',' {
			// Look ahead for closing bracket/brace (skipping whitespace)
			nextNonSpace := findNextNonSpace(runes, i+1)
			if nextNonSpace >= 0 && (runes[nextNonSpace] == '}' || runes[nextNonSpace] == ']') {
				continue // skip this comma
			}
		}
		result.WriteRune(r)
	}

	return result.String()
}

// findNextNonSpace finds the index of the next non-whitespace rune.
func findNextNonSpace(runes []rune, start int) int {
	for i := start; i < len(runes); i++ {
		if !unicode.IsSpace(runes[i]) {
			return i
		}
	}
	return -1
}

// balanceBrackets appends missing closing brackets/braces, reporting whether
// anything had to be appended.
func balanceBrackets(s string) (string, bool) {
	var stack []rune
	inString := false
	escaped := false

	for _, r := range s {
		if escaped {
			escaped = false
			continue
		}
		if r == '\\' && inString {
			escaped = true
			continue
		}
		if r == '"' {
			inString = !inString
			continue
		}
		if inString {
			continue
		}

		switch r {
		case '{':
			stack = append(stack, '}')
		case '[':
			stack = append(stack, ']')
		case '}', ']':
			if len(stack) > 0 && stack[len(stack)-1] == r {
				stack = stack[:len(stack)-1]
			}
		}
	}

	closed := false

	// Close unclosed string if needed
	if inString {
		s += `"`
		closed = true
	}

	// Append missing closers in reverse order
	for i := len(stack) - 1; i >= 0; i-- {
		s += string(stack[i])
		closed = true
	}

	return s, closed
}
