package dingtalk

import (
	"fmt"
	"strconv"
	"strings"
)

func renderBlocksMarkdown(blocks []docBlock) string {
	var b strings.Builder
	for _, block := range blocks {
		renderBlock(&b, block, "")
	}
	return strings.TrimSpace(b.String()) + "\n"
}

func renderBlock(out *strings.Builder, block docBlock, quotePrefix string) {
	typ := strings.ToUpper(strings.TrimSpace(block.kind()))
	switch typ {
	case "HEADING", "HEADER", "TITLE":
		text := firstNonEmpty(blockInlineContent(block, block.Heading), blockFallbackText(block))
		if text != "" {
			writePrefixed(out, quotePrefix, strings.Repeat("#", headingLevel(block.Heading.Level))+" "+text)
			out.WriteString("\n")
		}
	case "PARAGRAPH", "TEXT":
		writeParagraph(out, quotePrefix, blockInlineContent(block, block.Paragraph))
	case "BULLET", "BULLET_LIST", "UNORDERED_LIST", "LIST_ITEM":
		writeLine(out, quotePrefix, "- ", blockInlineContent(block, firstNonZeroBlockText(block.Bullet, block.UnorderedList)))
	case "UNORDEREDLIST":
		writeLine(out, quotePrefix, "- ", blockInlineContent(block, block.UnorderedList))
	case "ORDERED", "ORDERED_LIST", "NUMBERED_LIST":
		writeLine(out, quotePrefix, "1. ", blockInlineContent(block, firstNonZeroBlockText(block.Ordered, block.OrderedList)))
	case "ORDEREDLIST":
		writeLine(out, quotePrefix, "1. ", blockInlineContent(block, block.OrderedList))
	case "BLOCKQUOTE", "QUOTE":
		writeParagraph(out, quotePrefix+"> ", firstNonEmpty(blockInlineContent(block, block.Blockquote), blockFallbackText(block)))
	case "CALLOUT", "HIGHLIGHT", "COLUMNS":
		text := blockTextContent(block.Callout)
		if text != "" {
			writeParagraph(out, quotePrefix+"> ", text)
		}
		for _, child := range block.childBlocks() {
			renderBlock(out, child, quotePrefix+"> ")
		}
		return
	case "CODE", "CODE_BLOCK":
		text := firstNonEmpty(blockTextContent(block.Code), blockFallbackText(block))
		if text != "" {
			writePrefixed(out, quotePrefix, "```")
			out.WriteString("\n")
			out.WriteString(text)
			out.WriteString("\n")
			writePrefixed(out, quotePrefix, "```")
			out.WriteString("\n\n")
		}
	case "IMAGE":
		writeImage(out, quotePrefix, mergeMediaProperties(block.Image, block.Properties))
	case "TABLE":
		writeTable(out, quotePrefix, block.Table)
	default:
		writeParagraph(out, quotePrefix, blockFallbackText(block))
	}

	for _, child := range block.childBlocks() {
		renderBlock(out, child, quotePrefix)
	}
}

func writeParagraph(out *strings.Builder, quotePrefix, text string) {
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	writePrefixed(out, quotePrefix, text)
	out.WriteString("\n\n")
}

func writeLine(out *strings.Builder, quotePrefix, prefix, text string) {
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	writePrefixed(out, quotePrefix, prefix+text)
	out.WriteString("\n")
}

func writeImage(out *strings.Builder, quotePrefix string, image mediaBlock) {
	url := imageURL(image)
	if url == "" {
		return
	}
	alt := firstNonEmpty(image.Alt, stringFromMap(image.Properties, "alt"), stringFromMap(image.Properties, "name"), "image")
	writePrefixed(out, quotePrefix, fmt.Sprintf("![%s](%s)", escapeMarkdownAlt(alt), url))
	out.WriteString("\n\n")
}

