package core

import (
	"context"
	"fmt"
	"strings"
)

// sheetReader is the subset of *Client that blocksToMarkdown needs, so the
// converter can be unit-tested with a fake or nil client.
type sheetReader interface {
	readSheetRange(ctx context.Context, embedToken string) ([][]string, bool, error)
	readBitableRecords(ctx context.Context, embedToken string) ([][]string, bool, error)
}

// pendingAttachment is an embedded file block awaiting a download decision by
// the connector (whitelist/size filtering happens there, not here).
type pendingAttachment struct {
	FileToken string
	Name      string
}

// blocksToMarkdown renders a flat docx block array to Markdown, inlining
// embedded spreadsheet/bitable tables (Task 5) and collecting downloadable
// attachments. client may be nil when the block set has no downdrill blocks.
func blocksToMarkdown(ctx context.Context, client sheetReader, blocks []DocxBlock) ([]byte, []pendingAttachment, error) {
	byID := make(map[string]DocxBlock, len(blocks))
	for _, b := range blocks {
		byID[b.BlockID] = b
	}
	// Blocks nested inside a native table (its cells and their content blocks)
	// are rendered by renderNativeTable. Mark them so the flat loop below does
	// not also Emit them as stray top-level paragraphs.
	consumed := tableDescendants(blocks, byID)

	var sb strings.Builder
	var atts []pendingAttachment
	for _, b := range blocks {
		if consumed[b.BlockID] {
			continue
		}
		switch b.BlockType {
		case BlockTypePage:
			continue // root container, no text of its own
		case BlockTypeText:
			writePara(&sb, plainText(textBearingField(b)))
		case BlockTypeBullet:
			writePara(&sb, "- "+plainText(textBearingField(b)))
		case BlockTypeOrdered:
			writePara(&sb, "1. "+plainText(textBearingField(b)))
		case BlockTypeCode:
			writePara(&sb, "```\n"+plainText(textBearingField(b))+"\n```")
		case BlockTypeQuote:
			writePara(&sb, "> "+plainText(textBearingField(b)))
		case BlockTypeDivider:
			writePara(&sb, "---")
		case BlockTypeTable:
			writePara(&sb, renderNativeTable(b, byID))
		case BlockTypeTableCell:
			continue // rendered by its parent table (also covered by `consumed`)
		case BlockTypeSheet:
			if b.Sheet != nil {
				writePara(&sb, inlineTable(ctx, client, b.Sheet.Token, "sheet"))
			}
		case BlockTypeBitable:
			if b.Bitable != nil {
				writePara(&sb, inlineTable(ctx, client, b.Bitable.Token, "bitable"))
			}
		case BlockTypeImage:
			// Emit a token-free placeholder: images carry no retrievable text, and
			// leaking the internal media token would pollute embeddings. A neutral
			// marker preserves surrounding context (e.g. "如下图所示").
			writePara(&sb, "![图片]()")
		case BlockTypeTodo:
			if t := plainText(textBearingField(b)); t != "" {
				writePara(&sb, "- [ ] "+t)
			}
		case BlockTypeCallout:
			// Callout is a container; its body is usually in child blocks (rendered
			// separately). Emit its own text only if it carries direct inline text,
			// so the container case is a safe no-op.
			if t := plainText(textBearingField(b)); t != "" {
				writePara(&sb, "> "+t)
			}
		case BlockTypeFile:
			if b.File != nil {
				name := b.File.Name
				if name == "" {
					name = b.File.Token
				}
				writePara(&sb, "📎 附件："+name)
				atts = append(atts, pendingAttachment{FileToken: b.File.Token, Name: b.File.Name})
			}
		default:
			if b.BlockType >= BlockTypeHeading1 && b.BlockType <= blockTypeHeading9 {
				level := b.BlockType - BlockTypeHeading1 + 1
				writePara(&sb, strings.Repeat("#", level)+" "+plainText(textBearingField(b)))
			}
		}
	}

	out := strings.TrimRight(sb.String(), "\n")
	if out != "" {
		out += "\n"
	}
	return []byte(out), atts, nil
}

// writePara appends a block of text followed by a blank line; empty text is skipped.
func writePara(sb *strings.Builder, s string) {
	if s == "" {
		return
	}
	sb.WriteString(s)
	sb.WriteString("\n\n")
}

// plainText concatenates the text runs of a text-bearing block.
func plainText(bt *BlockText) string {
	if bt == nil {
		return ""
	}
	var sb strings.Builder
	for _, e := range bt.Elements {
		if e.TextRun != nil {
			sb.WriteString(e.TextRun.Content)
		}
	}
	return sb.String()
}

// headingText returns the heading field for the block's level, or Text as fallback.
func headingText(b DocxBlock) *BlockText {
	fields := []*BlockText{
		b.Heading1, b.Heading2, b.Heading3, b.Heading4, b.Heading5,
		b.Heading6, b.Heading7, b.Heading8, b.Heading9,
	}
	if idx := b.BlockType - BlockTypeHeading1; idx >= 0 && idx < len(fields) && fields[idx] != nil {
		return fields[idx]
	}
	return b.Text
}

