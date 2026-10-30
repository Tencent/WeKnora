package openaicompletions

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/api"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestChatPreservesRefusal(t *testing.T) {
	t.Setenv("SSRF_WHITELIST", "127.0.0.1")
	for _, tc := range []struct {
		name, message, want string
	}{
		{"refusal only", `{"content":null,"refusal":"I cannot help with that."}`, "I cannot help with that."},
		{
			"content and refusal",
			`{"content":"Explanation. ","refusal":"I cannot help."}`,
			"Explanation. I cannot help.",
		},
		{"null refusal", `{"content":"Answer","refusal":null}`, "Answer"},
		{"absent refusal", `{"content":"Answer"}`, "Answer"},
		{"empty refusal", `{"content":"Answer","refusal":""}`, "Answer"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"choices":[{"message":` + tc.message + `,"finish_reason":"stop"}],` +
					`"usage":{"prompt_tokens":3,"completion_tokens":2,"total_tokens":5}}`))
			}))
			defer srv.Close()
			c := newClient(t, func(cfg *Config) {
				cfg.Endpoint.BaseURL = srv.URL
				cfg.Endpoint.Client = srv.Client()
			})
			resp, err := c.Chat(context.Background(), []api.Message{{Role: "user", Content: "hi"}}, nil)
			require.NoError(t, err)
			require.Equal(t, tc.want, resp.Content)
			require.Equal(t, "stop", resp.FinishReason)
			require.Empty(t, resp.ToolCalls)
			require.Equal(t, 5, resp.Usage.TotalTokens)
		})
	}
}

func TestChatStreamPreservesRefusal(t *testing.T) {
	t.Setenv("SSRF_WHITELIST", "127.0.0.1")
	for _, tc := range []struct {
		name   string
		deltas []string
		want   string
	}{
		{"fragmented refusal", []string{
			`{"role":"assistant","content":null,"refusal":null}`,
			`{"refusal":"I cannot "}`, `{"refusal":"help with that."}`,
		}, "I cannot help with that."},
		{"content and refusal", []string{
			`{"content":"Explanation. ","refusal":"I cannot "}`, `{"refusal":"help."}`,
		}, "Explanation. I cannot help."},
		{"ordinary content", []string{`{"content":"Answer","refusal":null}`, `{"refusal":""}`}, "Answer"},
		{"content held by DSML filter", []string{
			`{"content":"Explanation <","refusal":"not a tool>"}`,
		}, "Explanation <not a tool>"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				for _, delta := range tc.deltas {
					_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":" + delta + "}]}\n\n"))
				}
				_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n" +
					"data: {\"choices\":[],\"usage\":{\"prompt_tokens\":3," +
					"\"completion_tokens\":2,\"total_tokens\":5}}\n\n" +
					"data: [DONE]\n\n"))
			}))
			defer srv.Close()
			c := newClient(t, func(cfg *Config) {
				cfg.Endpoint.BaseURL = srv.URL
				cfg.Endpoint.Client = srv.Client()
			})
			ch, err := c.ChatStream(context.Background(), []api.Message{{Role: "user", Content: "hi"}}, dsmlTestOptions)
			require.NoError(t, err)
			var text strings.Builder
			var last types.StreamResponse
			for chunk := range ch {
				require.NotEqual(t, types.ResponseTypeError, chunk.ResponseType, chunk.Content)
				if chunk.ResponseType == types.ResponseTypeAnswer {
					text.WriteString(chunk.Content)
				}
				if chunk.Done && chunk.ResponseType == types.ResponseTypeAnswer {
					last = chunk
				}
			}
			require.Equal(t, tc.want, text.String())
			require.True(t, last.Done)
			require.Equal(t, "stop", last.FinishReason)
			require.Empty(t, last.ToolCalls)
			require.NotNil(t, last.Usage)
			require.Equal(t, 5, last.Usage.TotalTokens)
		})
	}
}
