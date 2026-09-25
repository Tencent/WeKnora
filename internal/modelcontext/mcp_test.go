package modelcontext

import (
	"encoding/json"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

const (
	mcpTestServer = "8f7a5b68-a7ab-4565-b6f3-1cae2578f040"
	mcpTestRef    = "mcpt_123456789abcdef"
)

func mcpTestDefinitions() []chat.Tool {
	return []chat.Tool{{Type: "function", Function: chat.FunctionDef{
		Name:        "discover_mcp_tools",
		Description: `Sources:` + "\n" + `{"server_id":"` + mcpTestServer + `","name":"amap-maps"}`,
		Parameters: json.RawMessage(`{"type":"object","properties":{"server_id":` +
			`{"type":"string","enum":["` + mcpTestServer + `"]}}}`),
	}}}
}

func TestMCPRoutingHandlesRoundTripWithoutTouchingExternalPayloads(t *testing.T) {
	r := NewRegistry(false)
	definitions := mcpTestDefinitions()
	encoded := r.EncodeTools(definitions)
	require.Contains(t, encoded[0].Function.Description, `"server_id":"ms1"`)
	require.Contains(t, string(encoded[0].Function.Parameters), `"enum":["ms1"]`)
	require.Contains(t, string(definitions[0].Function.Parameters), mcpTestServer, "executor schema stays durable")
	external := `{"type":"object","properties":{"server_id":{"const":"` + mcpTestServer +
		`"},"tool_ref":{"const":"` + mcpTestRef + `"}}}`
	output := `{"server_id":"` + mcpTestServer + `","tool_ref":"` + mcpTestRef + `","input_schema":` + external + `}`
	result := &types.ToolResult{Success: true, Output: output}
	modelOutput := r.ModelToolResultForTool("discover_mcp_tools", result)
	var description map[string]json.RawMessage
	require.NoError(t, json.Unmarshal([]byte(modelOutput), &description))
	require.JSONEq(t, `"ms1"`, string(description["server_id"]))
	require.JSONEq(t, `"mt1"`, string(description["tool_ref"]))
	require.JSONEq(t, external, string(description["input_schema"]))
	require.Equal(t, output, result.Output, "durable result for UI and storage must not change")
	replayed := r.ModelToolResultForTool("discover_mcp_tools", &types.ToolResult{Success: true, Output: modelOutput})
	require.Equal(t, modelOutput, replayed)
	calls := []types.LLMToolCall{
		{Function: types.FunctionCall{
			Name: "discover_mcp_tools", Arguments: `{"mode":"list_tools","server_id":"ms1"}`,
		}},
		{Function: types.FunctionCall{
			Name:      "call_mcp_tool",
			Arguments: `{"tool_ref":"mt1","arguments":{"server_id":"ms1","tool_ref":"mt1","url":"w1"}}`,
		}},
	}
	r.DecodeToolCalls(calls)
	require.JSONEq(t, `{"mode":"list_tools","server_id":"`+mcpTestServer+`"}`, calls[0].Function.Arguments)
	require.JSONEq(t, `{"tool_ref":"`+mcpTestRef+`","arguments":{"server_id":"ms1","tool_ref":"mt1","url":"w1"}}`,
		calls[1].Function.Arguments)
	for _, call := range calls {
		require.Equal(t, ArgumentResolutionResolved, call.ArgumentResolution)
		require.Empty(t, call.UnresolvedHandles)
	}
	require.Contains(t, calls[0].ModelArguments, `"ms1"`)
	require.Contains(t, calls[1].ModelArguments, `"mt1"`)
	require.Equal(t, output, r.ModelToolResultForTool("call_mcp_tool", result), "external call results are opaque")
	require.Equal(t, output, r.ModelToolResultForTool("mcp_external_tool", result))
}

func TestMCPRoutingReplayAndStreamUseTheSameRegistry(t *testing.T) {
	r := NewRegistry(false)
	r.EncodeTools(mcpTestDefinitions())
	output := `{"server_id":"` + mcpTestServer + `","tool_ref":"` + mcpTestRef + `"}`
	raw := `{"tool_ref":"` + mcpTestRef + `","arguments":{"id":"42"}}`
	history := []chat.Message{
		{Role: "user", Content: `Use server_id="` + mcpTestServer + `"`},
		{Role: "assistant", ToolCalls: []chat.ToolCall{
			{Function: chat.FunctionCall{Name: "call_mcp_tool", Arguments: raw}},
		}},
		{Role: "tool", Name: "discover_mcp_tools", Content: output},
	}
	encoded := r.EncodeMessages(history)
	require.Equal(t, `Use server_id="ms1"`, encoded[0].Content)
	require.JSONEq(t, `{"tool_ref":"mt1","arguments":{"id":"42"}}`, encoded[1].ToolCalls[0].Function.Arguments)
	require.JSONEq(t, `{"server_id":"ms1","tool_ref":"mt1"}`, encoded[2].Content)
	require.Equal(t, raw, history[1].ToolCalls[0].Function.Arguments)
	require.Equal(t, output, history[2].Content)
	require.Equal(t, encoded, r.EncodeMessages(encoded), "replay must not renumber handles")
	durable := "Use " + mcpTestServer + " with " + mcpTestRef + "."
	require.Equal(t, durable, r.DecodeOutputText("Use ms1 with mt1."))
	decoder := r.StreamDecoder()
	var streamed string
	for _, char := range "Use ms1 with mt1." {
		streamed += decoder.Feed(string(char))
	}
	streamed += decoder.Flush()
	require.Equal(t, durable, streamed)
	// A fresh execution remaps durable history; handles themselves never carry authority.
	fresh := NewRegistry(false)
	calls := []types.LLMToolCall{{Function: types.FunctionCall{
		Name: "call_mcp_tool", Arguments: `{"tool_ref":"mt1","arguments":{}}`,
	}}}
	fresh.DecodeToolCalls(calls)
	require.Equal(t, []string{"mt1"}, calls[0].UnresolvedHandles)
	fresh.EncodeTools(mcpTestDefinitions())
	require.Equal(t, encoded, fresh.EncodeMessages(history))
}

func TestMCPUnknownRoutingHandlesFailClosedOnlyAtTheEnvelope(t *testing.T) {
	r := NewRegistry(false)
	r.EncodeTools(mcpTestDefinitions())
	calls := []types.LLMToolCall{
		{Function: types.FunctionCall{Name: "discover_mcp_tools", Arguments: `{"server_id":"ms99","query":"ms98"}`}},
		{Function: types.FunctionCall{
			Name: "call_mcp_tool", Arguments: `{"tool_ref":"mt99","arguments":{"tool_ref":"mt98"}}`,
		}},
		{Function: types.FunctionCall{Name: "mcp_external", Arguments: `{"server_id":"ms99","tool_ref":"mt99"}`}},
	}
	r.DecodeToolCalls(calls)
	require.Equal(t, []string{"ms99"}, calls[0].UnresolvedHandles)
	require.Equal(t, []string{"mt99"}, calls[1].UnresolvedHandles)
	require.Empty(t, calls[2].UnresolvedHandles)
	for _, call := range calls {
		require.JSONEq(t, call.ModelArguments, call.Function.Arguments)
	}
	require.Equal(t, ArgumentResolutionUnresolved, calls[0].ArgumentResolution)
	require.Equal(t, ArgumentResolutionUnresolved, calls[1].ArgumentResolution)
}

func TestMCPStringArgumentsNormalizeOnlyOneCompleteObject(t *testing.T) {
	for _, tc := range []struct {
		name       string
		arguments  any
		normalized bool
	}{
		{"empty object", "{}", true},
		{"whitespace", " \n { } \t", true},
		{"nested business string", `{"query":"{\"id\":1}","items":[{"enabled":true}]}`, true},
		{"already an object", map[string]any{"query": "{}"}, false},
		{"null", nil, false},
		{"encoded null", "null", false},
		{"array", "[]", false},
		{"scalar", "123", false},
		{"empty string", "", false},
		{"malformed", `{"id":`, false},
		{"trailing content", `{} {}`, false},
		{"another encoding layer", `"{}"`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := NewRegistry(false)
			raw, err := json.Marshal(map[string]any{"tool_ref": mcpTestRef, "arguments": tc.arguments})
			require.NoError(t, err)
			calls := []types.LLMToolCall{{Function: types.FunctionCall{Name: "call_mcp_tool", Arguments: string(raw)}}}
			r.DecodeToolCalls(calls)
			require.Equal(t, string(raw), calls[0].ModelArguments)
			if tc.normalized {
				require.Equal(t, ArgumentResolutionResolved, calls[0].ArgumentResolution)
				var envelope map[string]json.RawMessage
				require.NoError(t, json.Unmarshal([]byte(calls[0].Function.Arguments), &envelope))
				require.JSONEq(t, tc.arguments.(string), string(envelope["arguments"]))
			} else {
				require.JSONEq(t, string(raw), calls[0].Function.Arguments)
				require.Equal(t, ArgumentResolutionUnchanged, calls[0].ArgumentResolution)
			}
			first := calls[0]
			r.DecodeToolCalls(calls)
			require.Equal(t, first, calls[0], "stream completion may decode the same call again")
		})
	}
}

