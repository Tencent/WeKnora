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
				{"elementType":"link","properties":{"href":"https://example.com/a"},
				 "children":[{"elementType":"text","text":"link"}]},
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
				{"elementType":"link","properties":{"href":"javascript:alert(1)"},
				 "children":[{"elementType":"text","text":"safe label"}]},
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

func TestRenderDocumentAcceptsOfficialBlockVariants(t *testing.T) {
	result := renderDocument("Doc", []json.RawMessage{
		rawJSON(`{"blockType":"heading","heading":{"level":"heading-3","text":"Section"}}`),
		rawJSON(`{
			"blockType":"paragraph",
			"children":[
				{"elementType":"text","text":"gone","strike":true},
				{"elementType":"text","text":" also","stike":true}
			]
		}`),
		rawJSON(`{"blockType":"table","table":{"cells":[[{"text":"A"},{"text":"B"}],[{"text":"1"},{"text":"2"}]]}}`),
		rawJSON(`{
			"blockType":"unorderedList",
			"unorderedList":{"list":{"level":"0"}},
			"children":[{"blockType":"paragraph","paragraph":{"text":"item body"}}]
		}`),
		rawJSON(`{"blockType":"callout"}`),
	})
	for _, expected := range []string{
		"### Section",
		"~~gone~~",
		"~~ also~~",
		"| A | B |",
		"| 1 | 2 |",
		"- item body",
	} {
		if !strings.Contains(result.Markdown, expected) {
			t.Errorf("Markdown missing %q:\n%s", expected, result.Markdown)
		}
	}
	if !reflect.DeepEqual(result.UnknownTypes, []string{"nested_blocks_unavailable"}) {
		t.Fatalf("UnknownTypes = %#v", result.UnknownTypes)
	}
}

// A block whose type is modelled but whose payload carries nothing renderable
// (a code block without text, an attachment without a name) must still be
// collected, so the loss stays visible instead of turning into an empty
// fragment of Markdown.
func TestRenderDocumentCollectsUnknownBlockTypes(t *testing.T) {
	result := renderDocument("测试文档", []json.RawMessage{
		rawJSON(`{"blockType":"code","code":{"syntax":"bash"}}`),
		rawJSON(`{"blockType":"attachment","attachment":{"resourceId":"res-synthetic-1"}}`),
		rawJSON(`{"blockType":"paragraph","paragraph":{"text":"正文仍然保留"}}`),
	})

	if !strings.Contains(result.Markdown, "正文仍然保留") {
		t.Fatalf("known content lost:\n%s", result.Markdown)
	}
	if strings.Contains(result.Markdown, "```") || strings.Contains(result.Markdown, "Attachment:") {
		t.Fatalf("empty payload rendered a fragment:\n%s", result.Markdown)
	}
	if want := []string{"attachment", "code"}; !reflect.DeepEqual(result.UnknownTypes, want) {
		t.Fatalf("UnknownTypes = %#v, want %#v", result.UnknownTypes, want)
	}
}

// The list fix must not change how ordinary documents render: paragraphs,
// headings, styles, blockquotes, tables and inline-only lists keep their exact
// Markdown output.
func TestRenderDocumentKeepsOrdinaryBlocksStable(t *testing.T) {
	result := renderDocument("标题 Doc", []json.RawMessage{
		rawJSON(`{"blockType":"heading","heading":{"level":2,"text":"Overview"}}`),
		rawJSON(`{"blockType":"paragraph","children":[` +
			`{"elementType":"text","text":"bold","bold":true},` +
			`{"elementType":"text","text":" and plain"}]}`),
		rawJSON(`{"blockType":"blockquote","blockquote":{"text":"first\nsecond"}}`),
		rawJSON(`{"blockType":"table","table":{"cells":[["A","B"],["1","x|y"]]}}`),
		rawJSON(`{"blockType":"orderedList","orderedList":{"list":{"level":1}},` +
			`"children":[{"elementType":"text","text":"nested"}]}`),
		rawJSON(`{"blockType":"paragraph","paragraph":{"text":"tail"}}`),
	})

	want := "# 标题 Doc\n\n" +
		"## Overview\n\n" +
		"**bold** and plain\n\n" +
		"> first\n> second\n\n" +
		"| A | B |\n| --- | --- |\n| 1 | x\\|y |\n\n" +
		"  1. nested\n" +
		"tail\n"
	if result.Markdown != want {
		t.Fatalf("Markdown = %q, want %q", result.Markdown, want)
	}
	if len(result.UnknownTypes) != 0 {
		t.Fatalf("UnknownTypes = %#v, want none", result.UnknownTypes)
	}
}

// Inline element types the renderer does not model may carry their text in
// children rather than in a text field (slot is documented that way), and those
// runs used to disappear: the default branch wrote an empty text and stopped.
func TestRenderDocumentKeepsTextOfUnmodelledInlineElements(t *testing.T) {
	result := renderDocument("测试文档", []json.RawMessage{
		rawJSON(`{"blockType":"paragraph","children":[` +
			`{"elementType":"text","text":"由 "},` +
			`{"elementType":"slot","children":[{"elementType":"text","text":"张三"}]},` +
			`{"elementType":"text","text":" 负责"}]}`),
	})

	if !strings.Contains(result.Markdown, "由 张三 负责") {
		t.Fatalf("inline run lost its unmodelled element:\n%s", result.Markdown)
	}
	if want := []string{"inline_slot"}; !reflect.DeepEqual(result.UnknownTypes, want) {
		t.Fatalf("UnknownTypes = %#v, want %#v", result.UnknownTypes, want)
	}
}
