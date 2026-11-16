package confluence

import (
	"bytes"
	"fmt"
	"io"
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
