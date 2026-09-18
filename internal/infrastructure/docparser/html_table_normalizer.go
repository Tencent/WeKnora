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

	// htmlSpanAttrPattern detects rowspan/colspan values greater than 1, which
	// Markdown tables cannot represent; such tables keep their HTML form
	// (attributes stripped) instead. Span values of 1 (and the invalid 0) do
	// not merge anything and must stay convertible.
	htmlSpanAttrPattern = regexp.MustCompile(`(?i)\b(?:row|col)span\s*=\s*["']?(?:[2-9]|\d{2,})`)

	// htmlTableRowPattern matches the opening <tr> tag of each table row, used
	// to put every row on its own line so the chunker can split degraded HTML
	// tables at "\n" boundaries.
	htmlTableRowPattern = regexp.MustCompile(`(?i)(<tr\b)`)

	// markdownTableSeparatorPattern matches the |---|---| delimiter row that a
	// valid GFM table must contain.
	markdownTableSeparatorPattern = regexp.MustCompile(`(?m)^\s*\|?\s*:?-+:?\s*(?:\|\s*:?-+:?\s*)+\|?\s*$`)
)

// NormalizeHTMLTables rewrites inline HTML <table> blocks embedded in OCR
// markdown output. PaddleOCR-VL emits tables as HTML with per-cell text-align
// styles, which (1) waste tokens on layout markup and (2) are not recognized
// by the chunker's table-protection logic, so large tables get split mid-row.
//
// Each table block is converted to a GFM Markdown table when possible. Tables
// that use rowspan/colspan (which Markdown cannot express) fall back to having
// their presentational attributes stripped so they stay intact as HTML.
func NormalizeHTMLTables(md string) string {
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
			return splitHTMLTableRows(stripHTMLLayoutAttrs(block))
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

// splitHTMLTableRows puts each table row on its own line and pads the block with
// blank lines. The chunker splits on "\n", so a single-line HTML table would
// otherwise be unsplittable and get force-cut at the absolute max size. Whitespace
// between tags is insignificant in HTML, and newlines are only inserted before
// <tr> (never inside a tag), so the table's meaning is preserved.
func splitHTMLTableRows(block string) string {
	withRows := htmlTableRowPattern.ReplaceAllString(block, "\n$1")
	return "\n\n" + strings.TrimSpace(withRows) + "\n\n"
}
