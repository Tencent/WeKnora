package notion

import (
	"encoding/json"
	"fmt"
	"strings"
)

// BlocksToMarkdown converts a tree of Notion blocks into Markdown text,
// and collects attachment references (image, file, pdf, video, audio).
func BlocksToMarkdown(blocks []notionBlock) (string, []attachment) {
	var b strings.Builder
	var attachments []attachment
	renderBlocks(&b, blocks, 0, &attachments)
	// Collapse 3+ consecutive newlines into 2 (avoids excessive blank lines from empty paragraphs)
	result := b.String()
	for strings.Contains(result, "\n\n\n") {
		result = strings.ReplaceAll(result, "\n\n\n", "\n\n")
	}
	return strings.TrimSpace(result) + "\n", attachments
}

// renderBlocks renders a list of blocks at the given indentation depth.
func renderBlocks(b *strings.Builder, blocks []notionBlock, depth int, attachments *[]attachment) {
	inList := false

	for i, block := range blocks {
		isList := block.Type == "bulleted_list_item" || block.Type == "numbered_list_item" || block.Type == "to_do"

		// End list spacing: if previous was list and current is not
		if inList && !isList {
			b.WriteString("\n")
			inList = false
		}

		renderBlock(b, block, depth, attachments, i, blocks)

		if isList {
			inList = true
		}
	}

	// Close trailing list
	if inList {
		b.WriteString("\n")
	}
}

// renderBlock renders a single block to Markdown.
func renderBlock(b *strings.Builder, block notionBlock, depth int, attachments *[]attachment, index int, siblings []notionBlock) {
	indent := strings.Repeat("  ", depth)

	switch block.Type {
	case "paragraph":
		rt := extractRichText(block.RawContent)
		text := renderRichText(rt)
		b.WriteString(indent + text + "\n\n")

	case "heading_1", "heading_2", "heading_3", "heading_4":
		level := int(block.Type[len(block.Type)-1] - '0')
		rt := extractRichText(block.RawContent)
		b.WriteString(strings.Repeat("#", level) + " " + renderRichText(rt) + "\n\n")
		if len(block.Children) > 0 {
			renderBlocks(b, block.Children, depth, attachments)
		}

	case "bulleted_list_item":
		rt := extractRichText(block.RawContent)
		b.WriteString(indent + "- " + renderRichText(rt) + "\n")
		if len(block.Children) > 0 {
			renderBlocks(b, block.Children, depth+1, attachments)
		}

	case "numbered_list_item":
		rt := extractRichText(block.RawContent)
		// Find position among consecutive numbered_list_items
		num := 1
		for j := index - 1; j >= 0 && siblings[j].Type == "numbered_list_item"; j-- {
			num++
		}
		b.WriteString(fmt.Sprintf("%s%d. %s\n", indent, num, renderRichText(rt)))
		if len(block.Children) > 0 {
			renderBlocks(b, block.Children, depth+1, attachments)
		}

	case "to_do":
		rt := extractRichText(block.RawContent)
		checked := extractBool(block.RawContent, "checked")
		checkbox := "[ ]"
		if checked {
			checkbox = "[x]"
		}
		b.WriteString(fmt.Sprintf("%s- %s %s\n", indent, checkbox, renderRichText(rt)))

	case "toggle":
		rt := extractRichText(block.RawContent)
		b.WriteString(fmt.Sprintf("<details><summary>%s</summary>\n\n", renderRichText(rt)))
		if len(block.Children) > 0 {
			renderBlocks(b, block.Children, depth, attachments)
		}
		b.WriteString("</details>\n\n")

	case "code":
		rt := extractRichText(block.RawContent)
		lang := extractString(block.RawContent, "language")
		b.WriteString(fmt.Sprintf("```%s\n%s\n```\n\n", lang, renderRichText(rt)))

	case "quote", "meeting_notes":
		rt := extractRichText(block.RawContent)
		text := renderRichText(rt)
		for _, line := range strings.Split(text, "\n") {
			b.WriteString("> " + line + "\n")
		}
		b.WriteString("\n")
		if len(block.Children) > 0 {
			renderBlocks(b, block.Children, depth, attachments)
		}

	case "callout":
		rt := extractRichText(block.RawContent)
		icon := extractIcon(block.RawContent)
		prefix := ""
		if icon != "" {
			prefix = icon + " "
		}
		b.WriteString("> " + prefix + renderRichText(rt) + "\n\n")
		if len(block.Children) > 0 {
			renderBlocks(b, block.Children, depth, attachments)
		}

	case "divider":
		b.WriteString("---\n\n")

	case "equation":
		expr := extractString(block.RawContent, "expression")
		b.WriteString("$$" + expr + "$$\n\n")

	case "table":
		renderTable(b, block)

	case "image":
		file, caption := extractFileAndCaption(block.RawContent)
		url := file.GetURL()
		b.WriteString(fmt.Sprintf("![%s](%s)\n\n", caption, url))
		if url != "" {
			*attachments = append(*attachments, attachment{
				URL:      url,
				FileName: fileNameFromURL(url, "image"),
				Type:     "image",
			})
		}

	case "file", "pdf", "video", "audio":
		renderMediaBlock(b, block, attachments)

	case "bookmark", "link_preview":
		url := extractString(block.RawContent, "url")
		caption := ""
		if rt := extractCaptionText(block.RawContent); rt != "" {
			caption = rt
		} else {
			caption = url
		}
		b.WriteString(fmt.Sprintf("[%s](%s)\n\n", caption, url))

	case "embed":
		url := extractString(block.RawContent, "url")
		b.WriteString(fmt.Sprintf("[%s](%s)\n\n", url, url))

	case "link_to_page":
		// Render as a link; the actual page title is not available in block data
		pageID := extractLinkToPageID(block.RawContent)
		b.WriteString(fmt.Sprintf("[Page %s](https://notion.so/%s)\n\n", pageID, strings.ReplaceAll(pageID, "-", "")))

	case "synced_block":
		// Render children (the synced content)
		if len(block.Children) > 0 {
			renderBlocks(b, block.Children, depth, attachments)
		}

	case "column_list":
		// Render each column sequentially
		if len(block.Children) > 0 {
			for _, col := range block.Children {
				if len(col.Children) > 0 {
					renderBlocks(b, col.Children, depth, attachments)
				}
			}
		}

	case "column":
		// Handled by column_list
		if len(block.Children) > 0 {
			renderBlocks(b, block.Children, depth, attachments)
		}

	case "tab_list":
		// Render each tab's content sequentially (similar to column_list)
		if len(block.Children) > 0 {
			for _, tab := range block.Children {
				if len(tab.Children) > 0 {
					renderBlocks(b, tab.Children, depth, attachments)
				}
			}
		}

	case "tab":
		// Render tab content sequentially
		if len(block.Children) > 0 {
			renderBlocks(b, block.Children, depth, attachments)
		}

	case "child_page":
		// Render as a link in the parent page so it's not empty.
		// The child page itself is fetched as a separate knowledge item by connector layer.
		title := extractString(block.RawContent, "title")
		if title == "" {
			title = "Untitled"
		}
		b.WriteString(fmt.Sprintf("- [%s](https://notion.so/%s)\n", title, strings.ReplaceAll(block.ID, "-", "")))

	case "child_database":
		title := extractString(block.RawContent, "title")
		if title == "" {
			title = "Database"
		}
		b.WriteString(fmt.Sprintf("- [%s](https://notion.so/%s)\n", title, strings.ReplaceAll(block.ID, "-", "")))

	case "table_of_contents", "breadcrumb", "template", "table_row", "unsupported":
		// Skip — no meaningful content

	default:
		// Unknown block type — skip silently for forward compatibility
	}
}

