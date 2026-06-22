package docparser

import (
	"regexp"
	"strings"

	"github.com/JohannesKaufmann/html-to-markdown/v2/converter"
	"github.com/JohannesKaufmann/html-to-markdown/v2/plugin/base"
	"github.com/JohannesKaufmann/html-to-markdown/v2/plugin/commonmark"
	"github.com/JohannesKaufmann/html-to-markdown/v2/plugin/table"
)

var (
	// htmlTableBlockPattern matches a single (non-nested) <table>...</table>
	// block, the form OCR/layout engines such as PaddleOCR-VL emit tables in.
	htmlTableBlockPattern = regexp.MustCompile(`(?is)<table\b[^>]*>.*?</table>`)

	// htmlLayoutAttrPattern matches presentational HTML attributes that carry
	// no semantic value (text-align styles, CSS classes, sizing). Structural
	// attributes like rowspan/colspan are intentionally excluded.
	htmlLayoutAttrPattern = regexp.MustCompile(
		`(?is)\s+(?:style|class|align|valign|width|height|bgcolor)\s*=\s*(?:"[^"]*"|'[^']*'|[^\s>]+)`,
	)

	// htmlSpanAttrPattern detects rowspan/colspan, which Markdown tables cannot
	// represent; such tables keep their HTML form (attributes stripped) instead.
	htmlSpanAttrPattern = regexp.MustCompile(`(?i)\b(?:row|col)span\b`)

	// markdownTableSeparatorPattern matches the |---|---| delimiter row that a
	// valid GFM table must contain.
	markdownTableSeparatorPattern = regexp.MustCompile(`(?m)^\s*\|?\s*:?-+:?\s*(?:\|\s*:?-+:?\s*)+\|?\s*$`)
)

// normalizeHTMLTables rewrites inline HTML <table> blocks embedded in OCR
// markdown output. PaddleOCR-VL emits tables as HTML with per-cell text-align
// styles, which (1) waste tokens on layout markup and (2) are not recognized
// by the chunker's table-protection logic, so large tables get split mid-row.
//
// Each table block is converted to a GFM Markdown table when possible. Tables
// that use rowspan/colspan (which Markdown cannot express) fall back to having
// their presentational attributes stripped so they stay intact as HTML.
func normalizeHTMLTables(md string) string {
	if !strings.Contains(strings.ToLower(md), "<table") {
		return md
	}

	conv := converter.NewConverter(
		converter.WithPlugins(
			base.NewBasePlugin(),
			commonmark.NewCommonmarkPlugin(),
			table.NewTablePlugin(),
		),
	)

	return htmlTableBlockPattern.ReplaceAllStringFunc(md, func(block string) string {
		if htmlSpanAttrPattern.MatchString(block) {
			return stripHTMLLayoutAttrs(block)
		}
		converted, err := conv.ConvertString(block)
		if err != nil {
			return stripHTMLLayoutAttrs(block)
		}
		converted = strings.TrimSpace(converted)
		if converted == "" || !markdownTableSeparatorPattern.MatchString(converted) {
			return stripHTMLLayoutAttrs(block)
		}
		// Pad with blank lines so the Markdown table is a standalone block that
		// the chunker recognizes (and protects) as a table.
		return "\n\n" + converted + "\n\n"
	})
}

// stripHTMLLayoutAttrs removes presentational attributes from an HTML fragment
// while preserving structural attributes (rowspan/colspan) and text content.
func stripHTMLLayoutAttrs(html string) string {
	return htmlLayoutAttrPattern.ReplaceAllString(html, "")
}
