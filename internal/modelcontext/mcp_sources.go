package modelcontext

import (
	"encoding/json"
	"fmt"
	"html"
	"net/url"
	"regexp"
	"strings"
)

// MCP schemas and payloads remain opaque. This sidecar only indexes literal
// HTTP(S) links observed in a successful result; it never interprets external
// IDs as knowledge chunks or synthesizes URLs from article IDs.
const maxMCPSourceCandidates = 50

var (
	mcpJSONStringRE = regexp.MustCompile(`"(?:[^"\\]|\\.)*"`)
	mcpURLRE        = regexp.MustCompile("https?://[^\\s<>\"'`\\\\，。；！？、（）【】]+")
)

func (r *Registry) mcpSourceCandidates(output string) string {
	if !r.sources.citationsEnabled {
		return ""
	}
	// Decode JSON string escaping only in a scratch copy used to find links.
	// Both the external body and the durable ToolResult stay unchanged.
	scan := mcpJSONStringRE.ReplaceAllStringFunc(output, func(token string) string {
		var value string
		if json.Unmarshal([]byte(token), &value) == nil {
			return `"` + value + `"`
		}
		return token
	})
	seen := make(map[string]bool)
	var rows strings.Builder
	for _, match := range mcpURLRE.FindAllString(scan, -1) {
		rawURL := strings.TrimRight(html.UnescapeString(match), ".,;!?")
		// Strip Markdown/prose closing delimiters, retaining balanced URL
		// parentheses such as /wiki/Function_(mathematics).
		for _, pair := range [][2]string{{"(", ")"}, {"[", "]"}, {"{", "}"}} {
			for strings.HasSuffix(rawURL, pair[1]) && strings.Count(rawURL, pair[1]) > strings.Count(rawURL, pair[0]) {
				rawURL = strings.TrimSuffix(rawURL, pair[1])
			}
		}
		parsed, err := url.Parse(rawURL)
		if err != nil || parsed.Hostname() == "" || parsed.User != nil ||
			(parsed.Scheme != "https" && parsed.Scheme != "http") {
			continue
		}
		key := canonicalWebURL(rawURL)
		if seen[key] {
			continue
		}
		seen[key] = true
		handle := r.sources.RegisterWeb(rawURL, "")
		fmt.Fprintf(&rows, "<source id=\"%s\" url=\"%s\"/>\n", handle, escapeAttr(rawURL))
		if len(seen) >= maxMCPSourceCandidates {
			break
		}
	}
	if rows.Len() == 0 {
		return ""
	}
	return "\n\n<external_source_candidates>\n" +
		"System-indexed links from this MCP result. Cite the matching wN only when the result supports the claim; " +
		"a link alone does not mean the linked page was read. Do not use KB cN handles for this external content.\n" +
		rows.String() + "</external_source_candidates>"
}