// renderMediaBlock renders a file/pdf/video/audio block as a Markdown link and collects the attachment.
func renderMediaBlock(b *strings.Builder, block notionBlock, attachments *[]attachment) {
	file, _ := extractFileAndCaption(block.RawContent)
	url := file.GetURL()
	name := file.Name
	if name == "" {
		name = fileNameFromURL(url, block.Type)
	}
	b.WriteString(fmt.Sprintf("[%s](%s)\n\n", name, url))
	if url != "" {
		*attachments = append(*attachments, attachment{
			URL:      url,
			FileName: name,
			Type:     block.Type,
		})
	}
}

// renderTable converts a table block (with table_row children) to Markdown.
func renderTable(b *strings.Builder, block notionBlock) {
	if len(block.Children) == 0 {
		return
	}

	for i, row := range block.Children {
		cells := extractTableCells(row.RawContent)
		var parts []string
		for _, cell := range cells {
			parts = append(parts, strings.ReplaceAll(renderRichText(cell), "|", "\\|"))
		}
		b.WriteString("| " + strings.Join(parts, " | ") + " |\n")

		// Markdown tables always need a separator after the first row
		if i == 0 {
			var sep []string
			for range parts {
				sep = append(sep, "---")
			}
			b.WriteString("| " + strings.Join(sep, " | ") + " |\n")
		}
	}
	b.WriteString("\n")
}

// --- Rich text rendering ---

// renderRichText converts a slice of rich text elements to Markdown.
func renderRichText(texts []notionRichText) string {
	var b strings.Builder
	for _, rt := range texts {
		text := richTextToString(rt)
		text = applyAnnotations(text, rt.Annotations)
		if rt.Href != "" && !rt.Annotations.Code {
			text = fmt.Sprintf("[%s](%s)", text, rt.Href)
		}
		b.WriteString(text)
	}
	return b.String()
}

