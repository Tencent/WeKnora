package tools

import (
	"fmt"
	"regexp"
	"unicode"
)

// compilePortableCaseInsensitiveRegex limits database-backed regex tools to
// syntax shared by PostgreSQL ARE, MySQL ICU, and Go RE2. Engine-specific
// shorthand and inline constructs otherwise make validation and DB execution
// disagree across supported deployments.
func compilePortableCaseInsensitiveRegex(pattern string) (*regexp.Regexp, error) {
	if err := validatePortableRegex(pattern); err != nil {
		return nil, err
	}

	compiled, err := regexp.Compile("(?i:" + pattern + ")")
	if err != nil {
		return nil, fmt.Errorf("invalid portable regular expression %q: %w", pattern, err)
	}
	return compiled, nil
}

func validatePortableRegex(pattern string) error {
	runes := []rune(pattern)
	inCharacterClass := false

	for index := 0; index < len(runes); index++ {
		current := runes[index]
		if current == '\\' {
			if index+1 >= len(runes) {
				return fmt.Errorf("invalid portable regular expression %q: trailing backslash", pattern)
			}
			escaped := runes[index+1]
			if unicode.IsLetter(escaped) || unicode.IsDigit(escaped) {
				return fmt.Errorf(
					"non-portable regex escape \\%c in %q; use literals, character ranges, "+
						"grouping, alternation, anchors, and quantifiers",
					escaped,
					pattern,
				)
			}
			index++
			continue
		}

		switch current {
		case '[':
			inCharacterClass = true
		case ']':
			inCharacterClass = false
		case '(':
			if !inCharacterClass && index+1 < len(runes) && runes[index+1] == '?' {
				return fmt.Errorf(
					"non-portable regex construct \"(?\" in %q; inline flags, lookarounds, "+
						"and named groups are not supported",
					pattern,
				)
			}
		}
	}

	return nil
}
