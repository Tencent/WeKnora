package chat

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestAnthropicToolsPreserveParallelHistoryAndSchema(t *testing.T) {
	schema := json.RawMessage(
		`{"type":"object","$defs":{"id":{"type":"string"}},` +
			`"properties":{"id":{"$ref":"#/$defs/id"}},` +
			`"oneOf":[{"required":["id"]}],"additionalProperties":false}`,
	)
	parallel := false
	opts := &ChatOptions{
		Tools: []Tool{
			{
				Type: "function",
				Function: FunctionDef{
					Name:        "lookup",
					Description: strings.Repeat("Detailed usage ", 40),
					Parameters:  schema,
				},
			},
		},
		ToolChoice:        "required",
		ParallelToolCalls: &parallel,
	}
	model := &AnthropicChat{modelName: "claude-test"}
	req := model.buildRequest(context.Background(), []Message{
		{Role: "user", Content: "find both"},
		{Role: "assistant", ToolCalls: []ToolCall{
			{ID: "a", Function: FunctionCall{Name: "lookup", Arguments: `{"id":"i1"}`}},
			{ID: "b", Function: FunctionCall{Name: "lookup", Arguments: `{"id":"i2"}`}},
		}},
		{Role: "tool", ToolCallID: "a", Content: "  keep whitespace  "},
		{Role: "tool", ToolCallID: "b", Content: ""},
	}, opts)
	require.Len(t, req.Tools, 1)
	require.Equal(t, opts.Tools[0].Function.Description, req.Tools[0].Description)
	require.JSONEq(t, string(schema), string(req.Tools[0].InputSchema))
	require.Equal(t, "any", req.ToolChoice.Type)
	require.True(t, *req.ToolChoice.DisableParallelToolUse)
	require.Len(t, req.Messages, 3)
	assistant := req.Messages[1].Content.([]anthropicContentBlock)
	require.Len(t, assistant, 2)
	require.Equal(t, "tool_use", assistant[0].Type)
	require.Equal(t, "a", assistant[0].ID)
	results := req.Messages[2].Content.([]anthropicContentBlock)
	require.Equal(t, "user", req.Messages[2].Role)
	require.Len(t, results, 2)
	require.Equal(t, "  keep whitespace  ", results[0].Content)
	require.Equal(t, "b", results[1].ToolUseID)
	raw, err := json.Marshal(req)
	require.NoError(t, err)
	require.Contains(t, string(raw), `"tool_use_id":"b","content":""`)
}

func TestAnthropicToolStreamParallelFragmentsAndIncompleteCalls(t *testing.T) {
	prefix := "data: {\"type\":\"content_block_start\",\"index\":1," +
		"\"content_block\":{\"type\":\"tool_use\",\"id\":\"a\",\"name\":\"lookup\"," +
		"\"input\":{}}}\n\ndata: {\"type\":\"content_block_delta\",\"index\":1," +
		"\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"{\\\"id\\\":\"}}\n\ndata: " +
		"{\"type\":\"content_block_delta\",\"index\":1," +
		"\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"\\\"42\\\"}\"}}\n\ndata: " +
		"{\"type\":\"content_block_stop\",\"index\":1}\n\ndata: " +
		"{\"type\":\"content_block_start\",\"index\":2," +
		"\"content_block\":{\"type\":\"tool_use\",\"id\":\"b\",\"name\":\"list\"," +
		"\"input\":{}}}\n\n"
	for _, test := range []struct{ name, tail, reason string }{
		{"complete", "data: {\"type\":\"content_block_stop\",\"index\":2}\n\ndata: " +
			"{\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"tool_use\"}}\n\ndata: " +
			"{\"type\":\"message_stop\"}\n\n", "tool_use"},
		{"cut off", "", types.FinishReasonIncomplete},
		{
			"missing block stop",
			"data: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"tool_use\"}}\n\n",
			types.FinishReasonIncomplete,
		},
		{"token limit", "data: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"max_tokens\"}}\n\n", "length"},
	} {
		t.Run(test.name, func(t *testing.T) {
			body := prefix + test.tail
			parsed, err := parseAnthropicSSE(strings.NewReader(body))
			require.NoError(t, err)
			require.Equal(t, test.reason, parsed.FinishReason)
			require.JSONEq(t, `{"id":"42"}`, parsed.ToolCalls[0].Function.Arguments)
			if test.name == "complete" {
				require.Len(t, parsed.ToolCalls, 2)
				require.Equal(t, "{}", parsed.ToolCalls[1].Function.Arguments)
			} else {
				require.Len(t, parsed.ToolCalls, 1, "unclosed tool_use must not be emitted for execution")
			}
			channel := make(chan types.StreamResponse, 8)
			processAnthropicStream(
				context.Background(),
				"test",
				&http.Response{Body: io.NopCloser(strings.NewReader(body))},
				channel,
			)
			var last types.StreamResponse
			for chunk := range channel {
				last = chunk
			}
			require.True(t, last.Done)
			require.Equal(t, test.reason, last.FinishReason)
			require.Equal(t, parsed.ToolCalls, last.ToolCalls)
		})
	}
}

func TestAnthropicNonStreamingToolUse(t *testing.T) {
	var response anthropicResponse
	require.NoError(
		t,
		json.Unmarshal(
			[]byte(
				`{"content":[{"type":"text","text":"Checking"},{"type":"tool_use",`+
					`"id":"a","name":"lookup","input":{"id":"42"}}],`+
					`"stop_reason":"tool_use"}`,
			),
			&response,
		),
	)
	parsed := (&AnthropicChat{}).parseResponse(&response)
	require.Equal(t, "Checking", parsed.Content)
	require.Len(t, parsed.ToolCalls, 1)
	require.Equal(t, "a", parsed.ToolCalls[0].ID)
	require.JSONEq(t, `{"id":"42"}`, parsed.ToolCalls[0].Function.Arguments)
}
