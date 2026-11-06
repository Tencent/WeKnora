package tools

import (
	"encoding/json"
	"fmt"
	"testing"
)

// Numeric JSON arguments declared as strings must keep the number's literal
// text. Decoding through interface{} turns them into float64 and silently
// rewrites large identifiers and precise decimals.
func TestCastParamsNumberToStringKeepsLiteral(t *testing.T) {
	cases := []struct{ name, literal, want string }{
		{"large integer", `9007199254740993`, `9007199254740993`},
		{"negative large integer", `-9007199254740993`, `-9007199254740993`},
		{"max uint64", `18446744073709551615`, `18446744073709551615`},
		{"long decimal", `0.1234567890123456789012345`, `0.1234567890123456789012345`},
		{"trailing zero decimal", `12.50`, `12.50`},
		{"exponent", `1e5`, `1e5`},
		{"tiny exponent", `1e-400`, `1e-400`},
		{"plain integer", `7`, `7`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			schema := json.RawMessage(`{"type":"object","properties":{"id":{"type":"string"}}}`)
			args := json.RawMessage(fmt.Sprintf(`{"id":%s}`, tc.literal))
			result := CastParams(args, schema)

			var parsed map[string]json.RawMessage
			if err := json.Unmarshal(result, &parsed); err != nil {
				t.Fatal(err)
			}
			var got string
			if err := json.Unmarshal(parsed["id"], &got); err != nil {
				t.Fatalf("expected a string, got %s: %v", parsed["id"], err)
			}
			if got != tc.want {
				t.Fatalf("expected %q, got %q", tc.want, got)
			}
		})
	}
}

// Values that are not JSON numbers keep the previous behaviour: booleans are
// stringified, while null, objects and arrays are not touched.
func TestCastParamsOtherTypesToString(t *testing.T) {
	cases := []struct {
		name  string
		value string
		want  string
	}{
		{"true", `true`, `"true"`},
		{"false", `false`, `"false"`},
		{"existing string", `"42"`, `"42"`},
		{"null", `null`, `null`},
		{"object", `{"a":1}`, `{"a":1}`},
		{"array", `[1,2]`, `[1,2]`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			schema := json.RawMessage(`{"type":"object","properties":{"id":{"type":"string"}}}`)
			args := json.RawMessage(fmt.Sprintf(`{"id":%s}`, tc.value))
			result := CastParams(args, schema)

			var parsed map[string]json.RawMessage
			if err := json.Unmarshal(result, &parsed); err != nil {
				t.Fatal(err)
			}
			if string(parsed["id"]) != tc.want {
				t.Fatalf("expected %s, got %s", tc.want, parsed["id"])
			}
		})
	}
}

// A numeric argument that is already a string must not be re-quoted, and
// untouched properties keep their original bytes.
func TestCastParamsNumberToStringLeavesOtherArgsAlone(t *testing.T) {
	schema := json.RawMessage(`{"type":"object","properties":{"id":{"type":"string"}}}`)
	args := json.RawMessage(`{"id":9007199254740993,"other":9007199254740993}`)
	result := CastParams(args, schema)

	var parsed map[string]json.RawMessage
	if err := json.Unmarshal(result, &parsed); err != nil {
		t.Fatal(err)
	}
	if string(parsed["id"]) != `"9007199254740993"` {
		t.Fatalf("expected quoted literal, got %s", parsed["id"])
	}
	if string(parsed["other"]) != `9007199254740993` {
		t.Fatalf("untouched argument changed: %s", parsed["other"])
	}
}
