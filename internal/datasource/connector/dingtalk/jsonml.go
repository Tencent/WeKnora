package dingtalk

import (
	"encoding/json"
	"fmt"
	"strings"
)

// renderJSONMLString is a compatibility path for DingTalk responses that carry
// a block as serialized JSONML instead of the default element representation.
func renderJSONMLString(builder *strings.Builder, raw string, unknown map[string]struct{}) bool {
	var value interface{}
	if err := json.Unmarshal([]byte(raw), &value); err != nil {
		unknown["invalid_jsonml"] = struct{}{}
		return false
	}
	before := builder.Len()
	renderJSONMLBlock(builder, value, 0, unknown)
	return builder.Len() > before
}

func renderJSONMLBlock(builder *strings.Builder, value interface{}, depth int, unknown map[string]struct{}) {
	if depth > maxResourceDepth {
		unknown["jsonml_max_depth"] = struct{}{}
		return
	}
	tag, attrs, children, ok := splitJSONMLNode(value)
	if !ok {
		if text, ok := value.(string); ok && text != "" {
			builder.WriteString(text)
		}
		return
	}

	switch tag {
	case "root", "container", "div", "section", "article", "body":
		for _, child := range children {
			renderJSONMLBlock(builder, child, depth+1, unknown)
		}
	case "p", "paragraph":
		if text := renderJSONMLInline(children, depth+1, unknown); text != "" {
			builder.WriteString(text)
			builder.WriteString("\n\n")
		}
	case "h1", "h2", "h3", "h4", "h5", "h6":
		level := int(tag[1] - '0')
		if text := renderJSONMLInline(children, depth+1, unknown); text != "" {
			builder.WriteString(strings.Repeat("#", level))
			builder.WriteByte(' ')
			builder.WriteString(text)
			builder.WriteString("\n\n")
		}
	case "blockquote", "quote":
		text := strings.TrimSpace(renderJSONMLInline(children, depth+1, unknown))
		for _, line := range strings.Split(text, "\n") {
			if line == "" {
				continue
			}
			builder.WriteString("> ")
			builder.WriteString(line)
			builder.WriteByte('\n')
		}
		if text != "" {
			builder.WriteByte('\n')
		}
	case "pre":
		text := strings.TrimRight(renderJSONMLPlainText(children, depth+1), "\n")
		if text != "" {
			language := firstString(attrs, "language", "lang")
			builder.WriteString("```")
			builder.WriteString(language)
			builder.WriteByte('\n')
			builder.WriteString(text)
			builder.WriteString("\n```\n\n")
		}
	case "ul":
		renderJSONMLList(builder, children, "- ", depth, unknown)
	case "ol":
		renderJSONMLList(builder, children, "1. ", depth, unknown)
	case "table":
		if rows := jsonMLTableRows(value, depth+1, unknown); len(rows) > 0 {
			maxColumns := 0
			for _, row := range rows {
				if len(row) > maxColumns {
					maxColumns = len(row)
				}
			}
			for i := range rows {
				for len(rows[i]) < maxColumns {
					rows[i] = append(rows[i], "")
				}
			}
			writeTableRow(builder, rows[0])
			separator := make([]string, maxColumns)
			for i := range separator {
				separator[i] = "---"
			}
			writeTableRow(builder, separator)
			for _, row := range rows[1:] {
				writeTableRow(builder, row)
			}
			builder.WriteByte('\n')
		}
	case "hr":
		builder.WriteString("---\n\n")
	case "br":
		builder.WriteByte('\n')
	case "span", "a", "strong", "b", "em", "i", "u", "s", "del", "code", "img", "image", "mention":
		if text := renderJSONMLInline([]interface{}{value}, depth+1, unknown); text != "" {
			builder.WriteString(text)
			builder.WriteString("\n\n")
		}
	case "li", "tr", "td", "th":
		if text := renderJSONMLInline(children, depth+1, unknown); text != "" {
			builder.WriteString(text)
			builder.WriteString("\n\n")
		}
	default:
		unknown["jsonml_"+tag] = struct{}{}
		for _, child := range children {
			renderJSONMLBlock(builder, child, depth+1, unknown)
		}
	}
}

func splitJSONMLNode(value interface{}) (string, map[string]interface{}, []interface{}, bool) {
	array, ok := value.([]interface{})
	if !ok || len(array) == 0 {
		return "", nil, nil, false
	}
	tag, ok := array[0].(string)
	if !ok || strings.TrimSpace(tag) == "" {
		return "", nil, nil, false
	}
	tag = strings.ToLower(strings.TrimSpace(tag))
	start := 1
	var attrs map[string]interface{}
	if len(array) > 1 {
		if candidate, ok := array[1].(map[string]interface{}); ok {
			attrs = candidate
			start = 2
		}
	}
	return tag, attrs, array[start:], true
}

