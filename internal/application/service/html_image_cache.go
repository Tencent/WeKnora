package service

import (
	"regexp"
	"strings"
)

// Consume quoted attributes as a whole so a title containing src= or > cannot
// be mistaken for the image destination or the end of the tag.
var (
	htmlCacheImageTagRE  = regexp.MustCompile(`(?i)<img(?:\s(?:[^"'<>]|"[^"]*"|'[^']*')*)?/?>`)
	htmlCacheAttributeRE = regexp.MustCompile(
		`(\s+)([^\s"'<>/=]+)(\s*=\s*)("[^"]*"|'[^']*'|[^\s"'=` + "`" + `<>]+)`,
	)
)

func rewriteHTMLCacheImageSources(s string, rewrite func(string) string) string {
	return htmlCacheImageTagRE.ReplaceAllStringFunc(s, func(tag string) string {
		return htmlCacheAttributeRE.ReplaceAllStringFunc(tag, func(attribute string) string {
			parts := htmlCacheAttributeRE.FindStringSubmatchIndex(attribute)
			if !strings.EqualFold(attribute[parts[4]:parts[5]], "src") {
				return attribute
			}
			start, end := parts[8], parts[9]
			if attribute[start] == '"' || attribute[start] == '\'' {
				start++
				end--
			}
			return attribute[:start] + rewrite(attribute[start:end]) + attribute[end:]
		})
	})
}
