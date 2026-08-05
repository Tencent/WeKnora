package dingtalk

import (
	"encoding/json"
	"strings"
	"testing"
)

// D5: structure survives conversion. RAG chunking splits on Markdown
// boundaries, so a flattened dump retrieves worse than a structured document.
func TestBlocksToMarkdownPreservesStructure(t *testing.T) {
	blocks := []block{
		{BlockType: "heading", Value: map[string]interface{}{"text": "Title", "level": float64(1)}},
		{BlockType: "paragraph", Value: map[string]interface{}{"text": "Intro prose."}},
		{BlockType: "heading", Value: map[string]interface{}{"text": "Sub", "level": float64(2)}},
		{BlockType: "bulleted_list", Value: map[string]interface{}{"text": "first"}},
		{BlockType: "bulleted_list", Value: map[string]interface{}{"text": "nested", "indentLevel": float64(1)}},
		{BlockType: "code", Value: map[string]interface{}{"text": "fmt.Println(1)", "language": "go"}},
		{BlockType: "quote", Value: map[string]interface{}{"text": "quoted"}},
		{BlockType: "divider", Value: map[string]interface{}{}},
	}
	got := blocksToMarkdown(blocks)

	for _, want := range []string{
		"# Title",
		"## Sub",
		"- first",
		"  - nested",
		"```go\nfmt.Println(1)\n```",
		"> quoted",
		"---",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
}

func TestOrderedListNumbersSequentially(t *testing.T) {
	blocks := []block{
		{BlockType: "ordered_list", Value: map[string]interface{}{"text": "alpha"}},
		{BlockType: "ordered_list", Value: map[string]interface{}{"text": "beta"}},
		{BlockType: "ordered_list", Value: map[string]interface{}{"text": "gamma"}},
	}
	got := blocksToMarkdown(blocks)
	for _, want := range []string{"1. alpha", "2. beta", "3. gamma"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
}

func TestTableRendersAsMarkdownTable(t *testing.T) {
	blocks := []block{{BlockType: "table", Value: map[string]interface{}{
		"rows": []interface{}{
			[]interface{}{"Name", "Qty"},
			[]interface{}{"苹果", "3"},
		},
	}}}
	got := blocksToMarkdown(blocks)
	if !strings.Contains(got, "| Name | Qty |") {
		t.Errorf("header row missing:\n%s", got)
	}
	if !strings.Contains(got, "| --- | --- |") {
		t.Errorf("separator row missing:\n%s", got)
	}
	if !strings.Contains(got, "| 苹果 | 3 |") {
		t.Errorf("body row missing:\n%s", got)
	}
}

// A pipe inside a cell must be escaped or it silently corrupts the table.
func TestTableCellPipeIsEscaped(t *testing.T) {
	blocks := []block{{BlockType: "table", Value: map[string]interface{}{
		"rows": []interface{}{
			[]interface{}{"a|b"},
			[]interface{}{"c"},
		},
	}}}
	got := blocksToMarkdown(blocks)
	if !strings.Contains(got, `a\|b`) {
		t.Errorf("pipe not escaped:\n%s", got)
	}
}

func TestRichTextInlineMarks(t *testing.T) {
	blocks := []block{{BlockType: "paragraph", Value: map[string]interface{}{
		"texts": []interface{}{
			map[string]interface{}{"text": "plain "},
			map[string]interface{}{"text": "strong", "bold": true},
			map[string]interface{}{"text": " and "},
			map[string]interface{}{"text": "WeKnora", "link": "https://example.com"},
		},
	}}}
	got := blocksToMarkdown(blocks)
	if !strings.Contains(got, "**strong**") {
		t.Errorf("bold lost:\n%s", got)
	}
	if !strings.Contains(got, "WeKnora") {
		t.Errorf("link text lost:\n%s", got)
	}
	if strings.Contains(got, "https://example.com") {
		t.Errorf("private link URL must not be persisted:\n%s", got)
	}
}

func TestOfficialBlockTreePreservesStructureWithoutLeakingURLs(t *testing.T) {
	var blocks []block
	err := json.Unmarshal([]byte(`[
		{
			"blockType":"heading",
			"heading":{"text":"Roadmap","level":2},
			"children":[
				{"inlineType":"text","properties":{"text":"Roadmap","bold":true}}
			]
		},
		{
			"blockType":"paragraph",
			"paragraph":{"text":"Visit Docs"},
			"children":[
				{"inlineType":"text","properties":{"text":"Visit "}},
				{
					"inlineType":"link",
					"properties":{"href":"https://example.com/private?token=link-secret"},
					"children":[
						{"inlineType":"text","properties":{"text":"Docs"}}
					]
				},
				{
					"inlineType":"image",
					"properties":{"src":"https://example.com/image?signature=image-secret"}
				}
			]
		},
		{
			"blockType":"callout",
			"callout":{},
			"children":[
				{
					"blockType":"unorderedList",
					"unorderedList":{"list":{"level":0}},
					"children":[
						{"inlineType":"text","properties":{"text":"Nested item"}}
					]
				}
			]
		},
		{
			"blockType":"table",
			"table":{"rolSize":2,"colSize":2,"cells":[["Name","Qty"],["Apple","3"]]}
		}
	]`), &blocks)
	if err != nil {
		t.Fatalf("unmarshal official blocks: %v", err)
	}

	got := blocksToMarkdown(blocks)
	for _, want := range []string{
		"## **Roadmap**",
		"Visit Docs [image]",
		"- Nested item",
		"| Name | Qty |",
		"| Apple | 3 |",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	for _, secret := range []string{"https://example.com", "link-secret", "image-secret"} {
		if strings.Contains(got, secret) {
			t.Errorf("rendered document leaks signed/private URL material %q:\n%s", secret, got)
		}
	}
}

func TestOfficialInlineElementShapePreservesTextAndStylesWithoutLeakingURLs(t *testing.T) {
	var blocks []block
	err := json.Unmarshal([]byte(`[
		{
			"blockType":"paragraph",
			"children":[
				{"elementType":"text","text":"Bold","bold":true},
				{
					"elementType":"link",
					"properties":{"href":"https://example.com/private?token=link-secret"},
					"children":[{"elementType":"text","text":" label","italic":true}]
				},
				{
					"elementType":"image",
					"properties":{"src":"https://example.com/image?signature=image-secret"}
				},
				{"elementType":"text","text":" old","stike":true}
			]
		}
	]`), &blocks)
	if err != nil {
		t.Fatalf("unmarshal official inline elements: %v", err)
	}

	got := blocksToMarkdown(blocks)
	for _, want := range []string{"**Bold**", "* label*", "[image]", "~~ old~~"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	for _, secret := range []string{"https://example.com", "link-secret", "image-secret"} {
		if strings.Contains(got, secret) {
			t.Errorf("rendered document leaks signed/private URL material %q:\n%s", secret, got)
		}
	}
}

// An unrecognized block type must keep its text rather than dropping content:
// a DingTalk API addition should degrade to prose, not data loss.
func TestUnknownBlockTypeKeepsText(t *testing.T) {
	blocks := []block{
		{BlockType: "some_future_block", Value: map[string]interface{}{"text": "still important"}},
	}
	if got := blocksToMarkdown(blocks); !strings.Contains(got, "still important") {
		t.Errorf("unknown block dropped its text: %q", got)
	}
}

func TestUnknownContainerPreservesStructuralChildrenAndReportsKind(t *testing.T) {
	blocks := []block{{
		BlockType: "future_layout",
		Value:     map[string]interface{}{"text": "wrapper note"},
		Children: []block{
			{
				BlockType: "paragraph",
				Value:     map[string]interface{}{"text": "nested paragraph"},
			},
			{
				BlockType: "bulleted_list",
				Value:     map[string]interface{}{"text": "nested list item"},
			},
		},
	}}

	got, unknownKinds := blocksToMarkdownWithDiagnostics(blocks)

	for _, want := range []string{"wrapper note", "nested paragraph", "- nested list item"} {
		if !strings.Contains(got, want) {
			t.Errorf("unknown container dropped %q:\n%s", want, got)
		}
	}
	if len(unknownKinds) != 1 || unknownKinds[0] != "future_layout" {
		t.Fatalf("unknown kinds = %v, want [future_layout]", unknownKinds)
	}
}

func TestBoundedDiagnosticValueEscapesSizeAndPreservesUnicode(t *testing.T) {
	got := boundedDiagnosticValue(strings.Repeat("新", 80))
	if len([]rune(got)) != 67 || !strings.HasSuffix(got, "...") {
		t.Fatalf("bounded diagnostic = %q (%d runes)", got, len([]rune(got)))
	}
}

func TestSanitizeFileNameHandlesCJKAndSeparators(t *testing.T) {
	if got := sanitizeFileName("a/b:c*d"); strings.ContainsAny(got, `/\:*?"<>|`) {
		t.Errorf("unsafe characters survived: %q", got)
	}
	if got := sanitizeFileName(""); got != "untitled" {
		t.Errorf("empty name = %q, want untitled", got)
	}
	long := strings.Repeat("中", 300) // 3 bytes per rune
	got := sanitizeFileName(long)
	if len(got) > 200 {
		t.Errorf("not truncated: %d bytes", len(got))
	}
	for _, r := range got {
		if r == '�' {
			t.Fatalf("truncation split a rune, producing invalid UTF-8: %q", got)
		}
	}
}
