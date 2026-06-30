package dingtalk

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

func renderBlocksMarkdown(title string, blocks []docBlock) string {
	var out bytes.Buffer
	if strings.TrimSpace(title) != "" {
		out.WriteString("# ")
		out.WriteString(strings.TrimSpace(title))
		out.WriteString("\n\n")
	}

	sort.SliceStable(blocks, func(i, j int) bool { return blocks[i].Index < blocks[j].Index })
	for _, block := range blocks {
		renderBlock(&out, block, 0)
	}
	return strings.TrimSpace(out.String()) + "\n"
}

func renderBlock(out *bytes.Buffer, block docBlock, depth int) {
	text := strings.TrimSpace(blockText(block))
	switch block.BlockType {
	case "heading":
		if text != "" {
			level := 2
			if block.Heading != nil && block.Heading.Level > 0 {
				level = block.Heading.Level
			}
			if level < 1 {
				level = 1
			}
			if level > 6 {
				level = 6
			}
			out.WriteString(strings.Repeat("#", level))
			out.WriteByte(' ')
			out.WriteString(text)
			out.WriteString("\n\n")
		}
	case "blockquote":
		if text != "" {
			for _, line := range strings.Split(text, "\n") {
				out.WriteString("> ")
				out.WriteString(strings.TrimSpace(line))
				out.WriteByte('\n')
			}
			out.WriteByte('\n')
		}
	case "orderedList":
		if text != "" {
			out.WriteString(indent(depth))
			out.WriteString("1. ")
			out.WriteString(text)
			out.WriteByte('\n')
		}
	case "unorderedList":
		if text != "" {
			out.WriteString(indent(depth))
			out.WriteString("- ")
			out.WriteString(text)
			out.WriteByte('\n')
		}
	case "callout":
		if text != "" {
			out.WriteString("> ")
			out.WriteString(text)
			out.WriteString("\n\n")
		}
	case "paragraph":
		if text != "" {
			out.WriteString(text)
			out.WriteString("\n\n")
		}
	default:
		if text != "" {
			out.WriteString(text)
			out.WriteString("\n\n")
		}
	}

	for _, child := range parseChildren(block.Children) {
		renderBlock(out, child, depth+1)
	}
}

func blockText(block docBlock) string {
	switch block.BlockType {
	case "heading":
		if block.Heading != nil {
			return block.Heading.Text
		}
	case "paragraph":
		if block.Paragraph != nil {
			return block.Paragraph.Text
		}
	case "blockquote":
		if block.Blockquote != nil {
			return block.Blockquote.Text
		}
	case "orderedList":
		if block.OrderedList != nil {
			return block.OrderedList.Text
		}
	case "unorderedList":
		if block.UnorderedList != nil {
			return block.UnorderedList.Text
		}
	case "callout":
		if block.Callout != nil {
			return block.Callout.Text
		}
	}
	return extractTextFromJSON(block)
}

func parseChildren(raw json.RawMessage) []docBlock {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var children []docBlock
	if err := json.Unmarshal(raw, &children); err == nil {
		return children
	}
	var wrapper struct {
		Data []docBlock `json:"data"`
	}
	if err := json.Unmarshal(raw, &wrapper); err == nil {
		return wrapper.Data
	}
	return nil
}

func extractTextFromJSON(v interface{}) string {
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	var decoded interface{}
	if err := json.Unmarshal(b, &decoded); err != nil {
		return ""
	}
	parts := collectText(decoded, nil)
	return strings.Join(parts, "")
}

func collectText(v interface{}, parts []string) []string {
	switch value := v.(type) {
	case map[string]interface{}:
		if raw, ok := value["text"].(string); ok {
			return append(parts, raw)
		}
		keys := make([]string, 0, len(value))
		for key := range value {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			if key == "id" || key == "index" || key == "blockType" {
				continue
			}
			parts = collectText(value[key], parts)
		}
	case []interface{}:
		for _, item := range value {
			parts = collectText(item, parts)
		}
	case string:
		parts = append(parts, value)
	case float64:
		if value == float64(int64(value)) {
			parts = append(parts, fmt.Sprintf("%d", int64(value)))
		}
	}
	return parts
}

func indent(depth int) string {
	if depth <= 0 {
		return ""
	}
	return strings.Repeat("  ", depth)
}
