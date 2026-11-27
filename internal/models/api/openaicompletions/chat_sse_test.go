package openaicompletions

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/api"
	"github.com/stretchr/testify/require"
)

func TestChatHandlesServerSentEvents(t *testing.T) {
	t.Setenv("SSRF_WHITELIST", "127.0.0.1")

	const jsonBody = `{
		"choices":[{"message":{"content":"{\"summary\":\"A short summary\"}","reasoning_content":"Read the document."},
			"finish_reason":"stop"}],
		"usage":{"prompt_tokens":3,"completion_tokens":5,"total_tokens":8}
	}`
	const sseBody = `data: {"choices":[{"delta":{"role":"assistant","reasoning_content":"Read the "}}]}

data: {"choices":[{"delta":{"reasoning_content":"document.","content":"{\"summary\":\"A short "}}]}

data: {"choices":[{"delta":{"content":"summary\"}"},"finish_reason":"stop"}]}

data: {"choices":[],"usage":{"prompt_tokens":3,"completion_tokens":5,"total_tokens":8}}

`

	for _, tc := range []struct {
		name, contentType, body, wantError string
	}{
		{"JSON", "application/json", jsonBody, ""},
		{"SSE", "Text/Event-Stream; charset=utf-8", sseBody + "data: [DONE]\n\n", ""},
		{"SSE ending at EOF", "text/event-stream", sseBody, ""},
		{"malformed chunk", "text/event-stream", sseBody + "data: {broken}\n\n", "decode stream chunk:"},
		{
			"provider error", "text/event-stream",
			sseBody + "data: {\"error\":{\"message\":\"upstream failed\"}}\n\n",
			"API stream error: upstream failed",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				var request map[string]json.RawMessage
				if err := json.NewDecoder(req.Body).Decode(&request); err != nil {
					t.Errorf("decode request: %v", err)
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				if _, ok := request["stream"]; ok {
					t.Error("non-streaming request unexpectedly set stream")
				}
				w.Header().Set("Content-Type", tc.contentType)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()
			c := newClient(t, func(cfg *Config) {
				cfg.Endpoint.BaseURL = srv.URL
				cfg.Endpoint.Client = srv.Client()
				cfg.Settings.ReasoningFields = []string{"reasoning_content"}
			})

			messages := []api.Message{{Role: "user", Content: "Summarize this document as JSON."}}
			resp, err := c.Chat(context.Background(), messages, nil)
			if tc.wantError != "" {
				require.ErrorContains(t, err, tc.wantError)
				require.Nil(t, resp)
				return
			}
			require.NoError(t, err)
			require.Equal(t, `{"summary":"A short summary"}`, resp.Content)
			require.Equal(t, "Read the document.", resp.ReasoningContent)
			require.Equal(t, "stop", resp.FinishReason)
			require.Empty(t, resp.ToolCalls)
			require.Equal(t, 3, resp.Usage.PromptTokens)
			require.Equal(t, 5, resp.Usage.CompletionTokens)
			require.Equal(t, 8, resp.Usage.TotalTokens)
		})
	}
}
