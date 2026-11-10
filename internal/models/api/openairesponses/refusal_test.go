package openairesponses

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/api"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestChatPreservesRefusal(t *testing.T) {
	t.Setenv("SSRF_WHITELIST", "127.0.0.1")
	for _, tc := range []struct {
		name    string
		content string
		want    string
	}{
		{
			name:    "refusal only",
			content: `[{"type":"refusal","refusal":"I cannot help with that request."}]`,
			want:    "I cannot help with that request.",
		},
		{
			name: "mixed content stays ordered",
			content: `[{"type":"output_text","text":"First. "},` +
				`{"type":"refusal","refusal":"I cannot help with that request."},` +
				`{"type":"unknown","text":"ignored"},{"type":"output_text","text":" Last."}]`,
			want: "First. I cannot help with that request. Last.",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(`{"status":"completed","output":[{"type":"message","content":` +
					tc.content + `}],"usage":{"input_tokens":3,"output_tokens":7,"total_tokens":10}}`))
			}))
			defer server.Close()

			resp, err := newTestClient(t, server, false).Chat(
				context.Background(), []api.Message{{Role: "user", Content: "hi"}}, nil,
			)
			require.NoError(t, err)
			require.Equal(t, tc.want, resp.Content)
			require.Equal(t, "stop", resp.FinishReason)
			require.Empty(t, resp.ToolCalls)
			require.Equal(t, 10, resp.Usage.TotalTokens)
		})
	}
}

func TestChatStreamPreservesRefusal(t *testing.T) {
	t.Setenv("SSRF_WHITELIST", "127.0.0.1")
	const refusal = "I cannot help with that request."
	body := sseBody(
		`{"type":"response.refusal.delta","item_id":"msg_1","output_index":0,`+
			`"content_index":0,"delta":"I cannot help "}`,
		`{"type":"response.refusal.delta","item_id":"msg_1","output_index":0,`+
			`"content_index":0,"delta":"with that request."}`,
		`{"type":"response.refusal.done","item_id":"msg_1","output_index":0,"content_index":0,"refusal":"`+refusal+`"}`,
		`{"type":"response.output_item.done","output_index":0,"item":{"type":"message",`+
			`"id":"msg_1","content":[{"type":"refusal","refusal":"`+refusal+`"}]}}`,
		`{"type":"response.completed","response":{"status":"completed",`+
			`"usage":{"input_tokens":3,"output_tokens":7,"total_tokens":10}}}`,
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(body))
	}))
	defer server.Close()

	ch, err := newTestClient(t, server, false).ChatStream(
		context.Background(), []api.Message{{Role: "user", Content: "hi"}}, nil,
	)
	require.NoError(t, err)
	var answer string
	var final types.StreamResponse
	for ev := range ch {
		require.NotEqual(t, types.ResponseTypeError, ev.ResponseType, ev.Content)
		if ev.ResponseType == types.ResponseTypeAnswer {
			answer += ev.Content
		}
		final = ev
	}
	require.Equal(t, refusal, answer, "done events must not duplicate refusal deltas")
	require.True(t, final.Done)
	require.Equal(t, "stop", final.FinishReason)
	require.Empty(t, final.ToolCalls)
	require.NotNil(t, final.Usage)
	require.Equal(t, 10, final.Usage.TotalTokens)
}
