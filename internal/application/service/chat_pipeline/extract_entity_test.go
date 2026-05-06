package chatpipeline

import (
	"context"
	"strings"
	"testing"
)

// TestFormater_ParseGraph_FenceVariants exercises the JSON parsing path used
// by the graph extraction pipeline against the LLM response shapes that
// caused issue #1113. Each case feeds a raw LLM response string to
// Formater.ParseGraph and asserts the resulting graph data, or that the
// error path is preserved for genuinely invalid input.
func TestFormater_ParseGraph_FenceVariants(t *testing.T) {
	const validJSON = `[
  {"entity": "Alice", "entity_attributes": ["person"]},
  {"entity": "Bob", "entity_attributes": ["person"]},
  {"entity1": "Alice", "entity2": "Bob", "relation": "knows"}
]`

	cases := []struct {
		name        string
		input       string
		wantNodes   int
		wantRels    int
		wantErr     bool
		errContains string
	}{
		{
			name:      "wrapped in ```json fence",
			input:     "```json\n" + validJSON + "\n```",
			wantNodes: 2,
			wantRels:  1,
		},
		{
			name:      "wrapped in plain ``` fence (no language tag)",
			input:     "```\n" + validJSON + "\n```",
			wantNodes: 2,
			wantRels:  1,
		},
		{
			name:      "no fences at all (raw JSON)",
			input:     validJSON,
			wantNodes: 2,
			wantRels:  1,
		},
		{
			name:      "leading prose then ```json fence",
			input:     "Here is the extracted graph:\n\n```json\n" + validJSON + "\n```",
			wantNodes: 2,
			wantRels:  1,
		},
		{
			name:      "trailing prose after closing fence",
			input:     "```json\n" + validJSON + "\n```\n\nHope this helps!",
			wantNodes: 2,
			wantRels:  1,
		},
		{
			name:      "extra surrounding whitespace and newlines",
			input:     "\n\n   ```json\n\n" + validJSON + "\n\n```   \n",
			wantNodes: 2,
			wantRels:  1,
		},
		{
			// Issue #1113 Pattern 3: LLM hit max_tokens, no closing fence.
			// The response is structurally a JSON array we can still parse.
			name:      "truncated response, opening ```json fence with no closer",
			input:     "```json\n" + validJSON,
			wantNodes: 2,
			wantRels:  1,
		},
		{
			// Issue #1113 Pattern 1: bare backticks/markdown around JSON
			// without a well-formed fence pair.
			name:      "stray backticks around JSON",
			input:     "`" + validJSON + "`",
			wantNodes: 2,
			wantRels:  1,
		},
		{
			name:      "JSON object embedded in prose (single dict)",
			input:     "Result: {\"entity\": \"Alice\", \"entity_attributes\": [\"person\"]} -- end.",
			wantNodes: 1,
			wantRels:  0,
		},
		{
			name:        "empty input",
			input:       "",
			wantErr:     true,
			errContains: "empty",
		},
		{
			name:        "whitespace only",
			input:       "   \n\t  ",
			wantErr:     true,
			errContains: "empty",
		},
		{
			name:        "fenced but body is invalid JSON",
			input:       "```json\nnot json at all\n```",
			wantErr:     true,
			errContains: "parse",
		},
		{
			name:        "no recoverable JSON, only prose",
			input:       "Sorry, I cannot extract a graph from this text.",
			wantErr:     true,
			errContains: "parse",
		},
	}

	ctx := context.Background()
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			f := NewFormater()
			graph, err := f.ParseGraph(ctx, tc.input)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil (graph=%+v)", graph)
				}
				if tc.errContains != "" && !strings.Contains(err.Error(), tc.errContains) {
					t.Fatalf("error %q does not contain %q", err.Error(), tc.errContains)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if graph == nil {
				t.Fatalf("expected non-nil graph")
			}
			if got := len(graph.Node); got != tc.wantNodes {
				t.Errorf("nodes: got %d, want %d (graph=%+v)", got, tc.wantNodes, graph)
			}
			if got := len(graph.Relation); got != tc.wantRels {
				t.Errorf("relations: got %d, want %d (graph=%+v)", got, tc.wantRels, graph)
			}
		})
	}
}

// TestExtractJSONLike covers the JSON-substring extraction helper in
// isolation. The helper is used by the fallback path in extractContent when
// fences are missing or malformed.
func TestExtractJSONLike(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"empty", "", ""},
		{"plain object", `{"a": 1}`, `{"a": 1}`},
		{"plain array", `[1, 2, 3]`, `[1, 2, 3]`},
		{"object in prose", `noise {"a": 1} tail`, `{"a": 1}`},
		{"array preferred when first", `tail [1] {"a":1}`, `[1]`},
		{"object preferred when first", `tail {"a":1} [1]`, `{"a":1}`},
		{"nested braces", `{"a": {"b": [1,2]}}`, `{"a": {"b": [1,2]}}`},
		{"brace inside string literal", `{"a": "}{not real}"}`, `{"a": "}{not real}"}`},
		{"escaped quote inside string", `{"a": "he said \"hi\""}`, `{"a": "he said \"hi\""}`},
		{"unbalanced object returns empty", `{"a": 1`, ""},
		{"no json", `just words`, ""},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			got := extractJSONLike(tc.in)
			if got != tc.want {
				t.Errorf("extractJSONLike(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestStripFencesAndExtract focuses on the fence-recovery helper used as a
// last resort when the main fence regex fails. Behavior must remain
// conservative: return an empty string when nothing plausible can be
// recovered, so the caller can fall through to existing behavior.
func TestStripFencesAndExtract(t *testing.T) {
	cases := []struct {
		name   string
		in     string
		format FormatType
		want   string
	}{
		{
			name:   "open fence with json tag and no close",
			in:     "```json\n{\"a\":1}",
			format: FormatTypeJSON,
			want:   `{"a":1}`,
		},
		{
			name:   "open fence with no tag and no close",
			in:     "```\n[1,2]",
			format: FormatTypeJSON,
			want:   `[1,2]`,
		},
		{
			name:   "well-formed fence still recovers body",
			in:     "```json\n{\"a\":1}\n```",
			format: FormatTypeJSON,
			want:   `{"a":1}`,
		},
		{
			name:   "no fence but embedded json object",
			in:     "Sure! {\"a\":1} done.",
			format: FormatTypeJSON,
			want:   `{"a":1}`,
		},
		{
			name:   "no fence and no json",
			in:     "just prose",
			format: FormatTypeJSON,
			want:   "",
		},
		{
			name:   "empty input",
			in:     "",
			format: FormatTypeJSON,
			want:   "",
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			got := stripFencesAndExtract(tc.in, tc.format)
			if got != tc.want {
				t.Errorf("stripFencesAndExtract(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestIsLikelyLanguageTag guards the heuristic used to drop language-tag
// lines after an opening fence in the recovery path.
func TestIsLikelyLanguageTag(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"json", true},
		{"yaml", true},
		{"yml", true},
		{"go", true},
		{"c++", true},
		{"objective-c", true},
		{"", false},
		{"this is not a tag", false},
		{`{"a":1}`, false},
		{strings.Repeat("a", 17), false},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.in, func(t *testing.T) {
			if got := isLikelyLanguageTag(tc.in); got != tc.want {
				t.Errorf("isLikelyLanguageTag(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}
