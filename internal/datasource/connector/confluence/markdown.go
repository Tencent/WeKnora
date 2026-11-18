package confluence

import (
	"bytes"
	"fmt"
	"io"
	"net/url"
	"regexp"
	"strings"

	htmltomd "github.com/JohannesKaufmann/html-to-markdown/v2"
	"github.com/Tencent/WeKnora/internal/infrastructure/docparser"
	"golang.org/x/net/html"
)

var nestedTableRow = regexp.MustCompile(`(?i)<tr\b`)

// convertPageHTML protects tables before the default HTML converter flattens
// their rows and cells. The shared normalizer renders simple tables as GFM and
// keeps complex tables as row-separated HTML.
// attachmentLinkPrefixes are the Confluence attachment paths that may remain
// root-relative in a page body. Anything else keeps its original form.
var attachmentLinkPrefixes = []string{"/download/attachments/", "/wiki/download/attachments/"}

// absolutizeAttachmentLinks rewrites root-relative attachment hrefs in
// converted Markdown against the Confluence origin. A link like
// [text](/download/attachments/123/example.pdf?version=1) resolves against
// the WeKnora origin in the browser and fails (#5445); resolving it here
// keeps the filename and query parameters and lets Confluence enforce
// access with the reader's own session. Absolute URLs and unrelated paths
// pass through untouched.
func absolutizeAttachmentLinks(markdown, baseURL string) string {
	if strings.TrimSpace(markdown) == "" || strings.TrimSpace(baseURL) == "" {
		return markdown
	}
	base, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil || base.Scheme == "" || base.Host == "" {
		return markdown
	}
	origin := base.Scheme + "://" + base.Host
	out := markdown
	for _, prefix := range attachmentLinkPrefixes {
		target := "](" + prefix
		for {
			at := strings.Index(out, target)
			if at < 0 {
				break
			}
			open := at + 1
			closeAt := strings.IndexByte(out[open:], ')')
			if closeAt < 0 {
				break
			}
			href := out[open+1 : open+closeAt]
			if strings.HasPrefix(href, prefix) {
				out = out[:open+1] + origin + href + out[open+closeAt:]
			} else {
				break
			}
		}
	}
	return out
}

func convertPageHTML(content string) (string, error) {
	if !strings.Contains(strings.ToLower(content), "<table") {
		return htmltomd.ConvertString(content)
	}

	var protected strings.Builder
	var tables []string
	var markers []string
	tokenizer := html.NewTokenizer(strings.NewReader(content))
	offset, tableStart, depth, copiedUntil := 0, 0, 0, 0
	nested := false
	for {
		kind := tokenizer.Next()
		raw := tokenizer.Raw()
		tokenStart := offset
		offset += len(raw)
		if kind == html.ErrorToken {
			if err := tokenizer.Err(); err != io.EOF {
				return "", fmt.Errorf("tokenize Confluence page HTML: %w", err)
			}
			break
		}
		if kind != html.StartTagToken && kind != html.EndTagToken {
			continue
		}
		name, _ := tokenizer.TagName()
		if !bytes.EqualFold(name, []byte("table")) {
			continue
		}
		if kind == html.StartTagToken {
			if depth == 0 {
				tableStart = tokenStart
				nested = false
			} else {
				nested = true
			}
			depth++
			continue
		}
		if depth == 0 {
			continue
		}
		depth--
		if depth != 0 {
			continue
		}

		marker := fmt.Sprintf("WEKNORACONFLUENCETABLE%dEND", len(tables))
		for strings.Contains(content, marker) {
			marker += "X"
		}
		protected.WriteString(content[copiedUntil:tableStart])
		protected.WriteString("<div>")
		protected.WriteString(marker)
		protected.WriteString("</div>")
		markers = append(markers, marker)
		block := content[tableStart:offset]
		if nested {
			// The shared normalizer matches non-nested tables. Keep nested HTML
			// intact and put rows on separate lines for downstream chunking.
			block = strings.TrimSpace(nestedTableRow.ReplaceAllStringFunc(block, func(tag string) string {
				return "\n" + tag
			}))
		} else {
			block = docparser.NormalizeHTMLTables(block)
		}
		tables = append(tables, block)
		copiedUntil = offset
	}
	if len(tables) == 0 {
		return htmltomd.ConvertString(content)
	}
	protected.WriteString(content[copiedUntil:])
	markdown, err := htmltomd.ConvertString(protected.String())
	if err != nil {
		return "", err
	}
	for i, marker := range markers {
		markdown = strings.ReplaceAll(markdown, marker, strings.TrimSpace(tables[i]))
	}
	return markdown, nil
}
