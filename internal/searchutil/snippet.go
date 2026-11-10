package searchutil

import (
	"regexp"
	"strings"
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

	matchStr := content[loc[0]:loc[1]]
	before := content[:loc[0]]
	after := content[loc[1]:]

	beforeRunes := []rune(before)
	if len(beforeRunes) > 60 {
		beforeRunes = beforeRunes[len(beforeRunes)-60:]
	}

	afterRunes := []rune(after)
	if len(afterRunes) > 60 {
		afterRunes = afterRunes[:60]
	}

	matchRunes := []rune(matchStr)
	if len(matchRunes) > 100 {
		matchRunes = append(matchRunes[:100], []rune("...")...)
	}

	snippet := string(beforeRunes) + string(matchRunes) + string(afterRunes)
	snippet = strings.ReplaceAll(snippet, "\n", " ")
	for strings.Contains(snippet, "  ") {
		snippet = strings.ReplaceAll(snippet, "  ", " ")
	}

	return "... " + strings.TrimSpace(snippet) + " ..."
}
