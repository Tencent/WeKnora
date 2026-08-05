package dingtalk

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

// blocksToMarkdown converts a DingTalk document's block list into Markdown.
//
// D5: the conversion preserves document structure that a plain-text dump would
// destroy — heading levels, list nesting, table rows, and code fences. Structure
// matters downstream because RAG chunking splits on Markdown boundaries, so a
// flattened document retrieves markedly worse than a structured one.
func blocksToMarkdown(blocks []block) string {
	markdown, _ := blocksToMarkdownWithDiagnostics(blocks)
	return markdown
}

// blocksToMarkdownWithDiagnostics returns the semantic fallback decisions made
// during rendering. Keeping diagnostics separate from Markdown avoids indexing
// implementation warnings as document content while still letting the
// connector surface new DingTalk block kinds to operators.
func blocksToMarkdownWithDiagnostics(blocks []block) (string, []string) {
	unknownKinds := make(map[string]struct{})
	markdown := renderBlocksToMarkdown(blocks, unknownKinds)
	kinds := make([]string, 0, len(unknownKinds))
	for kind := range unknownKinds {
		kinds = append(kinds, kind)
	}
	sort.Strings(kinds)
	return markdown, kinds
}

func renderBlocksToMarkdown(blocks []block, unknownKinds map[string]struct{}) string {
	var sb strings.Builder
	// listCounters tracks the running number for each ordered-list nesting level.
	listCounters := map[int]int{}

	for i := range blocks {
		b := &blocks[i]
		kind := strings.ToLower(strings.TrimSpace(b.BlockType))

		// Any non-list block resets ordered-list numbering.
		if kind != "ordered_list" && kind != "orderedlist" {
			listCounters = map[int]int{}
		}

		switch kind {
		case "heading", "header", "title":
			level := blockInt(b, "level")
			if level < 1 || level > 6 {
				level = 1
			}
			if text := blockText(b); text != "" {
				sb.WriteString(strings.Repeat("#", level) + " " + text + "\n\n")
			}

		case "bulleted_list", "bulletedlist", "unordered_list", "unorderedlist", "list_item":
			indent := strings.Repeat("  ", blockIndent(b))
			if text := blockText(b); text != "" {
				sb.WriteString(indent + "- " + text + "\n")
			}

		case "ordered_list", "orderedlist":
			lvl := blockIndent(b)
			listCounters[lvl]++
			// Deeper levels restart when we come back up.
			for k := range listCounters {
				if k > lvl {
					delete(listCounters, k)
				}
			}
			if text := blockText(b); text != "" {
				sb.WriteString(strings.Repeat("  ", lvl) +
					strconv.Itoa(listCounters[lvl]) + ". " + text + "\n")
			}

		case "code", "code_block", "codeblock":
			lang, _ := b.Value["language"].(string)
			text := blockText(b)
			sb.WriteString("```" + lang + "\n")
			if text != "" {
				sb.WriteString(text)
				if !strings.HasSuffix(text, "\n") {
					sb.WriteString("\n")
				}
			}
			sb.WriteString("```\n\n")

		case "quote", "blockquote":
			for _, line := range strings.Split(blockText(b), "\n") {
				sb.WriteString("> " + line + "\n")
			}
			sb.WriteString("\n")

		case "divider", "horizontal_rule", "hr":
			sb.WriteString("---\n\n")

		case "table":
			sb.WriteString(tableToMarkdown(b))

		case "callout", "columns":
			if nested := renderBlocksToMarkdown(b.Children, unknownKinds); nested != "" {
				sb.WriteString(nested + "\n\n")
			}

		case "image":
			alt := blockText(b)
			if alt == "" {
				alt = "image"
			}
			// DingTalk image URLs may be short-lived signed URLs. Until media
			// ingestion is implemented, keep a useful marker without persisting
			// credentials embedded in src.
			sb.WriteString("[" + alt + "]\n\n")

		case "todo", "checkbox", "todo_list":
			mark := " "
			if done, _ := blockProperties(b)["checked"].(bool); done {
				mark = "x"
			}
			if text := blockText(b); text != "" {
				sb.WriteString(strings.Repeat("  ", blockIndent(b)) +
					"- [" + mark + "] " + text + "\n")
			}

		default:
			// Paragraph and anything unrecognized keep their inline/value text.
			if text := blockText(b); text != "" {
				sb.WriteString(text + "\n\n")
			}
			if kind != "" && kind != "paragraph" {
				unknownKinds[kind] = struct{}{}
			}
			// Unknown containers can wrap structural blocks (paragraphs, lists,
			// tables) rather than inline runs. blockText deliberately ignores
			// those children, so recurse over them to preserve their semantics.
			if nested := renderBlocksToMarkdown(structuralBlockChildren(b), unknownKinds); nested != "" {
				sb.WriteString(nested + "\n\n")
			}
		}
	}

	return strings.TrimSpace(sb.String())
}

