package core

import (
	"context"
	"fmt"
	"strings"
)

// sheetReader is the subset of *Client that blocksToMarkdown needs, so the
// converter can be unit-tested with a fake or nil client.
type sheetReader interface {
	readSheetRange(ctx context.Context, embedToken string, maxBytes int) ([][]string, bool, bool, error)
	readBitableRecords(ctx context.Context, embedToken string, maxBytes int) ([][]string, bool, bool, error)
}

// maxDocumentMarkdownBytes bounds the complete rendered document, including
// ordinary blocks, native tables, and all embedded tables. Keeping this equal
// to the response-body cap restores a single-document boundary after paging.
const maxDocumentMarkdownBytes = maxJSONResponseBytes

const documentTruncatedNote = "> [文档内容未完整读取，已保留可读取内容]"

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
	return blocksToMarkdownWithin(ctx, client, blocks, maxDocumentMarkdownBytes)
}

func blocksToMarkdownWithin(
	ctx context.Context, client sheetReader, blocks []DocxBlock, maxBytes int,
) ([]byte, []pendingAttachment, error) {
	// Reserve the final note before rendering so an over-limit document can never
	// consume the bytes needed to explain why its tail is absent.
	contentLimit := maxBytes - len(documentTruncatedNote) - 2
	if contentLimit <= 0 {
		return nil, nil, fmt.Errorf("document Markdown budget %d is too small", maxBytes)
	}
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
	truncated := false
	for _, b := range blocks {
		if consumed[b.BlockID] {
			continue
		}
		// Attachments are separate fetched items. Preserve their existing collection
		// semantics even when the main Markdown body has reached its byte budget.
		if b.BlockType == BlockTypeFile && b.File != nil {
			atts = append(atts, pendingAttachment{FileToken: b.File.Token, Name: b.File.Name})
		}
		if truncated {
			continue
		}

		var paragraph string
		switch b.BlockType {
		case BlockTypePage:
			continue // root container, no text of its own
		case BlockTypeText:
			paragraph = plainText(textBearingField(b))
		case BlockTypeBullet:
			paragraph = "- " + plainText(textBearingField(b))
		case BlockTypeOrdered:
			paragraph = "1. " + plainText(textBearingField(b))
		case BlockTypeCode:
			paragraph = "```\n" + plainText(textBearingField(b)) + "\n```"
		case BlockTypeQuote:
			paragraph = "> " + plainText(textBearingField(b))
		case BlockTypeDivider:
			paragraph = "---"
		case BlockTypeTable:
			paragraph = renderNativeTable(b, byID)
		case BlockTypeTableCell:
			continue // rendered by its parent table (also covered by `consumed`)
		case BlockTypeSheet:
			if b.Sheet != nil {
				var exhausted bool
				paragraph, exhausted = inlineTable(ctx, client, b.Sheet.Token, "sheet", contentLimit-sb.Len()-2)
				if exhausted {
					truncated = true
				}
			}
		case BlockTypeBitable:
			if b.Bitable != nil {
				var exhausted bool
				paragraph, exhausted = inlineTable(ctx, client, b.Bitable.Token, "bitable", contentLimit-sb.Len()-2)
				if exhausted {
					truncated = true
				}
			}
		case BlockTypeImage:
			// Emit a token-free placeholder: images carry no retrievable text, and
			// leaking the internal media token would pollute embeddings. A neutral
			// marker preserves surrounding context (e.g. "如下图所示").
			paragraph = "![图片]()"
		case BlockTypeTodo:
			if t := plainText(textBearingField(b)); t != "" {
				paragraph = "- [ ] " + t
			}
		case BlockTypeCallout:
			// Callout is a container; its body is usually in child blocks (rendered
			// separately). Emit its own text only if it carries direct inline text,
			// so the container case is a safe no-op.
			if t := plainText(textBearingField(b)); t != "" {
				paragraph = "> " + t
			}
		case BlockTypeFile:
			if b.File != nil {
				name := b.File.Name
				if name == "" {
					name = b.File.Token
				}
				paragraph = "📎 附件：" + name
			}
		default:
			if b.BlockType >= BlockTypeHeading1 && b.BlockType <= blockTypeHeading9 {
				level := b.BlockType - BlockTypeHeading1 + 1
				paragraph = strings.Repeat("#", level) + " " + plainText(textBearingField(b))
			}
		}
		if !writeParaWithin(&sb, paragraph, contentLimit) {
			truncated = true
		}
	}
	if truncated {
		writePara(&sb, documentTruncatedNote)
	}

	out := strings.TrimRight(sb.String(), "\n")
	if out != "" {
		out += "\n"
	}
	return []byte(out), atts, nil
}

func writeParaWithin(sb *strings.Builder, s string, maxBytes int) bool {
	if s == "" {
		return true
	}
	if len(s)+2 > maxBytes-sb.Len() {
		return false
	}
	writePara(sb, s)
	return true
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
func inlineTable(ctx context.Context, client sheetReader, token, kind string, maxBytes int) (string, bool) {
	if client == nil {
		return "", false
	}
	var (
		rows      [][]string
		truncated bool
		exhausted bool
		err       error
		noun      string
	)
	if kind == "sheet" {
		noun = "内嵌电子表格"
	} else {
		noun = "内嵌多维表格"
	}
	note := fmt.Sprintf("> [%s未完整读取，已保留可读取内容]", noun)
	if maxBytes < len(note) {
		return "", true
	}
	// Reserve room for a table-level truncation note. The reader can then stop
	// before retaining rows that the complete document cannot accommodate.
	readerBudget := min(maxTableBytes, maxBytes-len(note)-2)
	if readerBudget <= 0 {
		return note, false
	}
	if kind == "sheet" {
		rows, truncated, exhausted, err = client.readSheetRange(ctx, token, readerBudget)
	} else {
		rows, truncated, exhausted, err = client.readBitableRecords(ctx, token, readerBudget)
	}
	if err != nil {
		failure := fmt.Sprintf("> [无法读取%s]", noun)
		if len(failure) <= maxBytes {
			return failure, false
		}
		return "", true
	}
	table := markdownTable(rows)
	if truncated {
		// A byte budget or incomplete pagination can stop before the row cap,
		// including before the first row. Never hide that behind an empty table.
		if table == "" {
			return note, exhausted
		}
		result := table + "\n\n" + note
		if len(result) <= maxBytes {
			return result, exhausted
		}
		return note, exhausted
	}
	if len(table) > maxBytes {
		return note, false
	}
	return table, false
}
