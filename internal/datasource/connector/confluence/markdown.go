package confluence

import (
	"bytes"
	"fmt"
	"io"
	"regexp"
	"strings"

	"github.com/JohannesKaufmann/html-to-markdown/v2/converter"
	"github.com/JohannesKaufmann/html-to-markdown/v2/plugin/base"
	"github.com/JohannesKaufmann/html-to-markdown/v2/plugin/commonmark"
	"github.com/JohannesKaufmann/html-to-markdown/v2/plugin/strikethrough"
	"github.com/PuerkitoBio/goquery"
	"github.com/Tencent/WeKnora/internal/infrastructure/docparser"
	"golang.org/x/net/html"
)

var nestedTableRow = regexp.MustCompile(`(?i)<tr\b`)

// convertHTML keeps strikethrough as GFM ~~text~~. Confluence pages often
// strike out superseded values, which must stay distinguishable from current
// text once the page is indexed.
func convertHTML(content string) (string, error) {
	conv := converter.NewConverter(converter.WithPlugins(
		base.NewBasePlugin(), commonmark.NewCommonmarkPlugin(), strikethrough.NewStrikethroughPlugin(),
	))
	return conv.ConvertString(content)
}

// convertPageHTML protects tables before the default HTML converter flattens
// their rows and cells. The shared normalizer renders simple tables as GFM and
// keeps complex tables as row-separated HTML.
func convertPageHTML(content string) (string, error) {
	content = stripBrowserOnlyMarkup(content)
	if !strings.Contains(strings.ToLower(content), "<table") {
		return convertHTML(content)
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
		if links, ok := attachmentMacroLinks(block); ok {
			block = links
		} else if nested {
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
		return convertHTML(content)
	}
	protected.WriteString(content[copiedUntil:])
	markdown, err := convertHTML(protected.String())
	if err != nil {
		return "", err
	}
	for i, marker := range markers {
		markdown = strings.ReplaceAll(markdown, marker, strings.TrimSpace(tables[i]))
	}
	return markdown, nil
}

// attachmentMacroLinks retains the downloads from Confluence's attachments
// table. The surrounding table UI (sort controls, relative times, and hidden
// detail rows) is not page content.
func attachmentMacroLinks(table string) (string, bool) {
	if !strings.Contains(table, "attachments") {
		return "", false
	}
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(table))
	if err != nil {
		return "", false
	}
	macro := doc.Find("table").First()
	if !macro.Is(".attachments.aui") {
		return "", false
	}
	var list strings.Builder
	list.WriteString("<ul>")
	macro.Find("tr[data-attachment-id] a[href]").Each(func(_ int, link *goquery.Selection) {
		href, _ := link.Attr("href")
		if !strings.Contains(href, "/download/attachments/") {
			return
		}
		list.WriteString("<li>")
		if err := html.Render(&list, link.Get(0)); err != nil {
			return
		}
		list.WriteString("</li>")
	})
	list.WriteString("</ul>")
	markdown, err := convertHTML(list.String())
	return markdown, err == nil
}
