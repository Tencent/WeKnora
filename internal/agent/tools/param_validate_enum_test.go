package tools

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateParamsEnumUsesJSONValues(t *testing.T) {
	cases := []struct {
		name     string
		property string
		value    string
		valid    bool
	}{
		{"number is not a string", `{"enum":["1"]}`, `1`, false},
		{"string is not a number", `{"enum":[1]}`, `"1"`, false},
		{"boolean is not a string", `{"enum":["true"]}`, `true`, false},
		{"array item types matter", `{"type":"array","enum":[["1"]]}`, `[1]`, false},
		{"array boundaries matter", `{"type":"array","enum":[["a b"]]}`, `["a","b"]`, false},
		{"object value types matter", `{"type":"object","enum":[{"x":"1"}]}`, `{"x":1}`, false},
		{"object structure matters", `{"type":"object","enum":[{"a":"b c:d"}]}`, `{"a":"b","c":"d"}`, false},
		{"same number different spelling", `{"enum":[1]}`, `1.0`, true},
		{"same string", `{"type":"string","enum":["fast","deep"]}`, `"deep"`, true},
		{"same boolean", `{"type":"boolean","enum":[true]}`, `true`, true},
		{"same nested array", `{"type":"array","enum":[[1,{"x":true}]]}`, `[1.0,{"x":true}]`, true},
		{"object key order irrelevant", `{"type":"object","enum":[{"a":1,"b":2}]}`, `{"b":2,"a":1}`, true},
		{"mixed enum", `{"enum":["1",true,2]}`, `2`, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			schema := json.RawMessage(`{"type":"object","properties":{"value":` + tc.property + `}}`)
			args := json.RawMessage(`{"value":` + tc.value + `}`)
			// Exercise the same casting/validation sequence as Registry.Execute.
			errs := ValidateParams(CastParams(args, schema), schema)
			if tc.valid {
				assert.Empty(t, errs)
			} else {
				require.Len(t, errs, 1)
				assert.Equal(t, "value", errs[0].Param)
				assert.Contains(t, errs[0].Message, "one of")
			}
		})
	}
}
