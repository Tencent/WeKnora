package dingtalk

import (
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
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
	Level int    `json:"level"`
	Text  string `json:"text"`
}

type listBlock struct {
	List struct {
		Level int `json:"level"`
	} `json:"list"`
}

type tableBlock struct {
	Cells [][]string `json:"cells"`
}

type inline struct {
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
		level := value.Heading.Level
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
		text := renderInlines(value.Children, depth+1, unknown)
		if text == "" {
			return
		}
		level, marker := value.UnorderedList.List.Level, "- "
		if blockType == "orderedlist" {
			level, marker = value.OrderedList.List.Level, "1. "
		}
		if level < 0 {
			level = 0
		} else if level > maxRenderDepth {
			level = maxRenderDepth
		}
		fmt.Fprintf(builder, "%s%s%s\n", strings.Repeat("  ", level), marker, text)
	case "callout", "columns":
		renderChildBlocks(builder, value.Children, depth, unknown)
	case "table":
		renderTable(builder, value.Table.Cells)
	case "":
		unknown["missing_block_type"] = struct{}{}
	default:
		unknown[blockType] = struct{}{}
		// Preserve useful content when DingTalk introduces a container block
		// before the connector learns its presentation semantics.
		renderChildBlocks(builder, value.Children, depth, unknown)
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
			builder.WriteString(escapeText(value.Text))
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
	if value.Strike {
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
