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
//
// Returns the repaired JSON string. If repair is not possible,
// returns the original string unchanged (caller should handle parse errors).
func RepairJSON(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "{}"
	}

	// Must start with { for object
	if s[0] != '{' {
		// Maybe the LLM returned just key=value pairs without braces
		if strings.Contains(s, ":") || strings.Contains(s, "=") {
			s = "{" + s + "}"
		} else {
			return s
		}
	}

	// Fix trailing commas: ,} or ,]
	s = fixTrailingCommas(s)

	// Balance brackets and braces
	s = balanceBrackets(s)

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

// balanceBrackets appends missing closing brackets/braces.
func balanceBrackets(s string) string {
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

	// Close unclosed string if needed
	if inString {
		s += `"`
	}

	// Append missing closers in reverse order
	for i := len(stack) - 1; i >= 0; i-- {
		s += string(stack[i])
	}

	return s
}
