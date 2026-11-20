package tools

import (
	"encoding/json"
	"fmt"
	"testing"
)

func TestCastParamsStringToNumberKeepsLiteral(t *testing.T) {
	cases := []struct{ name, value string }{
		{"large integer", `9007199254740993`},
		{"negative large integer", `-9007199254740993`},
		{"long decimal", `0.1234567890123456789`},
		{"trailing zero decimal", `12.50`},
		{"exponent", `1e5`},
		{"plain integer", `3`},
		{"plain decimal", `3.14`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			schema := json.RawMessage(`{"type":"object","properties":{"payload":{"type":"number"}}}`)
			args := json.RawMessage(fmt.Sprintf(`{"payload":"%s"}`, tc.value))
			result := CastParams(args, schema)
			var parsed map[string]json.RawMessage
			if err := json.Unmarshal(result, &parsed); err != nil {
				t.Fatal(err)
			}
			if string(parsed["payload"]) != tc.value {
				t.Fatalf("expected literal %s, got %s", tc.value, parsed["payload"])
			}
		})
	}
}

// A string ParseFloat accepts but JSON rejects must not reach the output as a
// bare literal; the legacy float64 normalization still applies.
func TestCastParamsStringToNumberNonJSONLiterals(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"hex float", `"0x1p-2"`, `0.25`},
		{"positive sign", `"+5"`, `5`},
		{"leading zero", `"0123"`, `123`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			schema := json.RawMessage(`{"type":"object","properties":{"payload":{"type":"number"}}}`)
			args := json.RawMessage(fmt.Sprintf(`{"payload":%s}`, tc.in))
			result := CastParams(args, schema)
			var parsed map[string]json.RawMessage
			if err := json.Unmarshal(result, &parsed); err != nil {
				t.Fatal(err)
			}
			if string(parsed["payload"]) != tc.want {
				t.Fatalf("expected %s, got %s", tc.want, parsed["payload"])
			}
		})
	}
}

// A non-finite string cannot be cast at all and stays untouched for
// validation to reject.
func TestCastParamsStringToNumberNonFiniteStaysString(t *testing.T) {
	schema := json.RawMessage(`{"type":"object","properties":{"payload":{"type":"number"}}}`)
	args := json.RawMessage(`{"payload":"inf"}`)
	if result := CastParams(args, schema); string(result) != string(args) {
		t.Fatalf("expected original input %s, got %s", args, result)
	}
}
