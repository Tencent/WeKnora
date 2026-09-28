package dingtalk

import (
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
)

const maxRenderDepth = 32

type block struct {
	BlockType     string            `json:"blockType"`
	Paragraph     textBlock         `json:"paragraph"`
	Heading       headingBlock      `json:"heading"`
	Blockquote    textBlock         `json:"blockquote"`
	OrderedList   listBlock         `json:"orderedList"`
	UnorderedList listBlock         `json:"unorderedList"`
	Table         tableBlock        `json:"table"`
	Children      []json.RawMessage `json:"children"`
}

type textBlock struct {
	Text string `json:"text"`
}

type headingBlock struct {
	Level flexibleInt `json:"level"`
	Text  string      `json:"text"`
}

type listBlock struct {
	List struct {
		Level flexibleInt `json:"level"`
	} `json:"list"`
}

type tableBlock struct {
	Cells json.RawMessage `json:"cells"`
}

type inline struct {
	ElementType string            `json:"elementType"`
	Text        string            `json:"text"`
	Bold        bool              `json:"bold"`
	Italic      bool              `json:"italic"`
	Strike      bool              `json:"strike"`
	Stike       bool              `json:"stike"` // DingTalk payloads have used this misspelling.
	Fonts       string            `json:"fonts"`
	Properties  inlineProperties  `json:"properties"`
	Children    []json.RawMessage `json:"children"`
}

type inlineProperties struct {
	Code string `json:"code"`
	Src  string `json:"src"`
	Href string `json:"href"`
}

func renderDocument(title string, blocks []json.RawMessage) renderResult {
	var builder strings.Builder
	if title = strings.TrimSpace(title); title != "" {
		title = strings.NewReplacer("\r", " ", "\n", " ").Replace(title)
		builder.WriteString("# ")
		builder.WriteString(escapeText(title))
		builder.WriteString("\n\n")
	}

	unknown := make(map[string]struct{})
	for _, raw := range blocks {
		renderBlock(&builder, raw, 0, unknown)
	}

	markdown := strings.TrimSpace(builder.String())
	if markdown != "" {
		markdown += "\n"
	}
	unknownTypes := make([]string, 0, len(unknown))
	for blockType := range unknown {
		unknownTypes = append(unknownTypes, blockType)
	}
	sort.Strings(unknownTypes)
	return renderResult{Markdown: markdown, UnknownTypes: unknownTypes}
}

func renderBlock(
	builder *strings.Builder,
	raw json.RawMessage,
	depth int,
	unknown map[string]struct{},
) {
	if depth > maxRenderDepth {
		unknown["max_depth"] = struct{}{}
		return
	}

	var value block
	if err := json.Unmarshal(raw, &value); err != nil {
		unknown["invalid_json"] = struct{}{}
		return
	}

	blockType := strings.ToLower(strings.TrimSpace(value.BlockType))
	switch blockType {
	case "paragraph":
		text := renderInlines(value.Children, depth+1, unknown)
		if text == "" {
			text = escapeText(value.Paragraph.Text)
		}
		writeParagraph(builder, text)
	case "heading":
		text := renderInlines(value.Children, depth+1, unknown)
		if text == "" {
			text = escapeText(value.Heading.Text)
		}
		if text == "" {
			return
		}
		level := int(value.Heading.Level)
		if level < 1 {
			level = 1
		} else if level > 6 {
			level = 6
		}
		fmt.Fprintf(builder, "%s %s\n\n", strings.Repeat("#", level), text)
	case "blockquote":
		text := renderInlines(value.Children, depth+1, unknown)
		if text == "" {
			text = escapeText(value.Blockquote.Text)
		}
		if text == "" {
			return
		}
		for _, line := range strings.Split(text, "\n") {
			fmt.Fprintf(builder, "> %s\n", line)
		}
		builder.WriteByte('\n')
	case "orderedlist", "unorderedlist":
		renderListBlock(builder, value, blockType, depth, 0, unknown)
	case "callout", "columns":
		if len(value.Children) == 0 {
			// The Blocks API only returns first-level blocks. A container with
			// no inlined children is indistinguishable from an empty callout,
			// so surface it instead of silently dropping nested body text.
			unknown["nested_blocks_unavailable"] = struct{}{}
			return
		}
		renderChildBlocks(builder, value.Children, depth, unknown)
	case "table":
		renderTable(builder, parseTableCells(value.Table.Cells))
	case "":
		unknown["missing_block_type"] = struct{}{}
	default:
		unknown[blockType] = struct{}{}
		// Preserve useful content when DingTalk introduces a container block
		// before the connector learns its presentation semantics.
		renderChildBlocks(builder, value.Children, depth, unknown)
	}
}

