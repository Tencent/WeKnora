package dingtalk

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

type markdownRenderResult struct {
	Content      string
	UnknownTypes []string
}

func renderDocumentMarkdown(title string, blocks []json.RawMessage) markdownRenderResult {
	var builder strings.Builder
	title = strings.TrimSpace(title)
	if title != "" {
		builder.WriteString("# ")
		builder.WriteString(title)
		builder.WriteString("\n\n")
	}

	unknown := make(map[string]struct{})
	for _, raw := range blocks {
		var block map[string]interface{}
		if err := json.Unmarshal(raw, &block); err != nil {
			unknown["invalid_json"] = struct{}{}
			continue
		}
		renderBlock(&builder, block, 0, unknown)
	}

	content := strings.TrimSpace(builder.String())
	if content == "" && title != "" {
		content = "# " + title
	}
	if content != "" {
		content += "\n"
	}

	unknownTypes := make([]string, 0, len(unknown))
	for blockType := range unknown {
		unknownTypes = append(unknownTypes, blockType)
	}
	sort.Strings(unknownTypes)
	return markdownRenderResult{Content: content, UnknownTypes: unknownTypes}
}

func renderBlock(builder *strings.Builder, block map[string]interface{}, depth int, unknown map[string]struct{}) {
	if depth > maxResourceDepth {
		unknown["max_depth"] = struct{}{}
		return
	}

	if rawJSONML := firstString(block, "jsonml"); rawJSONML != "" {
		if renderJSONMLString(builder, rawJSONML, unknown) {
			return
		}
	}

	blockType := normalizedBlockType(firstString(block, "blockType", "type", "elementType"))
	nestedPayload := blockPayload(block, blockType)
	payload := nestedPayload
	if payload == nil {
		payload = block
	}
	written := false

	switch blockType {
	case "heading", "heading1", "heading2", "heading3", "heading4", "heading5", "heading6", "heading_1", "heading_2", "heading_3", "heading_4", "heading_5", "heading_6", "h1", "h2", "h3", "h4", "h5", "h6", "title":
		text := extractInlineText(payload)
		if text == "" {
			text = extractInlineText(block)
		}
		if text != "" {
			level := headingLevel(blockType, payload)
			builder.WriteString(strings.Repeat("#", level))
			builder.WriteByte(' ')
			builder.WriteString(text)
			builder.WriteString("\n\n")
			written = true
		}

	case "paragraph", "text", "normal":
		if text := extractInlineText(payload); text != "" {
			builder.WriteString(text)
			builder.WriteString("\n\n")
			written = true
		}

	case "unorderedlist", "unordered_list", "bullet", "bulletedlist", "bulleted_list":
		written = renderList(builder, payload, "- ", depth)

	case "orderedlist", "ordered_list", "number", "numberedlist", "numbered_list":
		written = renderList(builder, payload, "1. ", depth)

	case "blockquote", "quote":
		if text := extractInlineText(payload); text != "" {
			for _, line := range strings.Split(text, "\n") {
				builder.WriteString("> ")
				builder.WriteString(line)
				builder.WriteByte('\n')
			}
			builder.WriteByte('\n')
			written = true
		}

	case "code", "codeblock", "code_block":
		text := extractInlineText(payload)
		if text != "" {
			language := firstString(payload, "language", "lang")
			builder.WriteString("```")
			builder.WriteString(strings.TrimSpace(language))
			builder.WriteByte('\n')
			builder.WriteString(strings.TrimRight(text, "\n"))
			builder.WriteString("\n```\n\n")
			written = true
		}

	case "divider", "horizontalrule", "horizontal_rule", "separator":
		builder.WriteString("---\n\n")
		written = true

	case "table":
		written = renderTable(builder, payload)

	case "image", "picture":
		properties := firstMap(payload, "properties")
		name := firstNonEmptyString(firstString(payload, "name", "title", "alt"), firstString(properties, "name", "title", "alt"), "image")
		url := firstNonEmptyString(firstString(payload, "url", "downloadUrl", "previewUrl", "src"), firstString(properties, "url", "downloadUrl", "previewUrl", "src"))
		if url != "" {
			fmt.Fprintf(builder, "![%s](%s)\n\n", escapeMarkdownLabel(name), url)
			written = true
		}

	case "link", "hyperlink":
		properties := firstMap(payload, "properties")
		label := firstNonEmptyString(extractInlineText(payload), firstString(properties, "text", "title", "name"), firstString(payload, "text", "title", "name"))
		url := firstNonEmptyString(firstString(payload, "url", "href"), firstString(properties, "url", "href"))
		if url != "" {
			if label == "" {
				label = url
			}
			fmt.Fprintf(builder, "[%s](%s)", escapeMarkdownLabel(label), url)
			written = true
		}

	case "attachment", "file":
		name := firstNonEmptyString(firstString(payload, "name", "fileName", "title"), "attachment")
		url := firstString(payload, "url", "downloadUrl", "previewUrl")
		if url != "" {
			fmt.Fprintf(builder, "[%s](%s)\n\n", escapeMarkdownLabel(name), url)
		} else if resourceID := firstString(payload, "resourceId"); resourceID != "" {
			fmt.Fprintf(builder, "[%s — resource %s]\n\n", escapeMarkdownLabel(name), escapeMarkdownLabel(resourceID))
		}
		written = true

	case "":
		// Some responses omit blockType for wrapper/root objects. Render only
		// their explicitly declared children below.

	default:
		if text := extractInlineText(payload); text != "" {
			builder.WriteString(text)
			builder.WriteString("\n\n")
			written = true
		}
		unknown[blockType] = struct{}{}
	}

	if !written && blockType != "" {
		if text := extractInlineText(block); text != "" {
			builder.WriteString(text)
			builder.WriteString("\n\n")
		}
	}

	for _, child := range childBlocks(block, nestedPayload) {
		renderBlock(builder, child, depth+1, unknown)
	}
}

