package tools

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

type stringArgumentCaptureTool struct {
	BaseTool
	args json.RawMessage
}

func (t *stringArgumentCaptureTool) Execute(_ context.Context, args json.RawMessage) (*types.ToolResult, error) {
	t.args = append(json.RawMessage(nil), args...)
	return &types.ToolResult{Success: true, Output: "captured"}, nil
}

func TestRegistryPreservesNumericStringArguments(t *testing.T) {
	for _, tc := range []struct{ name, input, want string }{
		{"large ID", "9007199254740993", "9007199254740993"},
		{"negative ID", "-9007199254740993", "-9007199254740993"},
		{"uint64 maximum", "18446744073709551615", "18446744073709551615"},
		{"precise decimal", "0.1234567890123456789012345", "0.1234567890123456789012345"},
		{"exponent", "9.007199254740993e15", "9.007199254740993e15"},
		{"beyond float64 range", "1e400", "1e400"},
		{"below float64 range", "1e-400", "1e-400"},
		{"ordinary integer", "42", "42"},
		{"trailing zeros", "42.00", "42.00"},
		{"negative zero", "-0", "-0"},
		{"already a string", `"00042"`, "00042"},
		{"boolean true", "true", "true"},
		{"boolean false", "false", "false"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tool := &stringArgumentCaptureTool{BaseTool: NewBaseTool("capture_string", "Capture arguments",
				json.RawMessage(`{"type":"object","properties":{"value":{"type":"string"},`+
					`"enabled":{"type":"boolean"}},"required":["value"]}`))}
			registry := NewToolRegistry()
			registry.RegisterTool(tool)
			args := json.RawMessage(`{"value": ` + tc.input + `,"enabled":"true"}`)

			result, err := registry.ExecuteTool(context.Background(), tool.Name(), args)
			require.NoError(t, err)
			require.True(t, result.Success, result.Error)
			var received struct {
				Value   string `json:"value"`
				Enabled bool   `json:"enabled"`
			}
			require.NoError(t, json.Unmarshal(tool.args, &received))
			// Assert the string delivered to the tool; decoding expected values
			// through float64 would hide rounded identifiers and decimals.
			require.Equal(t, tc.want, received.Value)
			require.True(t, received.Enabled)
		})
	}
}

func TestCastParamsStringLeavesNonScalarValuesUnchanged(t *testing.T) {
	schema := json.RawMessage(`{"properties":{"value":{"type":"string"}}}`)
	for _, value := range []string{`null`, `[]`, `{"id":9007199254740993}`, `"123"`, `"hello"`} {
		t.Run(value, func(t *testing.T) {
			args := json.RawMessage(`{ "value": ` + value + ` }`)
			require.Equal(t, string(args), string(CastParams(args, schema)))
		})
	}
}