// renderListBlock renders one list block.
//
// Documented shape: children are inline elements and one block is one item —
// nesting comes from list.level (0-based) and the items of one list are linked
// by a shared listId across sibling blocks, not by structural nesting. The
// child walk below also accepts block children, which the contract does not
// produce: the previous implementation rendered inline children *or* block
// children (never both), so a payload outside the contract lost whichever kind
// it did not pick, sometimes without even an unknown-type marker.
//
// minLevel is the smallest indent level this list may use; pass 0 for a
// top-level list.
func renderListBlock(
	builder *strings.Builder,
	value block,
	blockType string,
	depth int,
	minLevel int,
	unknown map[string]struct{},
) {
	if depth > maxRenderDepth {
		unknown["max_depth"] = struct{}{}
		return
	}

	level, marker := int(value.UnorderedList.List.Level), "- "
	if blockType == "orderedlist" {
		level, marker = int(value.OrderedList.List.Level), "1. "
	}
	if level < minLevel {
		level = minLevel
	}
	if level < 0 {
		level = 0
	} else if level > maxRenderDepth {
		level = maxRenderDepth
	}
	indent := strings.Repeat("  ", level)

	// Inline children are the current item's own text and block children are
	// further items, so walk the children in payload order: consecutive inline
	// elements form one item, and every block child becomes its own item.
	var pending strings.Builder
	flush := func() {
		if text := pending.String(); text != "" {
			writeListItem(builder, indent, marker, text)
		}
		pending.Reset()
	}
	for _, child := range value.Children {
		childType := blockChildType(child)
		if childType == "" {
			pending.WriteString(renderInlines([]json.RawMessage{child}, depth+1, unknown))
			continue
		}
		flush()
		if childType == "orderedlist" || childType == "unorderedlist" {
			var nested block
			if err := json.Unmarshal(child, &nested); err != nil {
				unknown["invalid_json"] = struct{}{}
				continue
			}
			// Outside the documented contract: the Blocks API returns lists
			// flat (one block per item, siblings linked by listId, nesting
			// carried by list.level), so a list child that is itself a list
			// should not appear. If one does, keep it below its parent rather
			// than at the same indent, and never drop it.
			renderListBlock(builder, nested, childType, depth+1, level+1, unknown)
			continue
		}
		var item strings.Builder
		renderBlock(&item, child, depth+1, unknown)
		if text := strings.TrimSpace(item.String()); text != "" {
			writeListItem(builder, indent, marker, text)
		}
	}
	flush()
}

