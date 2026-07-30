package dingtalk

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestBlocksToMarkdown(t *testing.T) {
	blocks := []json.RawMessage{
		json.RawMessage(`{"blockType":"heading","heading":{"level":2},"children":[{"text":"Release notes"}]}`),
		json.RawMessage(`{"blockType":"paragraph","children":[{"text":"Read "},{"text":"the docs","marks":[{"attrs":{"href":"https://example.com/docs"}}]}]}`),
		json.RawMessage(`{"blockType":"unorderedList","children":[{"text":"first item"}]}`),
		json.RawMessage(`{"blockType":"blockquote","children":[{"text":"important"}]}`),
		json.RawMessage(`{"blockType":"code","code":{"language":"go","text":"fmt.Println(1)"}}`),
		json.RawMessage(`{"blockType":"divider"}`),
	}

	got, err := blocksToMarkdown(blocks)
	if err != nil {
		t.Fatalf("blocksToMarkdown: %v", err)
	}
	wantParts := []string{
		"## Release notes",
		"Read [the docs](https://example.com/docs)",
		"- first item",
		"> important",
		"```go\nfmt.Println(1)\n```",
		"---",
	}
	for _, part := range wantParts {
		if !strings.Contains(got, part) {
			t.Errorf("Markdown missing %q:\n%s", part, got)
		}
	}
}

func TestBlocksToMarkdownSupportsUnknownTextWrappers(t *testing.T) {
	blocks := []json.RawMessage{
		json.RawMessage(`{"blockType":"newBlockType","content":{"children":[{"text":"future content"}]}}`),
	}
	got, err := blocksToMarkdown(blocks)
	if err != nil {
		t.Fatalf("blocksToMarkdown: %v", err)
	}
	if got != "future content" {
		t.Fatalf("Markdown = %q", got)
	}
}

func TestBlocksToMarkdownRejectsMalformedBlock(t *testing.T) {
	if _, err := blocksToMarkdown([]json.RawMessage{json.RawMessage(`{"blockType":`)}); err == nil {
		t.Fatal("expected malformed block error")
	}
}
