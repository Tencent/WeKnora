package dingtalk

import (
	"fmt"
	"strconv"
	"strings"
)

func renderBlocksMarkdown(blocks []docBlock) string {
	var b strings.Builder
	for _, block := range blocks {
		renderBlock(&b, block)
	}
	return strings.TrimSpace(b.String()) + "\n"
}

func renderBlock(out *strings.Builder, block docBlock) {
	typ := strings.ToUpper(strings.TrimSpace(block.kind()))
	switch typ {
	case "HEADING", "HEADER", "TITLE":
		text := blockTextContent(block.Heading)
		if text == "" {
			text = blockFallbackText(block)
		}
		if text != "" {
			out.WriteString(strings.Repeat("#", headingLevel(block.Heading.Level)))
			out.WriteString(" ")
			out.WriteString(text)
			out.WriteString("\n\n")
		}
	case "PARAGRAPH", "TEXT":
		writeParagraph(out, blockTextContent(block.Paragraph))
	case "BULLET", "BULLET_LIST", "UNORDERED_LIST", "LIST_ITEM":
		writeLine(out, "- ", blockTextContent(block.Bullet))
	case "ORDERED", "ORDERED_LIST", "NUMBERED_LIST":
		writeLine(out, "1. ", blockTextContent(block.Ordered))
	case "BLOCKQUOTE", "QUOTE":
		writeLine(out, "> ", blockTextContent(block.Blockquote))
	case "CALLOUT":
		writeLine(out, "> ", blockTextContent(block.Callout))
	default:
		writeParagraph(out, blockFallbackText(block))
	}

	for _, child := range block.Children {
		renderBlock(out, child)
	}
}

func writeParagraph(out *strings.Builder, text string) {
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	out.WriteString(text)
	out.WriteString("\n\n")
}

func writeLine(out *strings.Builder, prefix, text string) {
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	out.WriteString(prefix)
	out.WriteString(text)
	out.WriteString("\n")
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
		block.Blockquote,
		block.Callout,
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
		switch {
		case strings.TrimSpace(el.Text) != "":
			parts = append(parts, strings.TrimSpace(el.Text))
		case strings.TrimSpace(el.Content) != "":
			parts = append(parts, strings.TrimSpace(el.Content))
		case strings.TrimSpace(el.TextRun.Content) != "":
			parts = append(parts, strings.TrimSpace(el.TextRun.Content))
		}
	}
	return strings.TrimSpace(strings.Join(parts, ""))
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