func structuralBlockChildren(b *block) []block {
	if b == nil || len(b.Children) == 0 {
		return nil
	}
	children := make([]block, 0, len(b.Children))
	for i := range b.Children {
		child := b.Children[i]
		if inlineElementType(&child) == "" {
			children = append(children, child)
		}
	}
	return children
}

// tableToMarkdown renders a table block as a GitHub-flavored Markdown table.
// The first row is used as the header, which is what Markdown requires.
func tableToMarkdown(b *block) string {
	rows := extractRows(b)
	if len(rows) == 0 {
		return ""
	}
	width := 0
	for _, r := range rows {
		if len(r) > width {
			width = len(r)
		}
	}
	if width == 0 {
		return ""
	}

	var sb strings.Builder
	writeRow := func(cells []string) {
		sb.WriteString("|")
		for i := 0; i < width; i++ {
			cell := ""
			if i < len(cells) {
				// A literal pipe would break the table structure.
				cell = strings.ReplaceAll(cells[i], "|", "\\|")
				cell = strings.ReplaceAll(cell, "\n", " ")
			}
			sb.WriteString(" " + cell + " |")
		}
		sb.WriteString("\n")
	}

	writeRow(rows[0])
	sb.WriteString("|")
	for i := 0; i < width; i++ {
		sb.WriteString(" --- |")
	}
	sb.WriteString("\n")
	for _, r := range rows[1:] {
		writeRow(r)
	}
	sb.WriteString("\n")
	return sb.String()
}

// extractRows pulls a row/cell matrix out of a table block, tolerating the
// several shapes DingTalk uses across API versions.
func extractRows(b *block) [][]string {
	properties := blockProperties(b)
	raw, ok := properties["rows"]
	if !ok {
		raw = properties["cells"]
	}
	list, ok := raw.([]interface{})
	if !ok {
		return nil
	}
	var rows [][]string
	for _, r := range list {
		switch row := r.(type) {
		case []interface{}:
			rows = append(rows, toStrings(row))
		case map[string]interface{}:
			inner, ok := row["cells"].([]interface{})
			if !ok {
				continue
			}
			rows = append(rows, toStrings(inner))
		}
	}
	return rows
}

func toStrings(in []interface{}) []string {
	out := make([]string, 0, len(in))
	for _, v := range in {
		switch cell := v.(type) {
		case string:
			out = append(out, cell)
		case map[string]interface{}:
			if t, ok := cell["text"].(string); ok {
				out = append(out, t)
				continue
			}
			out = append(out, "")
		case nil:
			out = append(out, "")
		default:
			out = append(out, fmt.Sprintf("%v", cell))
		}
	}
	return out
}

// blockText extracts the textual payload of a block, tolerating the differing
// shapes DingTalk returns ("text", "content", or a rich-text run array).
func blockText(b *block) string {
	if len(b.Children) > 0 {
		var sb strings.Builder
		for i := range b.Children {
			child := &b.Children[i]
			if inlineElementType(child) == "" {
				continue
			}
			sb.WriteString(inlineToMarkdown(child))
		}
		if text := strings.TrimRight(sb.String(), " \t"); text != "" {
			return text
		}
	}

	properties := blockProperties(b)
	for _, key := range []string{"text", "content", "plainText"} {
		if s, ok := properties[key].(string); ok && s != "" {
			return strings.TrimRight(s, " \t")
		}
	}
	// Rich text: an array of runs, each carrying its own text.
	for _, key := range []string{"texts", "richText", "elements"} {
		if runs, ok := properties[key].([]interface{}); ok {
			var sb strings.Builder
			for _, r := range runs {
				m, ok := r.(map[string]interface{})
				if !ok {
					continue
				}
				t, _ := m["text"].(string)
				if t == "" {
					continue
				}
				sb.WriteString(applyMarks(t, m))
			}
			if s := sb.String(); s != "" {
				return strings.TrimRight(s, " \t")
			}
		}
	}
	return ""
}

// blockProperties returns the official type-specific payload for a block.
// Value is retained as a compatibility fallback for early fixtures.
func blockProperties(b *block) map[string]interface{} {
	switch strings.ToLower(strings.TrimSpace(b.BlockType)) {
	case "paragraph":
		if b.Paragraph != nil {
			return b.Paragraph
		}
	case "heading", "header", "title":
		if b.Heading != nil {
			return b.Heading
		}
	case "blockquote", "quote":
		if b.Blockquote != nil {
			return b.Blockquote
		}
	case "callout":
		if b.Callout != nil {
			return b.Callout
		}
	case "columns":
		if b.Columns != nil {
			return b.Columns
		}
	case "orderedlist", "ordered_list":
		if b.OrderedList != nil {
			return b.OrderedList
		}
	case "unorderedlist", "unordered_list", "bulletedlist", "bulleted_list", "list_item":
		if b.UnorderedList != nil {
			return b.UnorderedList
		}
	case "table":
		if b.Table != nil {
			return b.Table
		}
	}
	return b.Value
}

