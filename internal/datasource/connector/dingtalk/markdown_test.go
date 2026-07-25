package dingtalk

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestRenderDocumentSupportsDocumentedBlocks(t *testing.T) {
	blocks := []json.RawMessage{
		rawJSON(`{"blockType":"heading","heading":{"level":2,"text":"Overview"}}`),
		rawJSON(`{
			"blockType":"paragraph",
			"children":[
				{"elementType":"text","text":"bold","bold":true},
				{"elementType":"text","text":" and 2 * 3 "},
				{"elementType":"link","properties":{"href":"https://example.com/a"},"children":[{"elementType":"text","text":"link"}]},
				{"elementType":"image","properties":{"src":"https://example.com/image.png"}}
			]
		}`),
		rawJSON(`{"blockType":"blockquote","blockquote":{"text":"first\nsecond"}}`),
		rawJSON(`{
			"blockType":"orderedList",
			"orderedList":{"list":{"level":1}},
			"children":[{"elementType":"text","text":"nested"}]
		}`),
		rawJSON(`{"blockType":"table","table":{"cells":[["A","B"],["1","x|y"]]}}`),
		rawJSON(`{
			"blockType":"callout",
			"children":[{"blockType":"paragraph","paragraph":{"text":"note"}}]
		}`),
	}

	result := renderDocument("Team | Notes", blocks)
	for _, expected := range []string{
		"# Team | Notes",
		"## Overview",
		"**bold** and 2 \\* 3 [link](https://example.com/a)![image](https://example.com/image.png)",
		"> first\n> second",
		"  1. nested",
		"| A | B |\n| --- | --- |\n| 1 | x\\|y |",
		"note",
	} {
		if !strings.Contains(result.Markdown, expected) {
			t.Errorf("Markdown missing %q:\n%s", expected, result.Markdown)
		}
	}
	if len(result.UnknownTypes) != 0 {
		t.Fatalf("UnknownTypes = %#v", result.UnknownTypes)
	}
}

func TestRenderDocumentHandlesUnknownAndUnsafeContent(t *testing.T) {
	result := renderDocument("Doc", []json.RawMessage{
		rawJSON(`{
			"blockType":"futureContainer",
			"children":[{"blockType":"paragraph","paragraph":{"text":"preserved"}}]
		}`),
		rawJSON(`{
			"blockType":"paragraph",
			"children":[
				{"elementType":"link","properties":{"href":"javascript:alert(1)"},"children":[{"elementType":"text","text":"safe label"}]},
				{"elementType":"image","properties":{"src":"data:text/html,unsafe"}},
				{"elementType":"futureInline","text":"fallback"}
			]
		}`),
		json.RawMessage(`{`),
	})

	if strings.Contains(result.Markdown, "javascript:") || strings.Contains(result.Markdown, "data:") {
		t.Fatalf("unsafe URL rendered:\n%s", result.Markdown)
	}
	if !strings.Contains(result.Markdown, "preserved") ||
		!strings.Contains(result.Markdown, "safe labelfallback") {
		t.Fatalf("useful fallback content lost:\n%s", result.Markdown)
	}
	wantUnknown := []string{"futurecontainer", "inline_futureinline", "invalid_json"}
	if !reflect.DeepEqual(result.UnknownTypes, wantUnknown) {
		t.Fatalf("UnknownTypes = %#v, want %#v", result.UnknownTypes, wantUnknown)
	}
}

func TestSanitizeFilenameDoesNotSplitUTF8(t *testing.T) {
	name := strings.Repeat("文", 100) + "/draft"
	sanitized := sanitizeFilename(name)
	if !strings.HasSuffix(sanitized, "文") || len(sanitized) > 200 {
		t.Fatalf("sanitizeFilename() = %q (%d bytes)", sanitized, len(sanitized))
	}
	if sanitized = sanitizeFilename("line\nname"); sanitized != "line_name" {
		t.Fatalf("sanitizeFilename() retained control character: %q", sanitized)
	}
}
