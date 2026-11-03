package docparser

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestJSONToMarkdownSplitsDeepSingleKeyContainers(t *testing.T) {
	// JSON exports commonly wrap the actual records in response/data containers.
	for _, depth := range []int{1, 3, 6} {
		t.Run(strings.Repeat("nested/", depth), func(t *testing.T) {
			leaves := map[string]interface{}{
				"a": strings.Repeat("a", 1000),
				"b": strings.Repeat("b", 1000),
				"c": strings.Repeat("c", 1000),
			}
			var value interface{} = leaves
			for range depth {
				value = map[string]interface{}{"wrapper": value}
			}
			input, err := json.Marshal(value)
			if err != nil {
				t.Fatal(err)
			}
			output, err := jsonToMarkdown(input)
			if err != nil {
				t.Fatal(err)
			}
			blocks := strings.Split(output, "\n\n")
			if len(blocks) < 2 {
				t.Fatalf("splittable wrapped object stayed in one block (%d bytes)", len(output))
			}
			seen := map[string]bool{}
			for _, block := range blocks {
				body := strings.TrimSuffix(strings.TrimPrefix(block, "```json\n"), "\n```")
				var chunk map[string]interface{}
				if err := json.Unmarshal([]byte(body), &chunk); err != nil {
					t.Fatal(err)
				}
				for range depth {
					child, ok := chunk["wrapper"].(map[string]interface{})
					if !ok {
						t.Fatalf("missing wrapper path in %s", body)
					}
					chunk = child
				}
				for key, val := range chunk {
					if val != leaves[key] {
						t.Fatalf("changed leaf %q", key)
					}
					if seen[key] {
						t.Fatalf("duplicated leaf %q", key)
					}
					seen[key] = true
				}
			}
			if len(seen) != len(leaves) {
				t.Fatalf("retained leaves = %v", seen)
			}
		})
	}
}

func TestCanSplitDictKeepsIndivisibleValues(t *testing.T) {
	for _, value := range []interface{}{
		nil,
		"text",
		map[string]interface{}{},
		map[string]interface{}{"only": strings.Repeat("x", 4000)},
	} {
		wrapped := map[string]interface{}{"outer": map[string]interface{}{"inner": value}}
		if canSplitDict(wrapped) {
			t.Fatalf("indivisible value marked splittable: %T", value)
		}
	}
}
