package notion

import (
	"encoding/json"
	"testing"
)

func TestRenderRichText(t *testing.T) {
	tests := []struct {
		name     string
		texts    []notionRichText
		expected string
	}{
		{
			name: "plain text",
			texts: []notionRichText{
				{Type: "text", PlainText: "hello", Text: &notionTextContent{Content: "hello"}, Annotations: notionAnnotations{}},
			},
			expected: "hello",
		},
		{
			name: "bold text",
			texts: []notionRichText{
				{Type: "text", PlainText: "bold", Text: &notionTextContent{Content: "bold"}, Annotations: notionAnnotations{Bold: true}},
			},
			expected: "**bold**",
		},
		{
			name: "italic text",
			texts: []notionRichText{
				{Type: "text", PlainText: "italic", Text: &notionTextContent{Content: "italic"}, Annotations: notionAnnotations{Italic: true}},
			},
			expected: "*italic*",
		},
		{
			name: "code text",
			texts: []notionRichText{
				{Type: "text", PlainText: "code", Text: &notionTextContent{Content: "code"}, Annotations: notionAnnotations{Code: true}},
			},
			expected: "`code`",
		},
		{
			name: "strikethrough",
			texts: []notionRichText{
				{Type: "text", PlainText: "del", Text: &notionTextContent{Content: "del"}, Annotations: notionAnnotations{Strikethrough: true}},
			},
			expected: "~~del~~",
		},
		{
			name: "underline",
			texts: []notionRichText{
				{Type: "text", PlainText: "ul", Text: &notionTextContent{Content: "ul"}, Annotations: notionAnnotations{Underline: true}},
			},
			expected: "<u>ul</u>",
		},
		{
			name: "bold italic combo",
			texts: []notionRichText{
				{Type: "text", PlainText: "bi", Text: &notionTextContent{Content: "bi"}, Annotations: notionAnnotations{Bold: true, Italic: true}},
			},
			expected: "***bi***",
		},
		{
			name: "text with link",
			texts: []notionRichText{
				{Type: "text", PlainText: "click", Href: "https://example.com", Text: &notionTextContent{Content: "click"}, Annotations: notionAnnotations{}},
			},
			expected: "[click](https://example.com)",
		},
		{
			name: "equation",
			texts: []notionRichText{
				{Type: "equation", PlainText: "E=mc^2", Equation: &notionEquation{Expression: "E=mc^2"}, Annotations: notionAnnotations{}},
			},
			expected: "$E=mc^2$",
		},
		{
			name: "date mention",
			texts: []notionRichText{
				{Type: "mention", PlainText: "2026-01-15", Mention: &notionMention{
					Type: "date", Date: &notionDateMention{Start: "2026-01-15"},
				}, Annotations: notionAnnotations{}},
			},
			expected: "2026-01-15",
		},
		{
			name: "date mention with end",
			texts: []notionRichText{
				{Type: "mention", PlainText: "range", Mention: &notionMention{
					Type: "date", Date: &notionDateMention{Start: "2026-01-15", End: "2026-01-20"},
				}, Annotations: notionAnnotations{}},
			},
			expected: "2026-01-15 \u2192 2026-01-20",
		},
		{
			name: "multiple segments",
			texts: []notionRichText{
				{Type: "text", PlainText: "hello ", Text: &notionTextContent{Content: "hello "}, Annotations: notionAnnotations{}},
				{Type: "text", PlainText: "world", Text: &notionTextContent{Content: "world"}, Annotations: notionAnnotations{Bold: true}},
			},
			expected: "hello **world**",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := renderRichText(tt.texts)
			if got != tt.expected {
				t.Errorf("renderRichText() = %q, want %q", got, tt.expected)
			}
		})
	}
}

func makeBlock(t *testing.T, blockType string, content interface{}) notionBlock {
	t.Helper()
	raw, err := json.Marshal(content)
	if err != nil {
		t.Fatalf("marshal block content: %v", err)
	}
	return notionBlock{
		ID:         "blk-test",
		Type:       blockType,
		RawContent: raw,
	}
}

