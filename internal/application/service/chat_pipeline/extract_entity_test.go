package chatpipeline

import (
	"context"
	"encoding/json"
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

func TestFormater_ParseGraph_RejectsInvalidItemsIndividually(t *testing.T) {
	items := []interface{}{
		map[string]interface{}{"entity": " Alice ", "entity_attributes": []interface{}{" person ", "person", "", 7}},
		map[string]interface{}{"entity": " Bob "},
		"model explanation",
		42,
		map[string]interface{}{"entity": "   "},
		map[string]interface{}{"entity": 123},
		map[string]interface{}{"entity": "Bad\nName"},
		map[string]interface{}{"entity1": " Alice ", "entity2": " Bob ", "relation": " knows "},
		map[string]interface{}{"entity1": "Alice", "entity2": "Bob", "relation": "knows"},
		map[string]interface{}{"entity1": "", "entity2": "Bob", "relation": "knows"},
		map[string]interface{}{"entity1": "Alice", "entity2": "Bob", "relation": ""},
		map[string]interface{}{"entity1": "Alice", "entity2": " Alice ", "relation": "knows"},
		map[string]interface{}{"unexpected": "field"},
	}
	payload, err := json.Marshal(items)
	if err != nil {
		t.Fatalf("marshal test payload: %v", err)
	}

	graph, err := NewFormater().ParseGraph(context.Background(), string(payload))
	if err != nil {
		t.Fatalf("one invalid item must not fail the whole graph: %v", err)
	}
	if len(graph.Node) != 2 {
		t.Fatalf("nodes = %#v, want extracted Alice and Bob", graph.Node)
	}
	if graph.Node[0].Name != "Alice" || graph.Node[1].Name != "Bob" {
		t.Fatalf("node names = [%q, %q], want [Alice, Bob]", graph.Node[0].Name, graph.Node[1].Name)
	}
	if got := graph.Node[0].Attributes; len(got) != 1 || got[0] != "person" {
		t.Fatalf("Alice attributes = %#v, want [person]", got)
	}
	if len(graph.Relation) != 1 {
		t.Fatalf("relations = %#v, want one normalized and deduplicated relation", graph.Relation)
	}
	relation := graph.Relation[0]
	if relation.Node1 != "Alice" || relation.Node2 != "Bob" || relation.Type != "knows" {
		t.Fatalf("relation = %+v, want Alice --[knows]--> Bob", relation)
	}
	diagnostics := graph.Diagnostics
	if diagnostics == nil {
		t.Fatal("expected graph validation diagnostics")
	}
	if diagnostics.ItemsReceived != 13 || diagnostics.ItemsRejected != 9 {
		t.Fatalf("item diagnostics = %+v, want 13 received and 9 rejected", diagnostics)
	}
	if diagnostics.NodesExtracted != 5 || diagnostics.NodesAccepted != 2 || diagnostics.NodesRejected != 3 {
		t.Fatalf("node diagnostics = %+v, want 5 extracted, 2 accepted, 3 rejected", diagnostics)
	}
	if diagnostics.RelationsExtracted != 5 || diagnostics.RelationsAccepted != 1 ||
		diagnostics.RelationsRejected != 3 || diagnostics.RelationsMerged != 1 {
		t.Fatalf("relation diagnostics = %+v", diagnostics)
	}
	if len(diagnostics.Failures) != 9 {
		t.Fatalf("failure details = %+v, want all 9 rejected items", diagnostics.Failures)
	}
}

func TestFormater_ParseGraph_RejectsOverlongIdentifiers(t *testing.T) {
	longName := strings.Repeat("甲", maxGraphIdentifierRunes+1)
	items := []interface{}{
		map[string]interface{}{"entity": longName},
		map[string]interface{}{"entity": "Valid"},
		map[string]interface{}{"entity": "Target"},
		map[string]interface{}{"entity1": "Valid", "entity2": "Target", "relation": longName},
		map[string]interface{}{"entity1": "Valid", "entity2": "Target", "relation": "related"},
	}
	payload, err := json.Marshal(items)
	if err != nil {
		t.Fatalf("marshal test payload: %v", err)
	}

	graph, err := NewFormater().ParseGraph(context.Background(), string(payload))
	if err != nil {
		t.Fatalf("overlong item must be rejected without failing valid items: %v", err)
	}
	if len(graph.Node) != 2 || len(graph.Relation) != 1 {
		t.Fatalf("graph = %+v, want two valid nodes and one valid relation", graph)
	}
}

func TestFormater_ParseGraph_RejectsRelationsWithUnknownEndpoints(t *testing.T) {
	items := []interface{}{
		map[string]interface{}{"entity1": "Alice", "entity2": "Bob", "relation": "knows"},
		map[string]interface{}{"entity1": "Alice", "entity2": "Charlie", "relation": "knows"},
		map[string]interface{}{"entity1": "Dave", "entity2": "Bob", "relation": "knows"},
		map[string]interface{}{"entity1": "Eve", "entity2": "Frank", "relation": "knows"},
		map[string]interface{}{"entity": "Alice"},
		map[string]interface{}{"entity": "Bob"},
	}
	payload, err := json.Marshal(items)
	if err != nil {
		t.Fatalf("marshal test payload: %v", err)
	}

	graph, err := NewFormater().ParseGraph(context.Background(), string(payload))
	if err != nil {
		t.Fatalf("unknown relation endpoints must not fail the whole graph: %v", err)
	}
	if len(graph.Node) != 2 {
		t.Fatalf("nodes = %#v, want only explicitly extracted Alice and Bob", graph.Node)
	}
	if len(graph.Relation) != 1 {
		t.Fatalf("relations = %#v, want only Alice --[knows]--> Bob", graph.Relation)
	}
	if graph.Diagnostics == nil || graph.Diagnostics.RelationsRejected != 3 ||
		graph.Diagnostics.RelationsAccepted != 1 {
		t.Fatalf("diagnostics = %+v, want three unknown-endpoint failures", graph.Diagnostics)
	}
}

func TestFormater_ParseGraph_MergesDuplicateNodeAttributes(t *testing.T) {
	items := []interface{}{
		map[string]interface{}{
			"entity":            "Alice",
			"entity_attributes": []interface{}{"person", "founder"},
		},
		map[string]interface{}{
			"entity":            " Alice ",
			"entity_attributes": []interface{}{"founder", "engineer"},
		},
		map[string]interface{}{"entity": "Alice"},
	}
	payload, err := json.Marshal(items)
	if err != nil {
		t.Fatalf("marshal test payload: %v", err)
	}

	graph, err := NewFormater().ParseGraph(context.Background(), string(payload))
	if err != nil {
		t.Fatalf("duplicate nodes must merge without failing: %v", err)
	}
	if len(graph.Node) != 1 {
		t.Fatalf("nodes = %#v, want one merged Alice node", graph.Node)
	}
	want := []string{"person", "founder", "engineer"}
	got := graph.Node[0].Attributes
	if len(got) != len(want) {
		t.Fatalf("attributes = %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("attributes = %#v, want %#v", got, want)
		}
	}
	if graph.Diagnostics == nil || graph.Diagnostics.NodesMerged != 2 || graph.Diagnostics.ItemsRejected != 0 {
		t.Fatalf("diagnostics = %+v, duplicate-node merge must not count as failure", graph.Diagnostics)
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
