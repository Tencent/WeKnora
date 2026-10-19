package docparser

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/JohannesKaufmann/html-to-markdown/v2/converter"
	"github.com/JohannesKaufmann/html-to-markdown/v2/plugin/base"
	"github.com/JohannesKaufmann/html-to-markdown/v2/plugin/commonmark"
	"github.com/JohannesKaufmann/html-to-markdown/v2/plugin/table"
	"github.com/PuerkitoBio/goquery"
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
	// need to be expanded into a rectangular grid before Markdown conversion.
	// Span values of 1 (and the invalid 0) do not merge anything and must stay
	// directly convertible.
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

const (
	maxHTMLTableSpan         = 1000
	maxHTMLTableExpandedCell = 100000
)

// NormalizeHTMLTables rewrites inline HTML <table> blocks embedded in OCR
// markdown output. PaddleOCR-VL emits tables as HTML with per-cell text-align
// styles, which (1) waste tokens on layout markup and (2) are not recognized
// by the chunker's table-protection logic, so large tables get split mid-row.
//
// Each table block is converted to a GFM Markdown table when possible. Tables
// that use rowspan/colspan are first expanded into an equivalent rectangular
// grid, so every output row remains self-contained after the chunker splits it.
// Tables the converter cannot turn into a valid GFM table (cell <br>, nested
// lists, nested tables, …) fall back to presentational-attribute stripping plus
// one row per line so the chunker can still split on "\n". Tables inside fenced
// code blocks are left unchanged.
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
		expanded, ok := expandHTMLTableSpans(block)
		if !ok {
			return fallback()
		}
		block = expanded
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

type expandedHTMLTableCell struct {
	tag     string
	content string
}

// expandHTMLTableSpans turns rowspan/colspan cells into a rectangular grid.
// Rowspan values are repeated in the continuation rows so each row can be
// split into an independently readable Markdown table row. Colspan values keep
// content in the first column and leave the remaining columns empty.
func expandHTMLTableSpans(block string) (string, bool) {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(block))
	if err != nil {
		return "", false
	}
	tables := doc.Find("table")
	if tables.Length() != 1 {
		return "", false
	}
	table := tables.First()
	if table.Find("table").Length() != 0 || table.Find("caption, colgroup, col").Length() != 0 {
		return "", false
	}

	rows := table.Find("tr")
	if rows.Length() == 0 {
		return "", false
	}

	cells := make(map[int]map[int]expandedHTMLTableCell)
	maxRow, maxCol := -1, -1
	expandedCells := 0
	valid := true
	rowIndex := 0
	rows.EachWithBreak(func(_ int, row *goquery.Selection) bool {
		rowCells := row.ChildrenFiltered("td, th")
		if rowCells.Length() == 0 {
			valid = false
			return false
		}
		col := 0
		rowCells.EachWithBreak(func(_ int, cell *goquery.Selection) bool {
			for cells[rowIndex][col].tag != "" {
				col++
			}
			rowspan := htmlSpanValue(cell, "rowspan")
			colspan := htmlSpanValue(cell, "colspan")
			if rowspan > maxHTMLTableSpan || colspan > maxHTMLTableSpan ||
				expandedCells+rowspan*colspan > maxHTMLTableExpandedCell {
				valid = false
				return false
			}
			content, err := cell.Html()
			if err != nil {
				valid = false
				return false
			}
			tag := goquery.NodeName(cell)
			if tag != "th" {
				tag = "td"
			}
			for r := rowIndex; r < rowIndex+rowspan; r++ {
				if cells[r] == nil {
					cells[r] = make(map[int]expandedHTMLTableCell)
				}
				for c := col; c < col+colspan; c++ {
					if cells[r][c].tag != "" {
						valid = false
						return false
					}
					cellContent := ""
					if c == col {
						cellContent = content
					}
					cells[r][c] = expandedHTMLTableCell{tag: tag, content: cellContent}
					if r > maxRow {
						maxRow = r
					}
					if c > maxCol {
						maxCol = c
					}
					expandedCells++
				}
			}
			col += colspan
			return true
		})
		if !valid {
			return false
		}
		rowIndex++
		return true
	})
	if !valid || maxRow < 0 || maxCol < 0 {
		return "", false
	}

	var b strings.Builder
	b.WriteString("<table>")
	for r := 0; r <= maxRow; r++ {
		b.WriteString("<tr>")
		for c := 0; c <= maxCol; c++ {
			cell := cells[r][c]
			tag := cell.tag
			if tag == "" {
				tag = "td"
			}
			b.WriteByte('<')
			b.WriteString(tag)
			b.WriteByte('>')
			b.WriteString(cell.content)
			b.WriteString("</")
			b.WriteString(tag)
			b.WriteByte('>')
		}
		b.WriteString("</tr>")
	}
	b.WriteString("</table>")
	return b.String(), true
}

func htmlSpanValue(cell *goquery.Selection, attr string) int {
	value, err := strconv.Atoi(strings.TrimSpace(cell.AttrOr(attr, "1")))
	if err != nil || value < 1 {
		return 1
	}
	return value
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
