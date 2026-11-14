package tools

import (
	"encoding/json"
	"fmt"
	"testing"
)

func TestCastParamsPreservesIntegerLiterals(t *testing.T) {
	schema := json.RawMessage(`{"properties":{"id":{"type":"integer"},"enabled":{"type":"boolean"}}}`)
	for _, number := range []string{
		"0", "-0", "42", "9007199254740993", "-9007199254740993",
		"9223372036854775807", "-9223372036854775808", "18446744073709551615",
	} {
		for _, enabled := range []string{`true`, `"true"`} {
			t.Run(number+enabled, func(t *testing.T) {
				args := json.RawMessage(fmt.Sprintf(`{ "id": %s, "enabled": %s }`, number, enabled))
				result := CastParams(args, schema)
				var fields map[string]json.RawMessage
				if err := json.Unmarshal(result, &fields); err != nil {
					t.Fatal(err)
				}
				if string(fields["id"]) != number || string(fields["enabled"]) != "true" {
					t.Fatalf("expected id %s and true, got %s", number, result)
				}
				if enabled == "true" && string(result) != string(args) {
					t.Fatalf("already typed arguments should remain unchanged: %s", result)
				}
			})
		}
	}
}

func TestCastParamsIntegerConversionControls(t *testing.T) {
	schema := json.RawMessage(`{"properties":{"id":{"type":"integer"}}}`)
	for _, tc := range []struct{ input, want string }{
		{`"9007199254740993"`, "9007199254740993"},
		{"42.0", "42"},
		{"4.2e1", "42"},
		{"1.5", "1.5"},
		{`"invalid"`, `"invalid"`},
		{"null", "null"},
		{"true", "true"},
	} {
		result := CastParams(json.RawMessage(`{"id":`+tc.input+`}`), schema)
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(result, &fields); err != nil {
			t.Fatal(err)
		}
		if string(fields["id"]) != tc.want {
			t.Errorf("%s: want %s, got %s", tc.input, tc.want, result)
		}
	}
}