func normalizedBlockType(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.ReplaceAll(value, "-", "_")
	return value
}

func blockPayload(block map[string]interface{}, blockType string) map[string]interface{} {
	keys := []string{blockType}
	switch blockType {
	case "codeblock", "code_block":
		keys = append(keys, "codeBlock", "code")
	case "unorderedlist", "unordered_list", "bullet", "bulletedlist", "bulleted_list":
		keys = append(keys, "unorderedList", "bulletedList", "list")
	case "orderedlist", "ordered_list", "number", "numberedlist", "numbered_list":
		keys = append(keys, "orderedList", "numberedList", "list")
	case "blockquote", "quote":
		keys = append(keys, "blockQuote", "blockquote", "quote")
	}
	keys = append(keys, "element", "data", "payload")
	for _, key := range keys {
		if value, ok := block[key]; ok {
			if payload, ok := value.(map[string]interface{}); ok {
				return payload
			}
		}
	}
	// DingTalk blockType values and payload keys are not always cased or
	// separated identically (for example heading-2 vs heading_2). Match a
	// normalized key before falling back to the wrapper block.
	for key, value := range block {
		if normalizedBlockType(key) != blockType {
			continue
		}
		if payload, ok := value.(map[string]interface{}); ok {
			return payload
		}
	}
	return nil
}

func headingLevel(blockType string, payload map[string]interface{}) int {
	for _, key := range []string{"level", "headingLevel"} {
		if value, ok := payload[key]; ok {
			switch typed := value.(type) {
			case float64:
				return clampHeadingLevel(int(typed))
			case string:
				if parsed, err := strconv.Atoi(strings.TrimPrefix(strings.ToLower(typed), "h")); err == nil {
					return clampHeadingLevel(parsed)
				}
			}
		}
	}
	for level := 1; level <= 6; level++ {
		if blockType == fmt.Sprintf("heading%d", level) || blockType == fmt.Sprintf("heading_%d", level) || blockType == fmt.Sprintf("h%d", level) {
			return level
		}
	}
	return 1
}

func clampHeadingLevel(level int) int {
	if level < 1 {
		return 1
	}
	if level > 6 {
		return 6
	}
	return level
}

func renderList(builder *strings.Builder, payload map[string]interface{}, marker string, depth int) bool {
	items := firstArray(payload, "items", "elements", "children")
	if len(items) == 0 {
		if text := extractInlineText(payload); text != "" {
			builder.WriteString(strings.Repeat("  ", depth))
			builder.WriteString(marker)
			builder.WriteString(text)
			builder.WriteString("\n\n")
			return true
		}
		return false
	}
	written := false
	for _, item := range items {
		text := extractInlineText(item)
		if text == "" {
			continue
		}
		builder.WriteString(strings.Repeat("  ", depth))
		builder.WriteString(marker)
		builder.WriteString(text)
		builder.WriteByte('\n')
		written = true
	}
	if written {
		builder.WriteByte('\n')
	}
	return written
}

