package dingtalk

import (
	"encoding/json"
	"strings"
	"testing"
)

func rawBlock(t *testing.T, value interface{}) json.RawMessage {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func TestRenderDocumentMarkdownElementBlocks(t *testing.T) {
	blocks := []json.RawMessage{
		rawBlock(t, map[string]interface{}{"blockType": "heading-2", "heading-2": map[string]interface{}{"text": "Overview"}}),
		rawBlock(t, map[string]interface{}{
			"blockType": "paragraph",
			"paragraph": map[string]interface{}{},
			"children": []interface{}{
				map[string]interface{}{"elementType": "TEXT", "value": "Hello "},
				map[string]interface{}{"elementType": "TEXT", "value": "DingTalk"},
				map[string]interface{}{"elementType": "image", "properties": map[string]interface{}{"src": "https://example.com/a.png", "alt": "diagram"}},
			},
		}),
		rawBlock(t, map[string]interface{}{"blockType": "unordered-list", "unordered-list": map[string]interface{}{"items": []interface{}{"one", "two"}}}),
		rawBlock(t, map[string]interface{}{"blockType": "blockquote", "blockquote": map[string]interface{}{"text": "quoted"}}),
		rawBlock(t, map[string]interface{}{"blockType": "codeBlock", "codeBlock": map[string]interface{}{"language": "go", "text": "fmt.Println(1)"}}),
		rawBlock(t, map[string]interface{}{"blockType": "table", "table": map[string]interface{}{"cells": []interface{}{
			[]interface{}{"Name", "Role"},
			[]interface{}{"Alice", "R&D"},
		}}}),
		rawBlock(t, map[string]interface{}{"blockType": "attachment", "attachment": map[string]interface{}{"name": "Report.pdf", "resourceId": "resource-1"}}),
	}

	got := renderDocumentMarkdown("Demo", blocks)
	for _, expected := range []string{
		"# Demo", "## Overview", "Hello", "DingTalk", "![diagram](https://example.com/a.png)",
		"- one", "- two", "> quoted", "```go", "fmt.Println(1)",
		"| Name | Role |", "| --- | --- |", "| Alice | R&D |", "Report.pdf", "resource-1",
	} {
		if !strings.Contains(got.Content, expected) {
			t.Errorf("rendered markdown missing %q:\n%s", expected, got.Content)
		}
	}
	if strings.Count(got.Content, "![diagram]") != 1 {
		t.Fatalf("image child rendered more than once:\n%s", got.Content)
	}
	if len(got.UnknownTypes) != 0 {
		t.Fatalf("unexpected unknown types: %v", got.UnknownTypes)
	}
}

func TestRenderDocumentMarkdownDeduplicatesChildren(t *testing.T) {
	child := map[string]interface{}{"blockId": "child-1", "blockType": "paragraph", "paragraph": map[string]interface{}{"text": "once"}}
	block := rawBlock(t, map[string]interface{}{
		"blockType": "columns",
		"columns":   map[string]interface{}{"children": []interface{}{child}},
		"children":  []interface{}{child},
	})
	got := renderDocumentMarkdown("", []json.RawMessage{block})
	if count := strings.Count(got.Content, "once"); count != 1 {
		t.Fatalf("child text count = %d, want 1:\n%s", count, got.Content)
	}
	if len(got.UnknownTypes) != 0 {
		t.Fatalf("unexpected unknown types: %v", got.UnknownTypes)
	}
}

func TestRenderDocumentMarkdownOfficialBlockStructure(t *testing.T) {
	blocks := []json.RawMessage{
		rawBlock(t, map[string]interface{}{
			"blockType": "paragraph",
			"paragraph": map[string]interface{}{"text": "plain bold link"},
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
		rawBlock(t, map[string]interface{}{
			"blockType": "orderedList",
			"orderedList": map[string]interface{}{
				"list": map[string]interface{}{"level": 1},
			},
			"children": []interface{}{map[string]interface{}{"text": "nested item"}},
		}),
		rawBlock(t, map[string]interface{}{
			"blockType": "callout",
			"callout":   map[string]interface{}{"sticker": "灯泡"},
			"children": []interface{}{
				map[string]interface{}{
					"blockType": "paragraph",
					"paragraph": map[string]interface{}{"text": "inside callout"},
				},
			},
		}),
	}

	got := renderDocumentMarkdown("", blocks)
	for _, expected := range []string{
		"plain **bold**[ link](https://example.com)",
		"![image](https://example.com/a.png)",
		"  1. nested item",
		"inside callout",
	} {
		if !strings.Contains(got.Content, expected) {
			t.Errorf("rendered markdown missing %q:\n%s", expected, got.Content)
		}
	}
	if count := strings.Count(got.Content, "plain bold link"); count != 0 {
		t.Fatalf("summary text duplicated formatted inline children:\n%s", got.Content)
	}
	if len(got.UnknownTypes) != 0 {
		t.Fatalf("unexpected unknown types: %v", got.UnknownTypes)
	}
}

func TestRenderDocumentMarkdownJSONML(t *testing.T) {
	jsonml := []interface{}{
		"root", map[string]interface{}{},
		[]interface{}{"h2", map[string]interface{}{"uuid": "h"}, "JSONML heading"},
		[]interface{}{"p", map[string]interface{}{},
			[]interface{}{"span", map[string]interface{}{}, "plain "},
			[]interface{}{"strong", map[string]interface{}{}, "bold"},
			[]interface{}{"a", map[string]interface{}{"href": "https://example.com"}, " link"},
		},
		[]interface{}{"ul", map[string]interface{}{},
			[]interface{}{"li", map[string]interface{}{}, "first"},
			[]interface{}{"li", map[string]interface{}{}, "second"},
		},
		[]interface{}{"table", map[string]interface{}{},
			[]interface{}{"tbody", map[string]interface{}{},
				[]interface{}{"tr", map[string]interface{}{}, []interface{}{"th", map[string]interface{}{}, "A"}, []interface{}{"th", map[string]interface{}{}, "B"}},
				[]interface{}{"tr", map[string]interface{}{}, []interface{}{"td", map[string]interface{}{}, "1"}, []interface{}{"td", map[string]interface{}{}, "2"}},
			},
		},
	}
	encoded, err := json.Marshal(jsonml)
	if err != nil {
		t.Fatal(err)
	}
	block := rawBlock(t, map[string]interface{}{"blockId": "root", "blockType": "root", "jsonml": string(encoded)})
	got := renderDocumentMarkdown("Doc", []json.RawMessage{block})
	for _, expected := range []string{"## JSONML heading", "plain **bold**[ link](https://example.com)", "- first", "- second", "| A | B |", "| 1 | 2 |"} {
		if !strings.Contains(got.Content, expected) {
			t.Errorf("JSONML markdown missing %q:\n%s", expected, got.Content)
		}
	}
	if len(got.UnknownTypes) != 0 {
		t.Fatalf("unexpected JSONML unknown types: %v", got.UnknownTypes)
	}
}

func TestRenderDocumentMarkdownUnknownAndInvalidAreDeterministic(t *testing.T) {
	got := renderDocumentMarkdown("", []json.RawMessage{
		json.RawMessage(`{"blockType":"zeta","zeta":{"text":"z"}}`),
		json.RawMessage(`not-json`),
		json.RawMessage(`{"blockType":"alpha","alpha":{"text":"a"}}`),
		json.RawMessage(`{"blockType":"root","jsonml":"not-json"}`),
	})
	want := []string{"alpha", "invalid_json", "invalid_jsonml", "root", "zeta"}
	if len(got.UnknownTypes) != len(want) {
		t.Fatalf("unknown types = %v, want %v", got.UnknownTypes, want)
	}
	for i := range want {
		if got.UnknownTypes[i] != want[i] {
			t.Fatalf("unknown types = %v, want %v", got.UnknownTypes, want)
		}
	}
}
