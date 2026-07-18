package dingtalk

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// blocksToMarkdown converts the block-oriented DingTalk document response into
// stable Markdown suitable for indexing. DingTalk can add new block types over
// time, so unknown blocks fall back to their recursively extracted text rather
// than making the entire document unsyncable.
func blocksToMarkdown(blocks []json.RawMessage) (string, error) {
	parts := make([]string, 0, len(blocks))
	for index, raw := range blocks {
		var block map[string]interface{}
		if err := json.Unmarshal(raw, &block); err != nil {
			return "", fmt.Errorf("decode block %d: %w", index, err)
		}
		if rendered := strings.TrimSpace(renderBlock(block)); rendered != "" {
			parts = append(parts, rendered)
		}
	}
	return strings.TrimSpace(strings.Join(parts, "\n\n")), nil
}

func renderBlock(block map[string]interface{}) string {
	blockType, _ := block["blockType"].(string)
	normalizedType := strings.ToLower(blockType)
	if normalizedType == "divider" || normalizedType == "horizontalrule" {
		return "---"
	}
	text := extractBlockText(block)
	if text == "" {
		return ""
	}

	switch normalizedType {
	case "heading":
		level := headingLevel(block)
		return strings.Repeat("#", level) + " " + text
	case "unorderedlist", "bullet", "bulletedlist":
		return prefixLines(text, "- ")
	case "orderedlist", "numberedlist":
		return prefixLines(text, "1. ")
	case "blockquote", "quote":
		return prefixLines(text, "> ")
	case "code", "codeblock":
		language := nestedString(block, "code", "language")
		return "```" + language + "\n" + text + "\n```"
	default:
		return text
	}
}

func headingLevel(block map[string]interface{}) int {
	value := nestedValue(block, "heading", "level")
	var level int
	switch typed := value.(type) {
	case float64:
		level = int(typed)
	case json.Number:
		level, _ = strconv.Atoi(typed.String())
	case string:
		level, _ = strconv.Atoi(typed)
	}
	if level < 1 || level > 6 {
		return 1
	}
	return level
}

func prefixLines(text, prefix string) string {
	lines := strings.Split(text, "\n")
	for index, line := range lines {
		lines[index] = prefix + line
	}
	return strings.Join(lines, "\n")
}

// extractBlockText prioritizes the known content-bearing fields. The fallback
// only inspects semantically textual keys, avoiding block IDs and other API
// metadata leaking into indexed content.
func extractBlockText(block map[string]interface{}) string {
	for _, key := range []string{"children", "elements"} {
		if value, ok := block[key]; ok {
			if text := extractText(value); text != "" {
				return text
			}
		}
	}
	blockType, _ := block["blockType"].(string)
	if value, ok := block[blockType]; ok {
		if text := extractText(value); text != "" {
			return text
		}
	}
	for _, key := range []string{
		"heading", "paragraph", "unorderedList", "orderedList", "blockquote",
		"code", "table", "content", "text", "value",
	} {
		if value, ok := block[key]; ok {
			if text := extractText(value); text != "" {
				return text
			}
		}
	}
	return ""
}

func extractText(value interface{}) string {
	switch typed := value.(type) {
	case string:
		return typed
	case []interface{}:
		parts := make([]string, 0, len(typed))
		for _, item := range typed {
			if text := extractText(item); strings.TrimSpace(text) != "" {
				parts = append(parts, text)
			}
		}
		return strings.Join(parts, "")
	case map[string]interface{}:
		// Inline text runs may include a link mark. Preserve the target when it
		// is available without depending on one exact marks schema.
		if rawText, ok := typed["text"].(string); ok {
			if href := findHref(typed["marks"]); href != "" {
				return "[" + rawText + "](" + href + ")"
			}
			return rawText
		}
		for _, key := range []string{"children", "elements", "content", "value", "rows", "cells"} {
			if child, ok := typed[key]; ok {
				if text := extractText(child); text != "" {
					return text
				}
			}
		}
		// Deterministic fallback for new wrapper names.
		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		var parts []string
		for _, key := range keys {
			lower := strings.ToLower(key)
			if lower == "text" || lower == "content" || lower == "value" || strings.HasSuffix(lower, "children") {
				if text := extractText(typed[key]); text != "" {
					parts = append(parts, text)
				}
			}
		}
		return strings.Join(parts, "")
	default:
		return ""
	}
}

func findHref(value interface{}) string {
	switch typed := value.(type) {
	case []interface{}:
		for _, item := range typed {
			if href := findHref(item); href != "" {
				return href
			}
		}
	case map[string]interface{}:
		for _, key := range []string{"href", "url"} {
			if href, ok := typed[key].(string); ok {
				return href
			}
		}
		if attrs, ok := typed["attrs"]; ok {
			return findHref(attrs)
		}
	}
	return ""
}

func nestedValue(root map[string]interface{}, path ...string) interface{} {
	var current interface{} = root
	for _, key := range path {
		object, ok := current.(map[string]interface{})
		if !ok {
			return nil
		}
		current = object[key]
	}
	return current
}

func nestedString(root map[string]interface{}, path ...string) string {
	value, _ := nestedValue(root, path...).(string)
	return value
}
