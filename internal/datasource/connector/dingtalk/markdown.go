package dingtalk

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

const (
	blockTypeHeading       = "heading"
	blockTypeParagraph     = "paragraph"
	blockTypeBlockquote    = "blockquote"
	blockTypeOrderedList   = "orderedList"
	blockTypeUnorderedList = "unorderedList"
	blockTypeCallout       = "callout"
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
	case blockTypeHeading:
		writeHeading(out, block, text)
	case blockTypeBlockquote:
		writeBlockquote(out, text)
	case blockTypeOrderedList:
		writeListItem(out, depth, "1. ", text)
	case blockTypeUnorderedList:
		writeListItem(out, depth, "- ", text)
	case blockTypeCallout:
		writeCallout(out, text)
	case blockTypeParagraph:
		writeParagraph(out, text)
	default:
		writeParagraph(out, text)
	}

	for _, child := range parseChildren(block.Children) {
		renderBlock(out, child, depth+1)
	}
}

func writeHeading(out *bytes.Buffer, block docBlock, text string) {
	if text == "" {
		return
	}
	level := normalizedHeadingLevel(block)
	out.WriteString(strings.Repeat("#", level))
	out.WriteByte(' ')
	out.WriteString(text)
	out.WriteString("\n\n")
}

func normalizedHeadingLevel(block docBlock) int {
	level := 2
	if block.Heading != nil && block.Heading.Level > 0 {
		level = int(block.Heading.Level)
	}
	if level < 1 {
		return 1
	}
	if level > 6 {
		return 6
	}
	return level
}

func writeBlockquote(out *bytes.Buffer, text string) {
	if text == "" {
		return
	}
	for _, line := range strings.Split(text, "\n") {
		out.WriteString("> ")
		out.WriteString(strings.TrimSpace(line))
		out.WriteByte('\n')
	}
	out.WriteByte('\n')
}

func writeListItem(out *bytes.Buffer, depth int, marker, text string) {
	if text == "" {
		return
	}
	out.WriteString(indent(depth))
	out.WriteString(marker)
	out.WriteString(text)
	out.WriteByte('\n')
}

func writeCallout(out *bytes.Buffer, text string) {
	if text == "" {
		return
	}
	out.WriteString("> ")
	out.WriteString(text)
	out.WriteString("\n\n")
}

func writeParagraph(out *bytes.Buffer, text string) {
	if text == "" {
		return
	}
	out.WriteString(text)
	out.WriteString("\n\n")
}

func blockText(block docBlock) string {
	switch block.BlockType {
	case blockTypeHeading:
		if block.Heading != nil {
			return block.Heading.Text
		}
	case blockTypeParagraph:
		if block.Paragraph != nil {
			return block.Paragraph.Text
		}
	case blockTypeBlockquote:
		if block.Blockquote != nil {
			return block.Blockquote.Text
		}
	case blockTypeOrderedList:
		if block.OrderedList != nil {
			return block.OrderedList.Text
		}
	case blockTypeUnorderedList:
		if block.UnorderedList != nil {
			return block.UnorderedList.Text
		}
	case blockTypeCallout:
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
			if isBlockMetadataKey(key) {
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

func isBlockMetadataKey(key string) bool {
	switch key {
	case "id", "index", "blockType":
		return true
	default:
		return false
	}
}

func indent(depth int) string {
	if depth <= 0 {
		return ""
	}
	return strings.Repeat("  ", depth)
}