// inlineToMarkdown renders the public part of an InlineElement. Link and image
// URLs are deliberately omitted because DingTalk can return signed/private URLs
// that must not be persisted in indexed Markdown.
func inlineToMarkdown(element *block) string {
	properties := make(map[string]interface{}, len(element.Properties)+5)
	for key, value := range element.Properties {
		properties[key] = value
	}
	if element.Text != "" {
		properties["text"] = element.Text
	}
	if element.Bold {
		properties["bold"] = true
	}
	if element.Italic {
		properties["italic"] = true
	}
	if element.Code {
		properties["code"] = true
	}
	if element.Stike || element.Strikethrough {
		properties["strikethrough"] = true
	}

	switch inlineElementType(element) {
	case "link":
		var sb strings.Builder
		for i := range element.Children {
			sb.WriteString(inlineToMarkdown(&element.Children[i]))
		}
		if text := sb.String(); text != "" {
			return text
		}
		if text, _ := properties["text"].(string); text != "" {
			return applyMarks(text, properties)
		}
		return ""
	case "image":
		return " [image]"
	case "sticker":
		if code, _ := properties["code"].(string); code != "" {
			return "[" + code + "]"
		}
		return ""
	default:
		text, _ := properties["text"].(string)
		return applyMarks(text, properties)
	}
}

// inlineElementType accepts the current elementType field and the inlineType
// field used by older DingTalk responses and recorded fixtures.
func inlineElementType(element *block) string {
	elementType := element.ElementType
	if elementType == "" {
		elementType = element.InlineType
	}
	return strings.ToLower(strings.TrimSpace(elementType))
}

// applyMarks wraps a rich-text run in the Markdown for its inline styles.
func applyMarks(text string, run map[string]interface{}) string {
	if code, _ := run["code"].(bool); code {
		text = "`" + text + "`"
	}
	if bold, _ := run["bold"].(bool); bold {
		text = "**" + text + "**"
	}
	if italic, _ := run["italic"].(bool); italic {
		text = "*" + text + "*"
	}
	strike, _ := run["strikethrough"].(bool)
	if legacyStrike, _ := run["stike"].(bool); legacyStrike {
		strike = true
	}
	if strike {
		text = "~~" + text + "~~"
	}
	return text
}

// blockInt reads a numeric field, tolerating JSON's float64 decoding and
// string-encoded numbers.
func blockInt(b *block, key string) int {
	properties := blockProperties(b)
	if properties == nil {
		return 0
	}
	return interfaceInt(properties[key])
}

func interfaceInt(value interface{}) int {
	switch v := value.(type) {
	case float64:
		return int(v)
	case int:
		return v
	case string:
		n, err := strconv.Atoi(v)
		if err != nil {
			return 0
		}
		return n
	}
	return 0
}

// blockIndent returns the list nesting level, clamped to keep output sane.
func blockIndent(b *block) int {
	properties := blockProperties(b)
	lvl := 0
	if list, ok := properties["list"].(map[string]interface{}); ok {
		lvl = interfaceInt(list["level"])
	}
	if lvl == 0 {
		lvl = blockInt(b, "indentLevel")
	}
	if lvl == 0 {
		lvl = blockInt(b, "indent")
	}
	if lvl < 0 {
		return 0
	}
	if lvl > 8 {
		return 8
	}
	return lvl
}

// sanitizeFileName strips characters that are invalid in filenames and truncates
// on a UTF-8 rune boundary, so a Chinese title cannot produce invalid UTF-8.
func sanitizeFileName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return "untitled"
	}
	replacer := strings.NewReplacer(
		"/", "_", "\\", "_", ":", "_", "*", "_",
		"?", "_", "\"", "_", "<", "_", ">", "_", "|", "_",
		"\n", " ", "\r", " ", "\t", " ",
	)
	result := replacer.Replace(name)
	const maxBytes = 200
	if len(result) > maxBytes {
		result = result[:maxBytes]
		for len(result) > 0 {
			r, size := utf8.DecodeLastRuneInString(result)
			if r != utf8.RuneError || size != 1 {
				break
			}
			result = result[:len(result)-1]
		}
	}
	if result == "" {
		return "untitled"
	}
	return result
}
