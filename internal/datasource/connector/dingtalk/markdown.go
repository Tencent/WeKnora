package dingtalk

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

type renderResult struct {
	Markdown     string
	UnknownTypes []string
}

type blockEnvelope struct {
	BlockType     string            `json:"blockType"`
	Paragraph     textBlock         `json:"paragraph"`
	Heading       headingBlock      `json:"heading"`
	Blockquote    textBlock         `json:"blockquote"`
	Callout       calloutBlock      `json:"callout"`
	Columns       columnsBlock      `json:"columns"`
	OrderedList   listBlock         `json:"orderedList"`
	UnorderedList listBlock         `json:"unorderedList"`
	Table         tableBlock        `json:"table"`
	Children      []json.RawMessage `json:"children"`
}

type textBlock struct {
	Text string `json:"text"`
}

type headingBlock struct {
	Level int    `json:"level"`
	Text  string `json:"text"`
}

type calloutBlock struct {
	Sticker string `json:"sticker"`
}

type columnsBlock struct {
	Size int `json:"size"`
}

type listBlock struct {
	List struct {
		Level int `json:"level"`
	} `json:"list"`
}

type tableBlock struct {
	Rows  int        `json:"rolSize"`
	Cols  int        `json:"colSize"`
	Cells [][]string `json:"cells"`
}

type inlineEnvelope struct {
	ElementType string            `json:"elementType"`
	Text        string            `json:"text"`
	Bold        bool              `json:"bold"`
	Italic      bool              `json:"italic"`
	Strike      bool              `json:"stike"`
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
	title = strings.TrimSpace(title)
	if title != "" {
		builder.WriteString("# ")
		builder.WriteString(title)
		builder.WriteString("\n\n")
	}

	unknown := make(map[string]struct{})
	for _, raw := range blocks {
		renderBlock(&builder, raw, 0, unknown)
	}
	content := strings.TrimSpace(builder.String())
	if content != "" {
		content += "\n"
	}
	unknownTypes := make([]string, 0, len(unknown))
	for blockType := range unknown {
		unknownTypes = append(unknownTypes, blockType)
	}
	sort.Strings(unknownTypes)
	return renderResult{Markdown: content, UnknownTypes: unknownTypes}
}

func renderBlock(
	builder *strings.Builder,
	raw json.RawMessage,
	depth int,
	unknown map[string]struct{},
) {
	if depth > maxResourceDepth {
		unknown["max_depth"] = struct{}{}
		return
	}
	var block blockEnvelope
	if err := json.Unmarshal(raw, &block); err != nil {
		unknown["invalid_json"] = struct{}{}
		return
	}
	blockType := strings.ToLower(strings.TrimSpace(block.BlockType))
	switch blockType {
	case "paragraph":
		text := renderInlineChildren(block.Children, depth+1, unknown)
		if text == "" {
			text = block.Paragraph.Text
		}
		writeParagraph(builder, text)
	case "heading":
		text := renderInlineChildren(block.Children, depth+1, unknown)
		if text == "" {
			text = block.Heading.Text
		}
		if text != "" {
			level := block.Heading.Level
			if level < 1 {
				level = 1
			}
			if level > 6 {
				level = 6
			}
			builder.WriteString(strings.Repeat("#", level))
			builder.WriteByte(' ')
			builder.WriteString(text)
			builder.WriteString("\n\n")
		}
	case "blockquote":
		text := renderInlineChildren(block.Children, depth+1, unknown)
		if text == "" {
			text = block.Blockquote.Text
		}
		if text != "" {
			for _, line := range strings.Split(text, "\n") {
				builder.WriteString("> ")
				builder.WriteString(line)
				builder.WriteByte('\n')
			}
			builder.WriteByte('\n')
		}
	case "orderedlist", "unorderedlist":
		text := renderInlineChildren(block.Children, depth+1, unknown)
		if text == "" {
			return
		}
		level := 0
		marker := "- "
		if blockType == "orderedlist" {
			level = block.OrderedList.List.Level
			marker = "1. "
		} else {
			level = block.UnorderedList.List.Level
		}
		if level < 0 {
			level = 0
		}
		if level > maxResourceDepth {
			level = maxResourceDepth
		}
		builder.WriteString(strings.Repeat("  ", level))
		builder.WriteString(marker)
		builder.WriteString(text)
		builder.WriteByte('\n')
	case "callout", "columns":
		for _, child := range block.Children {
			renderBlock(builder, child, depth+1, unknown)
		}
	case "table":
		renderTable(builder, block.Table.Cells)
	case "":
		unknown["missing_block_type"] = struct{}{}
	default:
		unknown[blockType] = struct{}{}
	}
}