// tableDescendants returns the set of block IDs that belong to a native table —
// every cell listed in a table block plus everything reachable through those
// cells' Children. blocksToMarkdown skips these so table content is emitted only
// by the table renderer, never a second time as loose paragraphs.
func tableDescendants(blocks []DocxBlock, byID map[string]DocxBlock) map[string]bool {
	consumed := make(map[string]bool)
	var mark func(id string)
	mark = func(id string) {
		b, ok := byID[id]
		if !ok || consumed[id] {
			return
		}
		// Attachment/media blocks nested in a cell must still be collected (and
		// their reference emitted) by the main loop — the table renderer only
		// extracts text — so do not consume them, only their text structure.
		if b.BlockType == BlockTypeFile || b.BlockType == BlockTypeImage {
			return
		}
		consumed[id] = true
		for _, c := range b.Children {
			mark(c)
		}
	}
	for _, b := range blocks {
		// Only consume cells of tables we will actually render. A table that
		// renderNativeTable would bail on (missing property / zero columns) must
		// NOT have its cells consumed, or their text would be dropped entirely —
		// leave them for the flat loop to Emit as loose paragraphs instead.
		if b.BlockType == BlockTypeTable && tableRenderable(b) {
			for _, cid := range b.Table.Cells {
				mark(cid)
			}
		}
	}
	return consumed
}

// tableRenderable reports whether a native table block carries enough structure
// (a column count) for renderNativeTable to produce a Markdown table. It is the
// single predicate shared by the consume pass and the render pass so the two
// never disagree about which tables are handled by the table renderer.
func tableRenderable(b DocxBlock) bool {
	return b.Table != nil && b.Table.Property != nil && b.Table.Property.ColumnSize > 0
}

// textBearingField returns the inline-text payload for whichever type-named
// field a block populates (docx stores a block's text in a field named after
// its type), so cell content of any text-like type can be extracted uniformly.
func textBearingField(b DocxBlock) *BlockText {
	switch b.BlockType {
	case BlockTypeText:
		return b.Text
	case BlockTypeBullet:
		return b.Bullet
	case BlockTypeOrdered:
		return b.Ordered
	case BlockTypeCode:
		return b.Code
	case BlockTypeQuote:
		return b.Quote
	case BlockTypeTodo:
		return b.Todo
	case BlockTypeCallout:
		return b.Callout
	}
	if b.BlockType >= BlockTypeHeading1 && b.BlockType <= blockTypeHeading9 {
		return headingText(b)
	}
	return b.Text
}

// cellText renders a native table cell to a single string. A Feishu table_cell
// (block_type 32) is a container: its text lives in child blocks, not on the
// cell itself, so we concatenate the text of each child block.
func cellText(cell DocxBlock, byID map[string]DocxBlock) string {
	var parts []string
	for _, childID := range cell.Children {
		if t := plainText(textBearingField(byID[childID])); t != "" {
			parts = append(parts, t)
		}
	}
	return strings.Join(parts, " ")
}

// renderNativeTable renders a native docx table block into a Markdown table.
func renderNativeTable(b DocxBlock, byID map[string]DocxBlock) string {
	if !tableRenderable(b) {
		return ""
	}
	cols := b.Table.Property.ColumnSize
	var cells []string
	for _, cid := range b.Table.Cells {
		cells = append(cells, cellText(byID[cid], byID))
	}
	var rows [][]string
	for i := 0; i < len(cells); i += cols {
		end := i + cols
		if end > len(cells) {
			end = len(cells)
		}
		rows = append(rows, cells[i:end])
	}
	return markdownTable(rows)
}

// markdownTable renders a [][]string (first row = header) as a GFM table.
func markdownTable(rows [][]string) string {
	// A zero-column header (an embedded sheet/bitable with no columns) would Emit
	// a header line of "|  |" and a separator of just "|" — malformed GFM. Render
	// nothing instead.
	if len(rows) == 0 || len(rows[0]) == 0 {
		return ""
	}
	var sb strings.Builder
	cols := len(rows[0])
	sb.WriteString("| " + strings.Join(escapePipes(rows[0]), " | ") + " |\n")
	sb.WriteString("|" + strings.Repeat(" --- |", cols) + "\n")
	for _, r := range rows[1:] {
		// Ragged data: a row wider than the header would Emit more cells than the
		// header/separator declare, producing a malformed GFM table. Clamp to the
		// header width (truncate overflow, pad shortfall).
		if len(r) > cols {
			r = r[:cols]
		}
		for len(r) < cols {
			r = append(r, "")
		}
		sb.WriteString("| " + strings.Join(escapePipes(r), " | ") + " |\n")
	}
	return strings.TrimRight(sb.String(), "\n")
}

// escapePipes makes cell values safe for a single Markdown table row.
func escapePipes(row []string) []string {
	out := make([]string, len(row))
	for i, c := range row {
		out[i] = strings.ReplaceAll(strings.ReplaceAll(c, "\n", " "), "|", "\\|")
	}
	return out
}

// inlineTable reads an embedded sheet/bitable and renders it as a Markdown
// table. Read/permission errors degrade to an inline note rather than failing
// the whole document (partial content beats no content). A reader-reported
// truncation appends a note.
func inlineTable(ctx context.Context, client sheetReader, token, kind string) string {
	if client == nil {
		return ""
	}
	var (
		rows      [][]string
		truncated bool
		err       error
		noun      string
	)
	if kind == "sheet" {
		rows, truncated, err = client.readSheetRange(ctx, token)
		noun = "内嵌电子表格"
	} else {
		rows, truncated, err = client.readBitableRecords(ctx, token)
		noun = "内嵌多维表格"
	}
	if err != nil {
		return fmt.Sprintf("> [无法读取%s]", noun)
	}
	// markdownTable returns "" when there is nothing renderable (no rows, or a
	// header with no columns). Skip the truncation note too in that case, since it
	// would otherwise dangle without a table above it.
	table := markdownTable(rows)
	if table == "" {
		return ""
	}
	if truncated {
		table += fmt.Sprintf("\n\n> 表格已截断（仅显示前 %d 行）", maxTableRows)
	}
	return table
}