func writeTable(out *strings.Builder, quotePrefix string, table tableBlock) {
	if len(table.Cells) == 0 {
		return
	}
	writeTableRow(out, quotePrefix, table.Cells[0])
	separator := make([]string, len(table.Cells[0]))
	for i := range separator {
		separator[i] = "---"
	}
	writeTableRow(out, quotePrefix, separator)
	for _, row := range table.Cells[1:] {
		writeTableRow(out, quotePrefix, row)
	}
	out.WriteString("\n")
}

func writeTableRow(out *strings.Builder, quotePrefix string, row []string) {
	cells := make([]string, 0, len(row))
	for _, cell := range row {
		cells = append(cells, escapeTableCell(cell))
	}
	writePrefixed(out, quotePrefix, "| "+strings.Join(cells, " | ")+" |")
	out.WriteString("\n")
}

func writePrefixed(out *strings.Builder, quotePrefix, line string) {
	if quotePrefix == "" {
		out.WriteString(line)
		return
	}
	for i, current := range strings.Split(line, "\n") {
		if i > 0 {
			out.WriteString("\n")
		}
		out.WriteString(quotePrefix)
		out.WriteString(current)
	}
}

func blockInlineContent(block docBlock, value blockText) string {
	inline := inlineElementsMarkdown(block.inlineChildren())
	if inline != "" {
		return inline
	}
	return blockTextContent(value)
}

func blockFallbackText(block docBlock) string {
	if strings.TrimSpace(block.Text) != "" {
		return strings.TrimSpace(block.Text)
	}
	for _, candidate := range []blockText{
		block.Paragraph,
		block.Heading,
		block.Bullet,
		block.Ordered,
		block.OrderedList,
		block.UnorderedList,
		block.Blockquote,
		block.Callout,
		block.Code,
	} {
		if text := blockTextContent(candidate); text != "" {
			return text
		}
	}
	return ""
}

func blockTextContent(value blockText) string {
	if strings.TrimSpace(value.Text) != "" {
		return strings.TrimSpace(value.Text)
	}
	if strings.TrimSpace(value.Content) != "" {
		return strings.TrimSpace(value.Content)
	}
	var parts []string
	for _, el := range append(value.Elements, value.RichTextElements...) {
		if markdown := richTextElementMarkdown(el); markdown != "" {
			parts = append(parts, markdown)
		}
	}
	return strings.TrimSpace(strings.Join(parts, ""))
}

func inlineElementsMarkdown(elements []richTextElement) string {
	var parts []string
	for _, el := range elements {
		if markdown := richTextElementMarkdown(el); markdown != "" {
			parts = append(parts, markdown)
		}
	}
	return strings.TrimSpace(strings.Join(parts, ""))
}

func richTextElementMarkdown(el richTextElement) string {
	typ := strings.ToUpper(firstNonEmpty(el.Type, el.ElementType))
	text := inlineElementsMarkdown(el.Children)
	switch {
	case strings.TrimSpace(el.Text) != "":
		return applyInlineStyle(strings.TrimSpace(el.Text), el)
	case strings.TrimSpace(el.Content) != "":
		return applyInlineStyle(strings.TrimSpace(el.Content), el)
	case strings.TrimSpace(el.TextRun.Content) != "":
		return applyInlineStyle(strings.TrimSpace(el.TextRun.Content), el)
	case typ == "STICKER":
		return stringFromMap(el.Properties, "code")
	case strings.TrimSpace(el.Link.URL) != "":
		text := firstNonEmpty(el.Link.Text, el.Link.URL)
		return fmt.Sprintf("[%s](%s)", text, el.Link.URL)
	case typ == "LINK" && stringFromMap(el.Properties, "href") != "":
		label := firstNonEmpty(text, stringFromMap(el.Properties, "text"), stringFromMap(el.Properties, "href"))
		return fmt.Sprintf("[%s](%s)", label, stringFromMap(el.Properties, "href"))
	case imageURL(el.Image) != "":
		image := mergeMediaProperties(el.Image, el.Properties)
		alt := firstNonEmpty(image.Alt, stringFromMap(image.Properties, "alt"), "image")
		return fmt.Sprintf("![%s](%s)", escapeMarkdownAlt(alt), imageURL(image))
	case typ == "IMAGE" && imageURL(mediaBlock{Properties: el.Properties}) != "":
		image := mediaBlock{Properties: el.Properties}
		alt := firstNonEmpty(stringFromMap(el.Properties, "alt"), stringFromMap(el.Properties, "name"), "image")
		return fmt.Sprintf("![%s](%s)", escapeMarkdownAlt(alt), imageURL(image))
	case strings.TrimSpace(el.URL) != "":
		text := firstNonEmpty(stringFromMap(el.Properties, "text"), el.URL)
		return fmt.Sprintf("[%s](%s)", text, el.URL)
	default:
		return firstNonEmpty(
			text,
			stringFromMap(el.Properties, "text"),
			stringFromMap(el.Properties, "content"),
			stringFromMap(el.Properties, "name"),
		)
	}
}