// richTextToString extracts the base text from a rich text element.
func richTextToString(rt notionRichText) string {
	switch rt.Type {
	case "text":
		if rt.Text != nil {
			return rt.Text.Content
		}
		return rt.PlainText

	case "mention":
		if rt.Mention == nil {
			return rt.PlainText
		}
		switch rt.Mention.Type {
		case "date":
			if rt.Mention.Date != nil {
				if rt.Mention.Date.End != "" {
					return rt.Mention.Date.Start + " \u2192 " + rt.Mention.Date.End
				}
				return rt.Mention.Date.Start
			}
		case "page":
			if rt.Mention.Page != nil {
				return rt.PlainText // Notion fills PlainText with page title
			}
		case "database", "data_source":
			if rt.Mention.Database != nil {
				return rt.PlainText
			}
		case "link_preview":
			if rt.Mention.LinkPreview != nil {
				return rt.Mention.LinkPreview.URL
			}
		}
		return rt.PlainText

	case "equation":
		if rt.Equation != nil {
			return "$" + rt.Equation.Expression + "$"
		}
		return rt.PlainText

	default:
		return rt.PlainText
	}
}

// applyAnnotations wraps text with Markdown formatting based on annotations.
func applyAnnotations(text string, ann notionAnnotations) string {
	if text == "" {
		return text
	}
	if ann.Code {
		text = "`" + text + "`"
	}
	if ann.Bold && ann.Italic {
		text = "***" + text + "***"
	} else if ann.Bold {
		text = "**" + text + "**"
	} else if ann.Italic {
		text = "*" + text + "*"
	}
	if ann.Strikethrough {
		text = "~~" + text + "~~"
	}
	if ann.Underline {
		text = "<u>" + text + "</u>"
	}
	return text
}

// --- Raw content extraction helpers ---

func extractRichText(raw json.RawMessage) []notionRichText {
	if raw == nil {
		return nil
	}
	var content struct {
		RichText []notionRichText `json:"rich_text"`
	}
	json.Unmarshal(raw, &content)
	return content.RichText
}

func extractString(raw json.RawMessage, key string) string {
	if raw == nil {
		return ""
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return ""
	}
	val, ok := m[key]
	if !ok {
		return ""
	}
	var s string
	json.Unmarshal(val, &s)
	return s
}

func extractBool(raw json.RawMessage, key string) bool {
	if raw == nil {
		return false
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return false
	}
	val, ok := m[key]
	if !ok {
		return false
	}
	var b bool
	json.Unmarshal(val, &b)
	return b
}

func extractIcon(raw json.RawMessage) string {
	if raw == nil {
		return ""
	}
	var content struct {
		Icon *struct {
			Type  string `json:"type"`
			Emoji string `json:"emoji,omitempty"`
		} `json:"icon"`
	}
	json.Unmarshal(raw, &content)
	if content.Icon != nil && content.Icon.Type == "emoji" {
		return content.Icon.Emoji
	}
	return ""
}

func extractFileAndCaption(raw json.RawMessage) (notionFile, string) {
	var file notionFile
	if raw != nil {
		json.Unmarshal(raw, &file)
	}
	captionText := ""
	if len(file.Caption) > 0 {
		captionText = renderRichText(file.Caption)
	}
	return file, captionText
}

func extractCaptionText(raw json.RawMessage) string {
	if raw == nil {
		return ""
	}
	var content struct {
		Caption []notionRichText `json:"caption"`
	}
	json.Unmarshal(raw, &content)
	if len(content.Caption) > 0 {
		return renderRichText(content.Caption)
	}
	return ""
}

func extractTableCells(raw json.RawMessage) [][]notionRichText {
	if raw == nil {
		return nil
	}
	var content struct {
		Cells [][]notionRichText `json:"cells"`
	}
	json.Unmarshal(raw, &content)
	return content.Cells
}

func extractLinkToPageID(raw json.RawMessage) string {
	if raw == nil {
		return ""
	}
	var content struct {
		Type   string `json:"type"`
		PageID string `json:"page_id,omitempty"`
	}
	json.Unmarshal(raw, &content)
	return content.PageID
}

func fileNameFromURL(url, fallbackType string) string {
	if url == "" {
		return fallbackType
	}
	// Extract filename from URL path (before query params)
	path := url
	if idx := strings.Index(path, "?"); idx >= 0 {
		path = path[:idx]
	}
	if idx := strings.LastIndex(path, "/"); idx >= 0 {
		name := path[idx+1:]
		if name != "" {
			return name
		}
	}
	return fallbackType
}
