package tools

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

type arrayArgumentCaptureTool struct {
	BaseTool
	args json.RawMessage
}

func (t *arrayArgumentCaptureTool) Execute(_ context.Context, args json.RawMessage) (*types.ToolResult, error) {
	t.args = append(json.RawMessage(nil), args...)
	return &types.ToolResult{Success: true, Output: "captured"}, nil
}

func TestRegistryPreservesSerializedArrayValues(t *testing.T) {
	for _, tc := range []struct {
		name, serialized string
	}{
		{"large integers", `[9007199254740993,-9007199254740993,9223372036854775807,18446744073709551615]`},
		{"nested values", `[{"id":9007199254740993,"values":[9223372036854775807]}]`},
		{"decimal precision", `[0.1234567890123456789012345,9.007199254740993e15]`},
		{"beyond float64 range", `[1e400]`},
		{"scalar controls", `["hello",true,false,null,0]`},
		{"empty array", `[]`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tool := &arrayArgumentCaptureTool{BaseTool: NewBaseTool("capture_array", "Capture arguments",
				json.RawMessage(`{"type":"object","properties":{"values":{"type":"array"},`+
					`"enabled":{"type":"boolean"}},"required":["values"]}`))}
			registry := NewToolRegistry()
			registry.RegisterTool(tool)
			args, err := json.Marshal(map[string]any{"values": tc.serialized, "enabled": "true"})
			require.NoError(t, err)

			result, err := registry.ExecuteTool(context.Background(), tool.Name(), args)
			require.NoError(t, err)
			require.True(t, result.Success, result.Error)
			var received map[string]json.RawMessage
			require.NoError(t, json.Unmarshal(tool.args, &received))
			// Compare raw tokens: decoding the assertion through float64 would
			// round both sides and conceal the regression.
			require.Equal(t, tc.serialized, string(received["values"]))
			require.Equal(t, "true", string(received["enabled"]))
		})
	}
}

func TestCastParamsSerializedArrayControls(t *testing.T) {
	schema := json.RawMessage(`{"properties":{"values":{"type":"array"}}}`)
	for _, tc := range []struct{ input, want string }{
		{`["unfinished",`, `"[\"unfinished\","`},
		{`[1] trailing`, `"[1] trailing"`},
		{"ordinary text", `["ordinary text"]`},
		{`{"id":1}`, `["{\"id\":1}"]`},
	} {
		t.Run(tc.input, func(t *testing.T) {
			args, err := json.Marshal(map[string]string{"values": tc.input})
			require.NoError(t, err)
			var fields map[string]json.RawMessage
			require.NoError(t, json.Unmarshal(CastParams(args, schema), &fields))
			require.Equal(t, tc.want, string(fields["values"]))
		})
	}
}