func renderTable(builder *strings.Builder, payload map[string]interface{}) bool {
	rowsRaw := firstArray(payload, "rows", "cells", "data")
	if len(rowsRaw) == 0 {
		return false
	}
	rows := make([][]string, 0, len(rowsRaw))
	maxColumns := 0
	for _, rowRaw := range rowsRaw {
		rowMap, _ := rowRaw.(map[string]interface{})
		cellsRaw := firstArray(rowMap, "cells", "columns", "data")
		if len(cellsRaw) == 0 {
			if direct, ok := rowRaw.([]interface{}); ok {
				cellsRaw = direct
			}
		}
		cells := make([]string, 0, len(cellsRaw))
		for _, cell := range cellsRaw {
			cells = append(cells, escapeTableCell(extractInlineText(cell)))
		}
		if len(cells) > maxColumns {
			maxColumns = len(cells)
		}
		if len(cells) > 0 {
			rows = append(rows, cells)
		}
	}
	if len(rows) == 0 || maxColumns == 0 {
		return false
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
	return true
}

func writeTableRow(builder *strings.Builder, cells []string) {
	builder.WriteString("| ")
	builder.WriteString(strings.Join(cells, " | "))
	builder.WriteString(" |\n")
}

func extractInlineText(value interface{}) string {
	var parts []string
	collectInlineText(value, &parts, 0)
	parts = compactAdjacentDuplicates(parts)
	return strings.TrimSpace(strings.Join(parts, ""))
}

func collectInlineText(value interface{}, parts *[]string, depth int) {
	if depth > maxResourceDepth || value == nil {
		return
	}
	switch typed := value.(type) {
	case string:
		if typed != "" {
			*parts = append(*parts, typed)
		}
	case []interface{}:
		for _, item := range typed {
			collectInlineText(item, parts, depth+1)
		}
	case map[string]interface{}:
		for _, key := range []string{"text", "plainText", "plain_text", "value"} {
			if candidate, ok := typed[key]; ok {
				collectInlineText(candidate, parts, depth+1)
			}
		}
		for _, key := range []string{"elements", "richText", "rich_text", "spans", "runs", "content"} {
			if candidate, ok := typed[key]; ok {
				if _, isString := candidate.(string); key == "content" && isString {
					collectInlineText(candidate, parts, depth+1)
					continue
				}
				collectInlineText(candidate, parts, depth+1)
			}
		}
	}
}

func compactAdjacentDuplicates(parts []string) []string {
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if len(out) > 0 && out[len(out)-1] == part {
			continue
		}
		out = append(out, part)
	}
	return out
}

func childBlocks(block, nestedPayload map[string]interface{}) []map[string]interface{} {
	var out []map[string]interface{}
	seen := make(map[string]struct{})
	sources := []map[string]interface{}{block}
	if nestedPayload != nil {
		sources = append(sources, nestedPayload)
	}
	for _, source := range sources {
		for _, key := range []string{"children", "blocks"} {
			for _, child := range firstArray(source, key) {
				childMap, ok := child.(map[string]interface{})
				if !ok {
					continue
				}
				signature := firstString(childMap, "blockId", "id", "uuid")
				if signature == "" {
					if encoded, err := json.Marshal(childMap); err == nil {
						signature = string(encoded)
					}
				}
				if signature != "" {
					if _, exists := seen[signature]; exists {
						continue
					}
					seen[signature] = struct{}{}
				}
				out = append(out, childMap)
			}
		}
	}
	return out
}

func firstMap(values map[string]interface{}, keys ...string) map[string]interface{} {
	if values == nil {
		return nil
	}
	for _, key := range keys {
		if value, ok := values[key].(map[string]interface{}); ok {
			return value
		}
	}
	return nil
}

func firstArray(values map[string]interface{}, keys ...string) []interface{} {
	if values == nil {
		return nil
	}
	for _, key := range keys {
		if value, ok := values[key]; ok {
			if array, ok := value.([]interface{}); ok {
				return array
			}
		}
	}
	return nil
}

func firstString(values map[string]interface{}, keys ...string) string {
	if values == nil {
		return ""
	}
	for _, key := range keys {
		if value, ok := values[key].(string); ok && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func escapeMarkdownLabel(value string) string {
	value = strings.ReplaceAll(value, "\\", "\\\\")
	value = strings.ReplaceAll(value, "[", "\\[")
	value = strings.ReplaceAll(value, "]", "\\]")
	return value
}

func escapeTableCell(value string) string {
	value = strings.ReplaceAll(value, "|", "\\|")
	value = strings.ReplaceAll(value, "\r\n", "<br>")
	value = strings.ReplaceAll(value, "\n", "<br>")
	return value
}