func TestBlocksToMarkdown_Paragraph(t *testing.T) {
	blocks := []notionBlock{
		makeBlock(t, "paragraph", map[string]interface{}{
			"rich_text": []notionRichText{
				{Type: "text", PlainText: "Hello world", Text: &notionTextContent{Content: "Hello world"}, Annotations: notionAnnotations{}},
			},
		}),
	}
	md, attachments := BlocksToMarkdown(blocks)
	if md != "Hello world\n\n" {
		t.Errorf("markdown = %q", md)
	}
	if len(attachments) != 0 {
		t.Errorf("expected 0 attachments, got %d", len(attachments))
	}
}

func TestBlocksToMarkdown_Headings(t *testing.T) {
	blocks := []notionBlock{
		makeBlock(t, "heading_1", map[string]interface{}{
			"rich_text": []notionRichText{
				{Type: "text", PlainText: "H1", Text: &notionTextContent{Content: "H1"}, Annotations: notionAnnotations{}},
			},
		}),
		makeBlock(t, "heading_2", map[string]interface{}{
			"rich_text": []notionRichText{
				{Type: "text", PlainText: "H2", Text: &notionTextContent{Content: "H2"}, Annotations: notionAnnotations{}},
			},
		}),
		makeBlock(t, "heading_3", map[string]interface{}{
			"rich_text": []notionRichText{
				{Type: "text", PlainText: "H3", Text: &notionTextContent{Content: "H3"}, Annotations: notionAnnotations{}},
			},
		}),
	}
	md, _ := BlocksToMarkdown(blocks)
	expected := "# H1\n\n## H2\n\n### H3\n\n"
	if md != expected {
		t.Errorf("markdown = %q, want %q", md, expected)
	}
}

func TestBlocksToMarkdown_Code(t *testing.T) {
	blocks := []notionBlock{
		makeBlock(t, "code", map[string]interface{}{
			"language": "go",
			"rich_text": []notionRichText{
				{Type: "text", PlainText: "fmt.Println()", Text: &notionTextContent{Content: "fmt.Println()"}, Annotations: notionAnnotations{}},
			},
		}),
	}
	md, _ := BlocksToMarkdown(blocks)
	expected := "```go\nfmt.Println()\n```\n\n"
	if md != expected {
		t.Errorf("markdown = %q, want %q", md, expected)
	}
}

func TestBlocksToMarkdown_List(t *testing.T) {
	blocks := []notionBlock{
		makeBlock(t, "bulleted_list_item", map[string]interface{}{
			"rich_text": []notionRichText{
				{Type: "text", PlainText: "Item 1", Text: &notionTextContent{Content: "Item 1"}, Annotations: notionAnnotations{}},
			},
		}),
		makeBlock(t, "bulleted_list_item", map[string]interface{}{
			"rich_text": []notionRichText{
				{Type: "text", PlainText: "Item 2", Text: &notionTextContent{Content: "Item 2"}, Annotations: notionAnnotations{}},
			},
		}),
	}
	md, _ := BlocksToMarkdown(blocks)
	expected := "- Item 1\n- Item 2\n\n"
	if md != expected {
		t.Errorf("markdown = %q, want %q", md, expected)
	}
}

func TestBlocksToMarkdown_ToDo(t *testing.T) {
	blocks := []notionBlock{
		makeBlock(t, "to_do", map[string]interface{}{
			"checked": false,
			"rich_text": []notionRichText{
				{Type: "text", PlainText: "Unchecked", Text: &notionTextContent{Content: "Unchecked"}, Annotations: notionAnnotations{}},
			},
		}),
		makeBlock(t, "to_do", map[string]interface{}{
			"checked": true,
			"rich_text": []notionRichText{
				{Type: "text", PlainText: "Checked", Text: &notionTextContent{Content: "Checked"}, Annotations: notionAnnotations{}},
			},
		}),
	}
	md, _ := BlocksToMarkdown(blocks)
	expected := "- [ ] Unchecked\n- [x] Checked\n\n"
	if md != expected {
		t.Errorf("markdown = %q, want %q", md, expected)
	}
}

func TestBlocksToMarkdown_Quote(t *testing.T) {
	blocks := []notionBlock{
		makeBlock(t, "quote", map[string]interface{}{
			"rich_text": []notionRichText{
				{Type: "text", PlainText: "A wise quote", Text: &notionTextContent{Content: "A wise quote"}, Annotations: notionAnnotations{}},
			},
		}),
	}
	md, _ := BlocksToMarkdown(blocks)
	expected := "> A wise quote\n\n"
	if md != expected {
		t.Errorf("markdown = %q, want %q", md, expected)
	}
}