func TestMCPStringArgumentsDecodeResourcesAndKeepOtherToolsOpaque(t *testing.T) {
	r := NewRegistry(false)
	const resource = "resource://AbCdEfGhIjKlMnOpQrStUv"
	require.Equal(t, "res://0001", r.CompactKnownText(resource))
	r.ModelToolResultForTool("discover_mcp_tools", &types.ToolResult{
		Success: true, Output: `{"tool_ref":"` + mcpTestRef + `"}`,
	})
	raw := `{"tool_ref":"mt1","arguments":"{\"file\":\"res://0001\",\"knowledge_id\":\"d1\"}"}`
	calls := []types.LLMToolCall{{Function: types.FunctionCall{Name: "call_mcp_tool", Arguments: raw}}}
	r.DecodeToolCalls(calls)
	require.JSONEq(t, `{"tool_ref":"`+mcpTestRef+`","arguments":{"file":"`+resource+`","knowledge_id":"d1"}}`,
		calls[0].Function.Arguments)
	require.Equal(t, raw, calls[0].ModelArguments)
	require.Empty(t, calls[0].UnresolvedHandles)
	for _, name := range []string{"mcp_external_tool", "discover_mcp_tools", "read_file"} {
		original := `{"arguments":"{\"id\":1}"}`
		calls := []types.LLMToolCall{{Function: types.FunctionCall{Name: name, Arguments: original}}}
		r.DecodeToolCalls(calls)
		require.JSONEq(t, original, calls[0].Function.Arguments)
	}
	unknown := []types.LLMToolCall{{Function: types.FunctionCall{
		Name: "call_mcp_tool", Arguments: `{"tool_ref":"mt1","arguments":"{\"file\":\"res://9999\"}"}`,
	}}}
	r.DecodeToolCalls(unknown)
	require.Equal(t, []string{"res://9999"}, unknown[0].UnresolvedHandles)
	require.Equal(t, ArgumentResolutionPartiallyResolved, unknown[0].ArgumentResolution)
}
