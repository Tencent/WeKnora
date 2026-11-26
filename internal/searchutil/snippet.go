package searchutil

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

// ExtractSnippet matches Agent wiki_search: ~60 runes of context around the
// first case-insensitive regex hit, match itself capped at 100. Invalid
// patterns return an empty string.
func ExtractSnippet(content string, query string) string {
	if content == "" || query == "" {
		return ""
	}
	re, err := regexp.Compile("(?i)" + query)
	if err != nil {
		return ""
	}
	loc := re.FindStringIndex(content)
	if loc == nil {
		return ""
	}

	// Walk only the visible windows, even when the match sits in a large page.
	start := loc[0]
	for range 60 {
		if start == 0 {
			break
		}
		_, size := utf8.DecodeLastRuneInString(content[:start])
		start -= size
	}
	end := runePrefixEnd(content[loc[1]:], 60) + loc[1]

	match := content[loc[0]:loc[1]]
	snippet := content[start:end]
	if matchEnd := runePrefixEnd(match, 100); matchEnd < len(match) {
		snippet = content[start:loc[0]+matchEnd] + "..." + content[loc[1]:end]
	}
	// Preserve the previous rune conversion for malformed UTF-8 in the window.
	if !utf8.ValidString(snippet) {
		snippet = string([]rune(snippet))
	}

	snippet = strings.ReplaceAll(snippet, "\n", " ")
	for strings.Contains(snippet, "  ") {
		snippet = strings.ReplaceAll(snippet, "  ", " ")
	}

	return "... " + strings.TrimSpace(snippet) + " ..."
}

func runePrefixEnd(text string, limit int) int {
	for offset := range text {
		if limit == 0 {
			return offset
		}
		limit--
	}
	return len(text)
}