// blockChildType reports the block type of a list child, or "" when the child
// is an inline element (including an untyped text run) rather than a block.
func blockChildType(raw json.RawMessage) string {
	var probe struct {
		BlockType   string `json:"blockType"`
		ElementType string `json:"elementType"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		return ""
	}
	if strings.TrimSpace(probe.ElementType) != "" {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(probe.BlockType))
}

// writeListItem writes one list item. Continuation lines of a multi-line item
// are aligned under the first line so the value cannot escape its bullet.
func writeListItem(builder *strings.Builder, indent string, marker string, text string) {
	lines := strings.Split(text, "\n")
	builder.WriteString(indent)
	builder.WriteString(marker)
	builder.WriteString(lines[0])
	builder.WriteByte('\n')
	if len(lines) == 1 {
		return
	}
	continuation := strings.Repeat(" ", len(indent)+len(marker))
	for _, line := range lines[1:] {
		if line = strings.TrimRight(line, " \t"); line == "" {
			continue
		}
		builder.WriteString(continuation)
		builder.WriteString(line)
		builder.WriteByte('\n')
	}
}

func renderChildBlocks(
	builder *strings.Builder,
	children []json.RawMessage,
	depth int,
	unknown map[string]struct{},
) {
	for _, child := range children {
		renderBlock(builder, child, depth+1, unknown)
	}
}

func renderInlines(
	children []json.RawMessage,
	depth int,
	unknown map[string]struct{},
) string {
	if depth > maxRenderDepth {
		unknown["inline_max_depth"] = struct{}{}
		return ""
	}

	var builder strings.Builder
	for _, raw := range children {
		var value inline
		if err := json.Unmarshal(raw, &value); err != nil {
			unknown["invalid_inline_json"] = struct{}{}
			continue
		}

		elementType := strings.ToLower(strings.TrimSpace(value.ElementType))
		switch elementType {
		case "", "text":
			builder.WriteString(styleText(value.Text, value))
		case "sticker":
			builder.WriteString(escapeText(value.Properties.Code))
		case "image":
			if src, ok := safeURL(value.Properties.Src); ok {
				fmt.Fprintf(&builder, "![image](%s)", src)
			}
		case "link":
			label := renderInlines(value.Children, depth+1, unknown)
			href, ok := safeURL(value.Properties.Href)
			if !ok {
				builder.WriteString(label)
			} else if label == "" {
				fmt.Fprintf(&builder, "[%s](%s)", escapeLabel(href), href)
			} else {
				fmt.Fprintf(&builder, "[%s](%s)", label, href)
			}
		default:
			unknown["inline_"+elementType] = struct{}{}
			// Inline types this renderer does not model can carry their text in
			// children instead of in a text field (slot is documented that way),
			// so falling back to children keeps the run from vanishing.
			if text := escapeText(value.Text); text != "" {
				builder.WriteString(text)
			} else {
				builder.WriteString(renderInlines(value.Children, depth+1, unknown))
			}
		}
	}
	return builder.String()
}

func styleText(text string, value inline) string {
	text = strings.ReplaceAll(text, "\x00", "")
	if text == "" {
		return ""
	}
	if strings.EqualFold(value.Fonts, "monospace") {
		text = codeSpan(text)
	} else {
		text = escapeText(text)
	}
	if value.Bold {
		text = "**" + text + "**"
	}
	if value.Italic {
		text = "*" + text + "*"
	}
	if value.Strike || value.Stike {
		text = "~~" + text + "~~"
	}
	return text
}

func safeURL(value string) (string, bool) {
	value = strings.TrimSpace(value)
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme == "" {
		return "", false
	}
	switch strings.ToLower(parsed.Scheme) {
	case "http", "https", "mailto":
		return strings.NewReplacer(
			"\\", "%5C",
			" ", "%20",
			"(", "%28",
			")", "%29",
			"<", "%3C",
			">", "%3E",
		).Replace(value), true
	default:
		return "", false
	}
}

func writeParagraph(builder *strings.Builder, text string) {
	if text != "" {
		builder.WriteString(text)
		builder.WriteString("\n\n")
	}
}

func renderTable(builder *strings.Builder, rows [][]string) {
	columns := 0
	for _, row := range rows {
		if len(row) > columns {
			columns = len(row)
		}
	}
	if columns == 0 {
		return
	}

	writeTableRow(builder, normalizeRow(rows[0], columns))
	separator := make([]string, columns)
	for index := range separator {
		separator[index] = "---"
	}
	writeTableRow(builder, separator)
	for _, row := range rows[1:] {
		writeTableRow(builder, normalizeRow(row, columns))
	}
	builder.WriteByte('\n')
}

func normalizeRow(row []string, columns int) []string {
	normalized := make([]string, columns)
	for index := 0; index < len(row) && index < columns; index++ {
		normalized[index] = escapeTableCell(row[index])
	}
	return normalized
}

func writeTableRow(builder *strings.Builder, row []string) {
	fmt.Fprintf(builder, "| %s |\n", strings.Join(row, " | "))
}

func escapeText(value string) string {
	value = strings.ReplaceAll(value, "\x00", "")
	value = strings.ReplaceAll(value, "\\", "\\\\")
	return strings.NewReplacer(
		"`", "\\`",
		"*", "\\*",
		"_", "\\_",
		"{", "\\{",
		"}", "\\}",
		"[", "\\[",
		"]", "\\]",
		"<", "\\<",
		">", "\\>",
		"#", "\\#",
		"+", "\\+",
		"-", "\\-",
		"!", "\\!",
	).Replace(value)
}

func escapeLabel(value string) string {
	value = strings.ReplaceAll(value, "\\", "\\\\")
	value = strings.ReplaceAll(value, "[", "\\[")
	return strings.ReplaceAll(value, "]", "\\]")
}

func escapeTableCell(value string) string {
	value = escapeText(value)
	value = strings.ReplaceAll(value, "|", "\\|")
	value = strings.ReplaceAll(value, "\r\n", "<br>")
	return strings.ReplaceAll(value, "\n", "<br>")
}

func codeSpan(value string) string {
	delimiter := "`"
	for strings.Contains(value, delimiter) {
		delimiter += "`"
	}
	if len(delimiter) == 1 {
		return delimiter + value + delimiter
	}
	return delimiter + " " + value + " " + delimiter
}

type flexibleInt int

func (f *flexibleInt) UnmarshalJSON(data []byte) error {
	*f = 0
	if len(data) == 0 || string(data) == "null" {
		return nil
	}
	var n int
	if err := json.Unmarshal(data, &n); err == nil {
		*f = flexibleInt(n)
		return nil
	}
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return nil
	}
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.TrimPrefix(s, "heading-")
	s = strings.TrimPrefix(s, "h")
	n, err := strconv.Atoi(s)
	if err != nil {
		return nil
	}
	*f = flexibleInt(n)
	return nil
}

func parseTableCells(raw json.RawMessage) [][]string {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var rows [][]string
	if err := json.Unmarshal(raw, &rows); err == nil {
		return rows
	}
	var generic [][]any
	if err := json.Unmarshal(raw, &generic); err != nil {
		return nil
	}
	out := make([][]string, len(generic))
	for i, row := range generic {
		out[i] = make([]string, len(row))
		for j, cell := range row {
			out[i][j] = tableCellText(cell)
		}
	}
	return out
}

func tableCellText(cell any) string {
	switch value := cell.(type) {
	case nil:
		return ""
	case string:
		return value
	case float64:
		return strconv.FormatFloat(value, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(value)
	case map[string]any:
		if text, ok := value["text"].(string); ok && strings.TrimSpace(text) != "" {
			return text
		}
		if children, ok := value["children"].([]any); ok {
			var builder strings.Builder
			for _, child := range children {
				builder.WriteString(tableCellText(child))
			}
			return builder.String()
		}
		return ""
	default:
		return ""
	}
}
