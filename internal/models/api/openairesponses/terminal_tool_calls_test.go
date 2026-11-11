package openairesponses

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/api"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestChatStreamTerminalToolCalls(t *testing.T) {
	t.Setenv("SSRF_WHITELIST", "127.0.0.1")
	const call = `{"type":"function_call","id":"fc_1","call_id":"call_1",` +
		`"name":"get_weather","arguments":"{\"city\":\"Paris\"}","status":"completed"}`
	const added = `{"type":"response.output_item.added","output_index":0,"item":` +
		`{"type":"function_call","id":"fc_1","call_id":"call_1","name":"get_weather","arguments":""}}`
	const delta = `{"type":"response.function_call_arguments.delta","output_index":0,` +
		`"item_id":"fc_1","delta":"{\"city\":\"Paris\"}"}`
	const done = `{"type":"response.output_item.done","output_index":0,"item":` + call + `}`
	for _, tc := range []struct {
		name       string
		events     []string
		output     string
		incomplete bool
		wantCalls  int
	}{
		{name: "terminal only", output: call, wantCalls: 1},
		{name: "announced without arguments", events: []string{added}, output: call, wantCalls: 1},
		{name: "deltas without announcement", events: []string{delta}, output: call, wantCalls: 1},
		{name: "deltas are not duplicated", events: []string{added, delta}, output: call, wantCalls: 1},
		{name: "done item is not duplicated", events: []string{added, done}, output: call, wantCalls: 1},
		{
			name: "missing parallel call", events: []string{added, delta, done},
			output: call + `,{"type":"function_call","id":"fc_2","call_id":"call_2",` +
				`"name":"get_weather","arguments":"{\"city\":\"Tokyo\"}","status":"completed"}`,
			wantCalls: 2,
		},
		{
			name: "indices without item ids", wantCalls: 1,
			events: []string{`{"type":"response.output_item.added","output_index":0,"item":` +
				`{"type":"function_call","call_id":"call_1","name":"get_weather","arguments":""}}`},
			output: `{"type":"function_call","call_id":"call_1","name":"get_weather",` +
				`"arguments":"{\"city\":\"Paris\"}"}`,
		},
		{name: "incomplete remains length", output: call, incomplete: true, wantCalls: 1},
		{
			name: "reasoning before tool", wantCalls: 1,
			output: `{"type":"reasoning","id":"rs_1","summary":[],"encrypted_content":"enc"},` + call,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			terminalType, status, details := "response.completed", "completed", ""
			if tc.incomplete {
				terminalType, status = "response.incomplete", "incomplete"
				details = `,"incomplete_details":{"reason":"max_output_tokens"}`
			}
			terminal := `{"type":"` + terminalType + `","response":{"status":"` + status + `"` +
				details + `,"output":[` + tc.output + `],` +
				`"usage":{"input_tokens":3,"output_tokens":7,"total_tokens":10}}}`
			body := sseBody(append(append([]string{}, tc.events...), terminal)...)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = w.Write([]byte(body))
			}))
			defer server.Close()

			ch, err := newTestClient(t, server, true).ChatStream(
				context.Background(), []api.Message{{Role: "user", Content: "weather?"}}, nil,
			)
			require.NoError(t, err)
			var final types.StreamResponse
			for ev := range ch {
				require.NotEqual(t, types.ResponseTypeError, ev.ResponseType, ev.Content)
				final = ev
			}
			require.True(t, final.Done)
			wantFinish := "tool_calls"
			if tc.incomplete {
				wantFinish = "length"
			}
			require.Equal(t, wantFinish, final.FinishReason)
			require.Len(t, final.ToolCalls, tc.wantCalls)
			first := final.ToolCalls[0]
			require.Equal(t, "call_1", first.ID)
			require.Equal(t, "function", first.Type)
			require.Equal(t, "get_weather", first.Function.Name)
			require.JSONEq(t, `{"city":"Paris"}`, first.Function.Arguments)
			if tc.name != "indices without item ids" {
				require.Equal(t, `"fc_1"`, string(first.ProviderMetadata[metadataToolCallItemID]))
			}
			if tc.wantCalls == 2 {
				require.Equal(t, "call_2", final.ToolCalls[1].ID)
				require.JSONEq(t, `{"city":"Tokyo"}`, final.ToolCalls[1].Function.Arguments)
			}
			require.NotNil(t, final.Usage)
			require.Equal(t, 10, final.Usage.TotalTokens)
			if tc.name == "reasoning before tool" {
				metadata, ok := final.Data["reasoning_metadata"].(types.ProviderMetadata)
				require.True(t, ok)
				var items []json.RawMessage
				require.NoError(t, json.Unmarshal(metadata[metadataReasoningItems], &items))
				require.Len(t, items, 1)
			}
		})
	}
}

func TestChatStreamRejectsMalformedTerminalToolCall(t *testing.T) {
	t.Setenv("SSRF_WHITELIST", "127.0.0.1")
	body := sseBody(`{"type":"response.completed","response":{"status":"completed",` +
		`"output":[{"type":"function_call","call_id":"call_1","name":"get_weather","arguments":42}]}}`)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(body))
	}))
	defer server.Close()

	ch, err := newTestClient(t, server, false).ChatStream(
		context.Background(), []api.Message{{Role: "user", Content: "weather?"}}, nil,
	)
	require.NoError(t, err)
	var final types.StreamResponse
	for ev := range ch {
		final = ev
	}
	require.Equal(t, types.ResponseTypeError, final.ResponseType)
	require.Equal(t, types.FinishReasonIncomplete, final.FinishReason)
	require.Contains(t, final.Content, "decode output item")
}