func TestBlocksToMarkdown_Divider(t *testing.T) {
	blocks := []notionBlock{
		{ID: "blk-div", Type: "divider"},
	}
	md, _ := BlocksToMarkdown(blocks)
	if md != "---\n\n" {
		t.Errorf("markdown = %q", md)
	}
}

func TestBlocksToMarkdown_Equation(t *testing.T) {
	blocks := []notionBlock{
		makeBlock(t, "equation", map[string]interface{}{
			"expression": "E = mc^2",
		}),
	}
	md, _ := BlocksToMarkdown(blocks)
	expected := "$$E = mc^2$$\n\n"
	if md != expected {
		t.Errorf("markdown = %q, want %q", md, expected)
	}
}

func TestBlocksToMarkdown_Image(t *testing.T) {
	blocks := []notionBlock{
		makeBlock(t, "image", map[string]interface{}{
			"type": "file",
			"file": map[string]interface{}{
				"url":         "https://s3.example.com/img.png",
				"expiry_time": "2026-01-15T11:00:00.000Z",
			},
			"caption": []notionRichText{
				{Type: "text", PlainText: "A photo", Text: &notionTextContent{Content: "A photo"}, Annotations: notionAnnotations{}},
			},
		}),
	}
	md, attachments := BlocksToMarkdown(blocks)
	if md != "![A photo](https://s3.example.com/img.png)\n\n" {
		t.Errorf("markdown = %q", md)
	}
	if len(attachments) != 1 {
		t.Fatalf("expected 1 attachment, got %d", len(attachments))
	}
	if attachments[0].URL != "https://s3.example.com/img.png" {
		t.Errorf("attachment URL = %q", attachments[0].URL)
	}
	if attachments[0].Type != "image" {
		t.Errorf("attachment Type = %q", attachments[0].Type)
	}
}

func TestBlocksToMarkdown_UnknownType(t *testing.T) {
	blocks := []notionBlock{
		{ID: "blk-unknown", Type: "future_block_type"},
	}
	md, _ := BlocksToMarkdown(blocks)
	if md != "" {
		t.Errorf("unknown block should produce empty output, got %q", md)
	}
}

func TestBlocksToMarkdown_Table(t *testing.T) {
	tableBlock := notionBlock{
		ID:          "blk-table",
		Type:        "table",
		HasChildren: true,
		RawContent:  json.RawMessage(`{"table_width": 2, "has_column_header": true}`),
		Children: []notionBlock{
			makeBlock(t, "table_row", map[string]interface{}{
				"cells": [][]notionRichText{
					{{Type: "text", PlainText: "Name", Text: &notionTextContent{Content: "Name"}, Annotations: notionAnnotations{}}},
					{{Type: "text", PlainText: "Age", Text: &notionTextContent{Content: "Age"}, Annotations: notionAnnotations{}}},
				},
			}),
			makeBlock(t, "table_row", map[string]interface{}{
				"cells": [][]notionRichText{
					{{Type: "text", PlainText: "Alice", Text: &notionTextContent{Content: "Alice"}, Annotations: notionAnnotations{}}},
					{{Type: "text", PlainText: "30", Text: &notionTextContent{Content: "30"}, Annotations: notionAnnotations{}}},
				},
			}),
		},
	}
	md, _ := BlocksToMarkdown([]notionBlock{tableBlock})
	expected := "| Name | Age |\n| --- | --- |\n| Alice | 30 |\n\n"
	if md != expected {
		t.Errorf("markdown = %q, want %q", md, expected)
	}
}

func TestBlocksToMarkdown_NestedList(t *testing.T) {
	parent := makeBlock(t, "bulleted_list_item", map[string]interface{}{
		"rich_text": []notionRichText{
			{Type: "text", PlainText: "Parent", Text: &notionTextContent{Content: "Parent"}, Annotations: notionAnnotations{}},
		},
	})
	parent.HasChildren = true
	parent.Children = []notionBlock{
		makeBlock(t, "bulleted_list_item", map[string]interface{}{
			"rich_text": []notionRichText{
				{Type: "text", PlainText: "Child", Text: &notionTextContent{Content: "Child"}, Annotations: notionAnnotations{}},
			},
		}),
	}

	md, _ := BlocksToMarkdown([]notionBlock{parent})
	expected := "- Parent\n  - Child\n\n"
	if md != expected {
		t.Errorf("markdown = %q, want %q", md, expected)
	}
}