func renderJSONMLInline(values []interface{}, depth int, unknown map[string]struct{}) string {
	if depth > maxResourceDepth {
		unknown["jsonml_max_depth"] = struct{}{}
		return ""
	}
	var builder strings.Builder
	for _, value := range values {
		switch typed := value.(type) {
		case string:
			builder.WriteString(typed)
		case []interface{}:
			tag, attrs, children, ok := splitJSONMLNode(typed)
			if !ok {
				continue
			}
			inner := renderJSONMLInline(children, depth+1, unknown)
			switch tag {
			case "br":
				builder.WriteByte('\n')
			case "a":
				href := firstString(attrs, "href", "url")
				if href == "" {
					builder.WriteString(inner)
				} else {
					label := inner
					if label == "" {
						label = href
					}
					fmt.Fprintf(&builder, "[%s](%s)", escapeMarkdownLabel(label), href)
				}
			case "img", "image":
				src := firstString(attrs, "src", "url")
				if src != "" {
					alt := firstNonEmptyString(firstString(attrs, "alt", "title", "name"), "image")
					fmt.Fprintf(&builder, "![%s](%s)", escapeMarkdownLabel(alt), src)
				}
			case "strong", "b":
				if inner != "" {
					builder.WriteString("**" + inner + "**")
				}
			case "em", "i":
				if inner != "" {
					builder.WriteString("*" + inner + "*")
				}
			case "s", "del":
				if inner != "" {
					builder.WriteString("~~" + inner + "~~")
				}
			case "code":
				if inner != "" {
					builder.WriteByte('`')
					builder.WriteString(strings.ReplaceAll(inner, "`", "\\`"))
					builder.WriteByte('`')
				}
			case "ul", "ol", "table", "blockquote", "pre", "p", "paragraph", "h1", "h2", "h3", "h4", "h5", "h6":
				builder.WriteString(strings.TrimSpace(renderJSONMLPlainText([]interface{}{typed}, depth+1)))
			default:
				builder.WriteString(inner)
			}
		}
	}
	return builder.String()
}

func renderJSONMLPlainText(values []interface{}, depth int) string {
	if depth > maxResourceDepth {
		return ""
	}
	var builder strings.Builder
	for _, value := range values {
		switch typed := value.(type) {
		case string:
			builder.WriteString(typed)
		case []interface{}:
			tag, _, children, ok := splitJSONMLNode(typed)
			if !ok {
				continue
			}
			if tag == "br" {
				builder.WriteByte('\n')
				continue
			}
			builder.WriteString(renderJSONMLPlainText(children, depth+1))
		}
	}
	return builder.String()
}

func renderJSONMLList(builder *strings.Builder, children []interface{}, marker string, depth int, unknown map[string]struct{}) {
	written := false
	for _, child := range children {
		tag, _, itemChildren, ok := splitJSONMLNode(child)
		if !ok || tag != "li" {
			continue
		}
		var inline []interface{}
		var nested []interface{}
		for _, itemChild := range itemChildren {
			childTag, _, _, childOK := splitJSONMLNode(itemChild)
			if childOK && (childTag == "ul" || childTag == "ol") {
				nested = append(nested, itemChild)
			} else {
				inline = append(inline, itemChild)
			}
		}
		text := strings.TrimSpace(renderJSONMLInline(inline, depth+1, unknown))
		if text != "" {
			builder.WriteString(strings.Repeat("  ", depth))
			builder.WriteString(marker)
			builder.WriteString(text)
			builder.WriteByte('\n')
			written = true
		}
		for _, nestedList := range nested {
			nestedTag, _, nestedChildren, _ := splitJSONMLNode(nestedList)
			nestedMarker := "- "
			if nestedTag == "ol" {
				nestedMarker = "1. "
			}
			renderJSONMLList(builder, nestedChildren, nestedMarker, depth+1, unknown)
		}
	}
	if written {
		builder.WriteByte('\n')
	}
}

func jsonMLTableRows(value interface{}, depth int, unknown map[string]struct{}) [][]string {
	if depth > maxResourceDepth {
		unknown["jsonml_max_depth"] = struct{}{}
		return nil
	}
	tag, _, children, ok := splitJSONMLNode(value)
	if !ok {
		return nil
	}
	if tag == "tr" {
		var row []string
		for _, child := range children {
			cellTag, _, cellChildren, cellOK := splitJSONMLNode(child)
			if cellOK && (cellTag == "td" || cellTag == "th") {
				row = append(row, escapeTableCell(renderJSONMLInline(cellChildren, depth+1, unknown)))
			}
		}
		if len(row) > 0 {
			return [][]string{row}
		}
		return nil
	}
	var rows [][]string
	for _, child := range children {
		rows = append(rows, jsonMLTableRows(child, depth+1, unknown)...)
	}
	return rows
}