func renderInlineChildren(
	children []json.RawMessage,
	depth int,
	unknown map[string]struct{},
) string {
	if depth > maxResourceDepth {
		unknown["inline_max_depth"] = struct{}{}
		return ""
	}
	var builder strings.Builder
	for _, raw := range children {
		var inline inlineEnvelope
		if err := json.Unmarshal(raw, &inline); err != nil {
			unknown["invalid_inline_json"] = struct{}{}
			continue
		}
		switch strings.ToLower(strings.TrimSpace(inline.ElementType)) {
		case "", "text":
			builder.WriteString(styleInlineText(inline.Text, inline))
		case "sticker":
			builder.WriteString(strings.TrimSpace(inline.Properties.Code))
		case "image":
			if src := strings.TrimSpace(inline.Properties.Src); src != "" {
				fmt.Fprintf(&builder, "![image](%s)", src)
			}
		case "link":
			label := renderInlineChildren(inline.Children, depth+1, unknown)
			href := strings.TrimSpace(inline.Properties.Href)
			switch {
			case href == "":
				builder.WriteString(label)
			case label == "":
				fmt.Fprintf(&builder, "[%s](%s)", escapeLabel(href), href)
			default:
				fmt.Fprintf(&builder, "[%s](%s)", escapeLabel(label), href)
			}
		default:
			unknown["inline_"+strings.ToLower(inline.ElementType)] = struct{}{}
			builder.WriteString(inline.Text)
		}
	}
	return builder.String()
}

func styleInlineText(text string, inline inlineEnvelope) string {
	if text == "" {
		return ""
	}
	if strings.EqualFold(inline.Fonts, "monospace") {
		text = "`" + strings.ReplaceAll(text, "`", "\\`") + "`"
	}
	if inline.Bold {
		text = "**" + text + "**"
	}
	if inline.Italic {
		text = "*" + text + "*"
	}
	if inline.Strike {
		text = "~~" + text + "~~"
	}
	return text
}

func writeParagraph(builder *strings.Builder, text string) {
	if text == "" {
		return
	}
	builder.WriteString(text)
	builder.WriteString("\n\n")
}

func renderTable(builder *strings.Builder, rows [][]string) {
	if len(rows) == 0 {
		return
	}
	columnCount := 0
	for _, row := range rows {
		if len(row) > columnCount {
			columnCount = len(row)
		}
	}
	if columnCount == 0 {
		return
	}
	writeTableRow(builder, normalizedRow(rows[0], columnCount))
	separator := make([]string, columnCount)
	for i := range separator {
		separator[i] = "---"
	}
	writeTableRow(builder, separator)
	for _, row := range rows[1:] {
		writeTableRow(builder, normalizedRow(row, columnCount))
	}
	builder.WriteByte('\n')
}

func normalizedRow(row []string, columns int) []string {
	out := make([]string, columns)
	for i := 0; i < len(row) && i < columns; i++ {
		out[i] = escapeTableCell(row[i])
	}
	return out
}

func writeTableRow(builder *strings.Builder, row []string) {
	builder.WriteString("| ")
	builder.WriteString(strings.Join(row, " | "))
	builder.WriteString(" |\n")
}

func escapeLabel(text string) string {
	text = strings.ReplaceAll(text, "\\", "\\\\")
	text = strings.ReplaceAll(text, "[", "\\[")
	return strings.ReplaceAll(text, "]", "\\]")
}

func escapeTableCell(text string) string {
	text = strings.ReplaceAll(text, "|", "\\|")
	text = strings.ReplaceAll(text, "\r\n", "<br>")
	return strings.ReplaceAll(text, "\n", "<br>")
}
