package dingtalk

import (
	"encoding/json"
	"strings"
	"testing"
)

func blockJSON(t *testing.T, value interface{}) json.RawMessage {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestRenderDocumentUsesOfficialBlockAndInlineStructures(t *testing.T) {
	blocks := []json.RawMessage{
		blockJSON(t, map[string]interface{}{
			"blockType": "heading",
			"heading":   map[string]interface{}{"level": 2, "text": "summary heading"},
			"children":  []interface{}{map[string]interface{}{"text": "Rich heading", "bold": true}},
		}),
		blockJSON(t, map[string]interface{}{
			"blockType": "paragraph",
			"paragraph": map[string]interface{}{"text": "summary must not duplicate"},
			"children": []interface{}{
				map[string]interface{}{"text": "plain "},
				map[string]interface{}{"text": "bold", "bold": true},
				map[string]interface{}{
					"elementType": "link",
					"properties":  map[string]interface{}{"href": "https://example.com"},
					"children":    []interface{}{map[string]interface{}{"text": " link"}},
				},
				map[string]interface{}{
					"elementType": "image",
					"properties":  map[string]interface{}{"src": "https://example.com/a.png"},
				},
			},
		}),
		blockJSON(t, map[string]interface{}{
			"blockType": "orderedList",
			"orderedList": map[string]interface{}{
				"list": map[string]interface{}{"level": 1},
			},
			"children": []interface{}{map[string]interface{}{"text": "nested item"}},
		}),
		blockJSON(t, map[string]interface{}{
			"blockType":  "blockquote",
			"blockquote": map[string]interface{}{"text": "quoted"},
		}),
		blockJSON(t, map[string]interface{}{
			"blockType": "table",
			"table": map[string]interface{}{"cells": [][]string{
				{"Name", "Role"},
				{"Alice", "R&D | Search"},
			}},
		}),
	}

	result := renderDocument("Demo", blocks)
	for _, expected := range []string{
		"# Demo",
		"## **Rich heading**",
		"plain **bold**[ link](https://example.com)![image](https://example.com/a.png)",
		"  1. nested item",
		"> quoted",
		"| Name | Role |",
		"| Alice | R&D \\| Search |",
	} {
		if !strings.Contains(result.Markdown, expected) {
			t.Errorf("Markdown missing %q:\n%s", expected, result.Markdown)
		}
	}
	if strings.Contains(result.Markdown, "summary must not duplicate") {
		t.Fatalf("paragraph summary duplicated inline children:\n%s", result.Markdown)
	}
	if len(result.UnknownTypes) != 0 {
		t.Fatalf("unexpected unknown block types: %v", result.UnknownTypes)
	}
}

func TestRenderDocumentRecursesOfficialContainers(t *testing.T) {
	paragraph := map[string]interface{}{
		"blockType": "paragraph",
		"paragraph": map[string]interface{}{"text": "inside"},
	}
	blocks := []json.RawMessage{
		blockJSON(t, map[string]interface{}{
			"blockType": "callout",
			"callout":   map[string]interface{}{"sticker": "灯泡"},
			"children":  []interface{}{paragraph},
		}),
		blockJSON(t, map[string]interface{}{
			"blockType": "columns",
			"columns":   map[string]interface{}{"size": 2},
			"children": []interface{}{
				map[string]interface{}{
					"blockType": "paragraph",
					"paragraph": map[string]interface{}{"text": "column"},
				},
			},
		}),
	}
	result := renderDocument("", blocks)
	if strings.Count(result.Markdown, "inside") != 1 ||
		strings.Count(result.Markdown, "column") != 1 {
		t.Fatalf("container rendering = %q", result.Markdown)
	}
	if len(result.UnknownTypes) != 0 {
		t.Fatalf("container types reported unknown: %v", result.UnknownTypes)
	}
}

func TestRenderDocumentReportsUnknownAndMalformedBlocks(t *testing.T) {
	result := renderDocument("", []json.RawMessage{
		json.RawMessage(`{"blockType":"newWidget","newWidget":{"text":"future"}}`),
		json.RawMessage(`not-json`),
		json.RawMessage(`{"blockType":"Undefined"}`),
	})
	want := []string{"invalid_json", "newwidget", "undefined"}
	if strings.Join(result.UnknownTypes, ",") != strings.Join(want, ",") {
		t.Fatalf("unknown types = %v, want %v", result.UnknownTypes, want)
	}
}
