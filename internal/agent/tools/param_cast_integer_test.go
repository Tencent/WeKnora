package tools

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
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

func TestMCPIntegerValidationAfterExactCasting(t *testing.T) {
	tool := NewMCPTool(&types.MCPService{ID: "svc"}, &types.MCPTool{
		Name: "integer_argument",
		InputSchema: json.RawMessage(`{"type":"object","properties":{
			"id":{"type":"integer","enum":[9007199254740993]}
		},"required":["id"]}`),
	}, nil, nil, 0)
	for _, literal := range []string{"9007199254740993.0", "9.007199254740993e15"} {
		args := CastParams(json.RawMessage(`{"id":`+literal+`}`), tool.Parameters())
		require.NoError(t, tool.ValidateArguments(args), "literal %s", literal)
		var received struct {
			ID int64 `json:"id"`
		}
		require.NoError(t, json.Unmarshal(args, &received))
		require.Equal(t, int64(9007199254740993), received.ID)
	}

	// Fractions rounded to whole float64 values must still fail the MCP schema
	// before reaching the transport (the manager is intentionally nil).
	tool = NewMCPTool(&types.MCPService{ID: "svc"}, &types.MCPTool{
		Name:        "integer_only",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"id":{"type":"integer"}}}`),
	}, nil, nil, 0)
	registry := NewToolRegistry()
	registry.RegisterTool(tool)
	for _, literal := range []string{"1.0000000000000001", "9007199254740992.5"} {
		result, err := registry.ExecuteTool(catalogTestContext(), tool.Name(), json.RawMessage(`{"id":`+literal+`}`))
		require.NoError(t, err)
		require.False(t, result.Success)
		require.Contains(t, result.Error, "Parameter validation failed")
	}
}

func TestCastParamsIntegerConversionControls(t *testing.T) {
	schema := json.RawMessage(`{"properties":{"id":{"type":"integer"}}}`)
	for _, tc := range []struct{ input, want string }{
		{`"9007199254740993"`, "9007199254740993"},
		{"42.0", "42"},
		{"4.2e1", "42"},
		{"9007199254740993.0", "9007199254740993"},
		{"9.007199254740993e15", "9007199254740993"},
		{"-9007199254740993.0", "-9007199254740993"},
		{"9223372036854775807.0", "9223372036854775807"},
		{"-9223372036854775808.0", "-9223372036854775808"},
		{"9223372036854775808.0", "9223372036854775808.0"},
		{"-9223372036854775809.0", "-9223372036854775809.0"},
		{"9007199254740992.5", "9007199254740992.5"},
		{"1.0000000000000001", "1.0000000000000001"},
		{"1e-1000000000", "1e-1000000000"},
		{"0e-1000000000", "0"},
		{"1e1000000000", "1e1000000000"},
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
