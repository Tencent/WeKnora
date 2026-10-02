package tools

import (
	"encoding/json"
	"fmt"
	"testing"
)

func TestCastParamsPreservesUntouchedNumbers(t *testing.T) {
	cases := []struct {
		name, property, value string
	}{
		{"extra integer", "", `9007199254740993`},
		{"declared number", `,"payload":{"type":"number"}`, `9007199254740993`},
		{"nested object", `,"payload":{"type":"object"}`, `{"id":9223372036854775807}`},
		{"existing array", `,"payload":{"type":"array"}`, `[9007199254740993,{"id":9223372036854775807}]`},
		{"precise decimal", `,"payload":{"type":"number"}`, `0.1234567890123456789012345`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			schema := json.RawMessage(`{"type":"object","properties":{"enabled":{"type":"boolean"}` + tc.property + `}}`)
			args := json.RawMessage(fmt.Sprintf(`{"enabled":"true","payload":%s}`, tc.value))
			result := CastParams(args, schema)
			var parsed map[string]json.RawMessage
			if err := json.Unmarshal(result, &parsed); err != nil {
				t.Fatal(err)
			}
			if string(parsed["enabled"]) != "true" {
				t.Fatalf("expected boolean conversion, got %s", result)
			}
			if string(parsed["payload"]) != tc.value {
				t.Fatalf("untouched argument changed: want %s, got %s", tc.value, parsed["payload"])
			}
		})
	}
}

func TestCastParamsPreservesOriginalOnNoConversion(t *testing.T) {
	schema := json.RawMessage(`{"type":"object","properties":{"enabled":{"type":"boolean"}}}`)
	for _, args := range []string{
		`{ "enabled": true, "id": 9007199254740993 }`,
		`{"enabled":"true",`,
		`{"enabled":"true"} {"id":1}`,
	} {
		if result := CastParams(json.RawMessage(args), schema); string(result) != args {
			t.Errorf("expected original input %s, got %s", args, result)
		}
	}
}
