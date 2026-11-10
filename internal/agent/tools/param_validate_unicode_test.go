package tools

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestValidateParamsUnicodeLength(t *testing.T) {
	cases := []struct {
		name, value, constraint, wantError string
	}{
		{"Chinese maximum", "你好", `"maxLength":2`, ""},
		{"emoji maximum", "😀", `"maxLength":1`, ""},
		{"accent maximum", "é", `"maxLength":1`, ""},
		{"Chinese minimum", "你", `"minLength":2`, "at least 2 characters"},
		{"emoji minimum", "😀", `"minLength":2`, "at least 2 characters"},
		{"mixed exact length", "A你😀", `"minLength":3,"maxLength":3`, ""},
		{"combining code points", "e\u0301", `"minLength":2,"maxLength":2`, ""},
		{"too many code points", "你好啊", `"maxLength":2`, "at most 2 characters"},
		{"empty minimum", "", `"minLength":1`, "at least 1 characters"},
		{"ASCII exact length", "abc", `"minLength":3,"maxLength":3`, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			args, err := json.Marshal(map[string]string{"text": tc.value})
			if err != nil {
				t.Fatal(err)
			}
			schema := json.RawMessage(`{"type":"object","properties":{"text":{"type":"string",` + tc.constraint + `}}}`)
			errs := ValidateParams(args, schema)
			if tc.wantError == "" {
				if len(errs) != 0 {
					t.Fatalf("valid code-point length rejected: %v", errs)
				}
			} else if len(errs) != 1 || errs[0].Param != "text" || !strings.Contains(errs[0].Message, tc.wantError) {
				t.Fatalf("expected %q for text, got %v", tc.wantError, errs)
			}
		})
	}
}