func mergeMediaProperties(image mediaBlock, properties map[string]interface{}) mediaBlock {
	if len(image.Properties) == 0 {
		image.Properties = properties
		return image
	}
	if len(properties) == 0 {
		return image
	}
	merged := make(map[string]interface{}, len(properties)+len(image.Properties))
	for k, v := range properties {
		merged[k] = v
	}
	for k, v := range image.Properties {
		merged[k] = v
	}
	image.Properties = merged
	return image
}

func imageURL(image mediaBlock) string {
	return firstNonEmpty(
		image.URL,
		image.SourceURL,
		image.ResourceURL,
		stringFromMap(image.Properties, "url"),
		stringFromMap(image.Properties, "src"),
		stringFromMap(image.Properties, "sourceUrl"),
		stringFromMap(image.Properties, "resourceUrl"),
		stringFromMap(image.Properties, "downloadUrl"),
	)
}

func firstNonZeroBlockText(values ...blockText) blockText {
	for _, value := range values {
		if blockTextContent(value) != "" {
			return value
		}
	}
	return blockText{}
}

func stringFromMap(values map[string]interface{}, key string) string {
	if len(values) == 0 {
		return ""
	}
	value, ok := values[key]
	if !ok || value == nil {
		return ""
	}
	switch v := value.(type) {
	case string:
		return strings.TrimSpace(v)
	case fmt.Stringer:
		return strings.TrimSpace(v.String())
	default:
		return strings.TrimSpace(fmt.Sprint(v))
	}
}

func headingLevel(value interface{}) int {
	level := 1
	switch v := value.(type) {
	case int:
		level = v
	case int64:
		level = int(v)
	case float64:
		level = int(v)
	case string:
		raw := strings.TrimSpace(strings.TrimPrefix(strings.ToUpper(v), "H"))
		if i, err := strconv.Atoi(raw); err == nil {
			level = i
		}
	case fmt.Stringer:
		if i, err := strconv.Atoi(v.String()); err == nil {
			level = i
		}
	}
	if level < 1 {
		return 1
	}
	if level > 6 {
		return 6
	}
	return level
}

func escapeMarkdownAlt(text string) string {
	text = strings.ReplaceAll(text, "[", "\\[")
	return strings.ReplaceAll(text, "]", "\\]")
}

func escapeTableCell(text string) string {
	text = strings.ReplaceAll(text, "\n", "<br>")
	return strings.ReplaceAll(text, "|", "\\|")
}

func applyInlineStyle(text string, el richTextElement) string {
	if text == "" {
		return ""
	}
	if strings.EqualFold(el.Fonts, "monospace") {
		text = "`" + text + "`"
	}
	if el.Bold {
		text = "**" + text + "**"
	}
	if el.Italic {
		text = "*" + text + "*"
	}
	if el.Strike || el.Stike {
		text = "~~" + text + "~~"
	}
	return text
}
