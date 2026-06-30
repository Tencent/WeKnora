package dingtalk

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRenderBlocksMarkdown_MixedBlocks(t *testing.T) {
	children, _ := json.Marshal([]docBlock{
		{BlockType: "unorderedList", Index: 1, UnorderedList: &textBlock{Text: "child"}},
	})
	got := renderBlocksMarkdown("Doc Title", []docBlock{
		{BlockType: "paragraph", Index: 2, Paragraph: &textBlock{Text: "plain"}},
		{BlockType: "heading", Index: 1, Heading: &headingBlock{Text: "Section", Level: 3}},
		{BlockType: "blockquote", Index: 3, Blockquote: &textBlock{Text: "quote"}},
		{BlockType: "orderedList", Index: 4, OrderedList: &textBlock{Text: "first"}, Children: children},
	})
	wantParts := []string{
		"# Doc Title",
		"### Section",
		"plain",
		"> quote",
		"1. first",
		"  - child",
	}
	for _, part := range wantParts {
		if !strings.Contains(got, part) {
			t.Fatalf("markdown missing %q:\n%s", part, got)
		}
	}
}
