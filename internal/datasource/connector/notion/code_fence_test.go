package notion

import (
	"strings"
	"testing"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/text"
)

func TestCodeBlockKeepsEmbeddedMarkdownFences(t *testing.T) {
	for _, code := range []string{
		"```markdown\n# literal heading\n```",
		"````\n# literal heading\n````",
		"before\n```\n# literal heading\nafter",
	} {
		t.Run(code, func(t *testing.T) {
			block := makeBlock(t, "code", map[string]interface{}{
				"language":  "markdown",
				"rich_text": []notionRichText{{Type: "text", PlainText: code}},
			})
			after := makeBlock(t, "paragraph", map[string]interface{}{
				"rich_text": []notionRichText{{Type: "text", PlainText: "Following paragraph"}},
			})
			md, _ := BlocksToMarkdown([]notionBlock{block, after})
			source := []byte(md)
			root := goldmark.DefaultParser().Parse(text.NewReader(source))
			first, ok := root.FirstChild().(*ast.FencedCodeBlock)
			if !ok {
				t.Fatalf("first block = %T", root.FirstChild())
			}
			var got strings.Builder
			for i := 0; i < first.Lines().Len(); i++ {
				line := first.Lines().At(i)
				got.Write(line.Value(source))
			}
			if got.String() != code+"\n" {
				t.Fatalf("code = %q, want %q", got.String(), code+"\n")
			}
			next := first.NextSibling()
			if next == nil || next.Kind() != ast.KindParagraph || next.NextSibling() != nil {
				t.Fatalf("code boundaries changed: %s", md)
			}
		})
	}
}
