package adapters

// anthropic_tools_test.go: ported from v1 chat/anthropic_tools_test.go
// (upstream 2026-09) onto the v2 adapter seams — BuildChatRequest → wire JSON,
// aggregateAnthropicSSE / TranslateStreamEvent for the stream paths.

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/invoke"
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
	opts := &invoke.ChatOptions{
		Messages: []invoke.Message{
			{Role: invoke.RoleUser, Content: []invoke.Part{{Text: "find both"}}},
			{Role: invoke.RoleAssistant, ToolCalls: []invoke.ToolCall{
				{ID: "a", Function: invoke.FunctionCall{Name: "lookup", Arguments: `{"id":"i1"}`}},
				{ID: "b", Function: invoke.FunctionCall{Name: "lookup", Arguments: `{"id":"i2"}`}},
			}},
			{Role: invoke.RoleTool, ToolCallID: "a", Content: []invoke.Part{{Text: "  keep whitespace  "}}},
			{Role: invoke.RoleTool, ToolCallID: "b"},
		},
		Tools: []invoke.ToolDef{
			{
				Name:        "lookup",
				Description: strings.Repeat("Detailed usage ", 40),
				Parameters:  schema,
			},
		},
		ToolChoice:        "required",
		ParallelToolCalls: &parallel,
	}
	adapter := &AnthropicAdapter{}
	req, err := adapter.BuildChatRequest(
		invoke.Endpoint{Credentials: invoke.Credentials{APIKey: "test"}},
		"claude-test",
		opts,
	)
	require.NoError(t, err)
	// Content is `any` on the wire struct: a JSON round-trip would hand back
	// []interface{}, so re-decode each message body into the typed blocks.
	var wire struct {
		Tools      []anthropicTool      `json:"tools"`
		ToolChoice *anthropicToolChoice `json:"tool_choice"`
		Messages   []struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		} `json:"messages"`
	}
	require.NoError(t, json.Unmarshal(req.Body, &wire))
	require.Len(t, wire.Tools, 1)
	require.Equal(t, opts.Tools[0].Description, wire.Tools[0].Description)
	require.JSONEq(t, string(schema), string(wire.Tools[0].InputSchema))
	require.Equal(t, "any", wire.ToolChoice.Type)
	require.True(t, *wire.ToolChoice.DisableParallelToolUse)
	require.Len(t, wire.Messages, 3)
	var assistant []anthropicContentBlock
	require.NoError(t, json.Unmarshal(wire.Messages[1].Content, &assistant))
	require.Len(t, assistant, 2)
	require.Equal(t, "tool_use", assistant[0].Type)
	require.Equal(t, "a", assistant[0].ID)
	var results []anthropicContentBlock
	require.NoError(t, json.Unmarshal(wire.Messages[2].Content, &results))
	require.Equal(t, "user", wire.Messages[2].Role)
	require.Len(t, results, 2)
	// Tool payloads round-trip verbatim — no trimming (JSON whitespace is
	// part of the tool result the next turn replays).
	require.Equal(t, "  keep whitespace  ", results[0].Content)
	require.Equal(t, "b", results[1].ToolUseID)
	require.Contains(t, string(req.Body), `"tool_use_id":"b","content":""`)
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
			parsed, err := aggregateAnthropicSSE([]byte(body))
			require.NoError(t, err)
			require.Equal(t, test.reason, parsed.FinishReason)
			require.JSONEq(t, `{"id":"42"}`, parsed.ToolCalls[0].Function.Arguments)
			if test.name == "complete" {
				require.Len(t, parsed.ToolCalls, 2)
				require.Equal(t, "{}", parsed.ToolCalls[1].Function.Arguments)
			} else {
				require.Len(t, parsed.ToolCalls, 1, "unclosed tool_use must not be emitted for execution")
			}
			if test.name != "complete" {
				return // no message_stop: the bridge emits no Done; the entry synthesizes the terminal
			}
			// The streaming bridge must deliver the same terminal state the
			// aggregate does (v1 Done-chunk shape: reason + assembled calls).
			adapter := &AnthropicAdapter{}
			state := invoke.NewStreamBridgeState()
			demux := invoke.NewDemuxer("text/event-stream", strings.NewReader(body))
			var done *invoke.StreamEvent
			for {
				chunk, ok := demux.Next()
				if !ok {
					break
				}
				events, err := adapter.TranslateStreamEvent(state, chunk)
				require.NoError(t, err)
				for _, event := range events {
					if event.Done != nil {
						done = event
					}
				}
			}
			require.NotNil(t, done)
			require.Equal(t, test.reason, done.Done.FinishReason)
			require.Equal(t, parsed.ToolCalls, done.Done.ToolCalls)
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
	parsed := parseAnthropicResponse(&response)
	require.Equal(t, "Checking", parsed.Content)
	require.Len(t, parsed.ToolCalls, 1)
	require.Equal(t, "a", parsed.ToolCalls[0].ID)
	require.JSONEq(t, `{"id":"42"}`, parsed.ToolCalls[0].Function.Arguments)
}
