package reranking

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

func TestCleanPassagePreservesFencedCode(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, input, want string
	}{
		{
			name:  "XML-only evidence must not become empty",
			input: "```xml\n<refund-policy days=\"7\"/>\n```",
			want:  "<refund-policy days=\"7\"/>",
		},
		{
			name:  "URLs and formatting-like operators are code",
			input: "```python\nurl = \"https://api.example.com/v1\"\nvalue = 2 ** 3 ** 2\n```",
			want:  "url = \"https://api.example.com/v1\"\nvalue = 2 ** 3 ** 2",
		},
		{
			name: "literal Markdown is code",
			input: "```markdown\n# Heading\n- item\n| A | B |\n| --- | --- |\n" +
				"![alt](image.png)\n[label](https://example.com)\n```",
			want: "# Heading\n- item\n| A | B |\n| --- | --- |\n" +
				"![alt](image.png)\n[label](https://example.com)",
		},
		{
			name:  "code blank lines and math delimiters survive",
			input: "```text\nbefore\n\n\n\nafter $$price$$\n```",
			want:  "before\n\n\n\nafter $$price$$",
		},
		{
			name:  "longer outer fence keeps shorter inner fence",
			input: "````markdown\n```xml\n<policy/>\n```\n````",
			want:  "```xml\n<policy/>\n```",
		},
		{
			name:  "tilde fences",
			input: "~~~xml\n<policy/>\n~~~",
			want:  "<policy/>",
		},
		{
			name:  "mismatched delimiter is code",
			input: "```text\n~~~\n<policy/>\n```",
			want:  "~~~\n<policy/>",
		},
		{
			name:  "closing fence cannot carry an info string",
			input: "```text\n```not-a-close\n<policy/>\n```",
			want:  "```not-a-close\n<policy/>",
		},
		{
			name:  "longer closing fence",
			input: "```xml\n<policy/>\n`````",
			want:  "<policy/>",
		},
		{
			name:  "unclosed fence protects remaining code",
			input: "```xml\n<policy/>\nhttps://api.example.com",
			want:  "<policy/>\nhttps://api.example.com",
		},
		{
			name:  "CRLF code lines",
			input: "```xml\r\n<one/>\r\n<two/>\r\n```\r\n",
			want:  "<one/>\r\n<two/>",
		},
		{
			name:  "indented fence and CRLF closing whitespace",
			input: "   ~~~xml\r\n<policy/>\r\n   ~~~ \t\r\n",
			want:  "<policy/>",
		},
		{
			name: "only prose is cleaned between code blocks",
			input: "## Example\n```xml\n<one/>\n```\n**Explanation** [docs](https://example.com)\n" +
				"~~~shell\ncurl https://api.example.com\n~~~\n> End",
			want: "Example\n<one/>\nExplanation docs\ncurl https://api.example.com\nEnd",
		},
		{
			name:  "inline triple backticks do not open a block",
			input: "Text ```not a block\n**bold**",
			want:  "Text ```not a block\nbold",
		},
		{
			name:  "four-space indented fences are not fenced blocks",
			input: "    ```xml\n<policy/>\n    ```",
			want:  "```xml\n\n    ```",
		},
		{
			name:  "backticks in a backtick info string invalidate the opening",
			input: "```bad`info\n**bold**",
			want:  "```bad`info\nbold",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := CleanPassage(tt.input); got != tt.want {
				t.Errorf("CleanPassage() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestRerankReceivesFencedCodeWithoutMarkdownCleaning(t *testing.T) {
	t.Parallel()
	content := "```xml\n<refund-policy days=\"7\"/>\n```"
	model := &stubReranker{scores: []float64{0.9}}
	input := []*types.SearchResult{{
		ID: "policy", Content: content, KnowledgeTitle: "API policy",
		ContextHeader: "Refunds", ChunkType: string(types.ChunkTypeText), Score: 0.8,
	}}
	result := Rerank(context.Background(), model, "refund policy days", input,
		Options{Threshold: 0.3})

	want := "API policy\n\nRefunds\n\n<refund-policy days=\"7\"/>"
	if len(model.documents) != 1 || model.documents[0] != want {
		t.Fatalf("model received %q, want one passage %q", model.documents, want)
	}
	if len(result.Results) != 1 || result.Results[0].ID != "policy" {
		t.Fatalf("code-only candidate was dropped: %+v", result)
	}
	if input[0].Content != content || result.Results[0].Content != content {
		t.Fatal("passage cleaning changed the stored/returned chunk content")
	}
	if got := EnrichedPassage(context.Background(), input[0]); got != "<refund-policy days=\"7\"/>" {
		t.Fatalf("MMR enrichment lost the code evidence: %q", got)
	}
}
