package im

import (
	"fmt"
	"html"
	"net/url"
	"regexp"
	"strings"

	"github.com/Tencent/WeKnora/internal/types"
)

var imCitationAttributeRe = regexp.MustCompile(`(?i)\b([a-z][a-z0-9_-]*)\s*=\s*"([^"]*)"`)

// BuildReplyCitations extracts the sources explicitly cited in an answer.
//
// Knowledge references are emitted before the model answer and may include
// retrieved chunks that the model did not use. Restricting the result to the
// canonical <kb/> / <web/> tags keeps IM replies aligned with the sources the
// answer actually cites instead of dumping every retrieved result.
func BuildReplyCitations(answer string, refs []*types.SearchResult) []ReplyCitation {
	if strings.TrimSpace(answer) == "" {
		return nil
	}

	refsByChunkID := make(map[string]*types.SearchResult, len(refs))
	refsByKnowledgeID := make(map[string][]*types.SearchResult)
	for _, ref := range refs {
		if ref == nil {
			continue
		}
		if id := strings.TrimSpace(ref.ID); id != "" {
			refsByChunkID[id] = ref
		}
		if id := strings.TrimSpace(ref.KnowledgeID); id != "" {
			refsByKnowledgeID[id] = append(refsByKnowledgeID[id], ref)
		}
	}

	var citations []ReplyCitation
	seenURLs := make(map[string]struct{})
	for _, tag := range imCitationTagRe.FindAllString(answer, -1) {
		attrs := parseIMCitationAttributes(tag)
		name := strings.ToLower(strings.TrimSpace(tagName(tag)))

		var (
			citationURL string
			title       string
		)
		switch name {
		case "web":
			citationURL = attrs["url"]
			title = attrs["title"]
		case "kb":
			ref := matchIMKnowledgeReference(attrs, refsByChunkID, refsByKnowledgeID)
			if ref == nil {
				continue
			}
			citationURL = ref.Metadata[types.MetadataKeySourceURL]
			title = firstNonEmptyIMString(ref.KnowledgeTitle, ref.KnowledgeFilename, attrs["doc"])
		default:
			continue
		}

		citationURL = normalizeIMCitationURL(citationURL)
		if citationURL == "" {
			continue
		}
		if _, seen := seenURLs[citationURL]; seen {
			continue
		}
		seenURLs[citationURL] = struct{}{}
		if strings.TrimSpace(title) == "" {
			title = citationURL
		}
		citations = append(citations, ReplyCitation{
			Label: fmt.Sprintf("S%d", len(citations)+1),
			Title: strings.TrimSpace(html.UnescapeString(title)),
			URL:   citationURL,
		})
	}
	return citations
}

func parseIMCitationAttributes(tag string) map[string]string {
	attrs := make(map[string]string)
	for _, match := range imCitationAttributeRe.FindAllStringSubmatch(tag, -1) {
		if len(match) != 3 {
			continue
		}
		attrs[strings.ToLower(match[1])] = html.UnescapeString(strings.TrimSpace(match[2]))
	}
	return attrs
}

func tagName(tag string) string {
	trimmed := strings.TrimSpace(strings.TrimPrefix(tag, "<"))
	if idx := strings.IndexAny(trimmed, " \t\r\n>"); idx >= 0 {
		return trimmed[:idx]
	}
	return trimmed
}

func matchIMKnowledgeReference(
	attrs map[string]string,
	refsByChunkID map[string]*types.SearchResult,
	refsByKnowledgeID map[string][]*types.SearchResult,
) *types.SearchResult {
	for _, key := range []string{"chunk_id", "chunkid", "id"} {
		if ref := refsByChunkID[strings.TrimSpace(attrs[key])]; ref != nil {
			return ref
		}
	}

	knowledgeID := strings.TrimSpace(firstNonEmptyIMString(attrs["knowledge_id"], attrs["knowledgeid"]))
	if knowledgeID != "" {
		matches := refsByKnowledgeID[knowledgeID]
		if len(matches) == 1 {
			return matches[0]
		}
	}

	doc := strings.TrimSpace(attrs["doc"])
	if doc == "" {
		return nil
	}
	var match *types.SearchResult
	for _, ref := range refsByChunkID {
		if !sameIMCitationTitle(doc, ref.KnowledgeTitle) && !sameIMCitationTitle(doc, ref.KnowledgeFilename) {
			continue
		}
		if match != nil {
			return nil
		}
		match = ref
	}
	return match
}

func sameIMCitationTitle(left, right string) bool {
	return strings.EqualFold(strings.TrimSpace(html.UnescapeString(left)), strings.TrimSpace(html.UnescapeString(right)))
}

func normalizeIMCitationURL(raw string) string {
	raw = strings.TrimSpace(html.UnescapeString(raw))
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" {
		return ""
	}
	switch strings.ToLower(parsed.Scheme) {
	case "http", "https":
		return raw
	default:
		return ""
	}
}

// FormatReplyCitations returns a Markdown source section for adapters that
// render Markdown (Feishu/Lark cards, for example). It is intentionally a
// separate operation from BuildReplyCitations so non-Markdown adapters can
// choose their own structured representation.
func FormatReplyCitations(platform Platform, citations []ReplyCitation) string {
	if len(citations) == 0 {
		return ""
	}

	heading := "引用来源"
	if platform == PlatformLark {
		heading = "Sources"
	}
	var b strings.Builder
	written := 0
	for _, citation := range citations {
		url := normalizeIMCitationURL(citation.URL)
		if url == "" {
			continue
		}
		title := strings.TrimSpace(citation.Title)
		if title == "" {
			title = url
		}
		label := strings.TrimSpace(citation.Label)
		if label == "" {
			label = fmt.Sprintf("S%d", written+1)
		}
		if written == 0 {
			b.WriteString("\n\n")
			b.WriteString(heading)
			b.WriteString(":\n")
		}
		written++
		b.WriteString("[")
		b.WriteString(escapeIMMarkdownText(label))
		b.WriteString("] [")
		b.WriteString(escapeIMMarkdownText(title))
		b.WriteString("](")
		b.WriteString(strings.ReplaceAll(url, ")", "%29"))
		b.WriteString(")\n")
	}
	if written == 0 {
		return ""
	}
	return strings.TrimRight(b.String(), "\n")
}

func escapeIMMarkdownText(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, "[", `\[`)
	return strings.ReplaceAll(value, "]", `\]`)
}

func firstNonEmptyIMString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
