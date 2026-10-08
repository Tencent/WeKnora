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

	// fencedCodeBlockPattern matches CommonMark fenced code blocks so HTML
	// <table> examples inside them are left untouched.
	fencedCodeBlockPattern = regexp.MustCompile("(?s)(?:```|~~~)[^\\n]*\\n.*?(?:```|~~~)")

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
	htmlTableRowPattern = regexp.MustCompile(`(?i)<tr\b`)

	// markdownTableSeparatorPattern matches the |---| delimiter row that a
	// valid GFM table must contain. The repeating column group is optional so
	// single-column tables (|---|) are accepted; requiring two or more columns
	// discarded successful conversions of MinerU figure/TOC tables.
	markdownTableSeparatorPattern = regexp.MustCompile(`(?m)^\s*\|?\s*:?-+:?\s*(?:\|\s*:?-+:?\s*)*\|?\s*$`)
)

// NormalizeHTMLTables rewrites inline HTML <table> blocks embedded in OCR
// markdown output. PaddleOCR-VL emits tables as HTML with per-cell text-align
// styles, which (1) waste tokens on layout markup and (2) are not recognized
// by the chunker's table-protection logic, so large tables get split mid-row.
//
// Each table block is converted to a GFM Markdown table when possible. Tables
// that use rowspan/colspan (which Markdown cannot express), or that the
// converter cannot turn into a valid GFM table (cell <br>, nested lists, …),
// fall back to presentational-attribute stripping plus one row per line so
// the chunker can still split on "\n". Tables inside fenced code blocks are
// left unchanged.
func NormalizeHTMLTables(md string) string {
	if !strings.Contains(strings.ToLower(md), "<table") {
		return md
	}

	locs := htmlTableBlockPattern.FindAllStringIndex(md, -1)
	if len(locs) == 0 {
		return md
	}

	conv := converter.NewConverter(
		converter.WithPlugins(
			base.NewBasePlugin(),
			commonmark.NewCommonmarkPlugin(),
			table.NewTablePlugin(),
		),
	)
	fences := fencedCodeBlockPattern.FindAllStringIndex(md, -1)

	var b strings.Builder
	last := 0
	for _, loc := range locs {
		b.WriteString(md[last:loc[0]])
		block := md[loc[0]:loc[1]]
		if indexInsideSpans(loc[0], fences) {
			b.WriteString(block)
			last = loc[1]
			continue
		}
		// Isolate the rewritten table with blank lines without stacking pads
		// on repeated NormalizeHTMLTables calls.
		out := strings.TrimRight(b.String(), "\n")
		b.Reset()
		b.WriteString(out)
		b.WriteString("\n\n")
		b.WriteString(normalizeOneHTMLTable(conv, block))
		last = loc[1]
		for last < len(md) && md[last] == '\n' {
			last++
		}
		b.WriteString("\n\n")
	}
	b.WriteString(md[last:])
	return b.String()
}

func normalizeOneHTMLTable(conv *converter.Converter, block string) string {
	fallback := func() string {
		return splitHTMLTableRows(stripHTMLLayoutAttrs(block))
	}
	if htmlSpanAttrPattern.MatchString(block) {
		return fallback()
	}
	converted, err := conv.ConvertString(block)
	if err != nil {
		return fallback()
	}
	converted = unescapeMarkdownImageSyntax(strings.TrimSpace(converted))
	if converted == "" || !markdownTableSeparatorPattern.MatchString(converted) {
		return fallback()
	}
	return converted
}

func indexInsideSpans(pos int, spans [][]int) bool {
	for _, s := range spans {
		if pos >= s[0] && pos < s[1] {
			return true
		}
	}
	return false
}

// stripHTMLLayoutAttrs removes presentational attributes from an HTML fragment
// while preserving structural attributes (rowspan/colspan) and text content.
func stripHTMLLayoutAttrs(html string) string {
	return htmlLayoutAttrPattern.ReplaceAllString(html, "")
}

// splitHTMLTableRows puts each table row on its own line. The chunker splits
// on "\n", so a single-line HTML table would otherwise be unsplittable and get
// force-cut at the absolute max size. Whitespace between tags is insignificant
// in HTML, and newlines are only inserted before <tr> (never inside a tag).
// A <tr> that already starts on its own line is left alone.
func splitHTMLTableRows(block string) string {
	locs := htmlTableRowPattern.FindAllStringIndex(block, -1)
	if len(locs) == 0 {
		return strings.TrimSpace(block)
	}
	var b strings.Builder
	last := 0
	for _, loc := range locs {
		b.WriteString(block[last:loc[0]])
		if loc[0] == 0 || block[loc[0]-1] != '\n' {
			b.WriteByte('\n')
		}
		b.WriteString(block[loc[0]:loc[1]])
		last = loc[1]
	}
	b.WriteString(block[last:])
	return strings.TrimSpace(b.String())
}
