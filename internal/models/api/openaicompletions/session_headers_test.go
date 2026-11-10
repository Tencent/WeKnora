package openaicompletions

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/api"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConversationHeadersAcrossTurnsAndToolResults(t *testing.T) {
	t.Setenv("SSRF_WHITELIST", "127.0.0.1")
	for _, cacheKeyEnabled := range []bool{false, true} {
		name := "without-cache-key"
		if cacheKeyEnabled {
			name = "with-cache-key"
		}
		t.Run(name, func(t *testing.T) {
			var headers []http.Header
			var requests []map[string]any
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				headers = append(headers, r.Header.Clone())
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				requests = append(requests, body)
				if body["stream"] == true {
					w.Header().Set("Content-Type", "text/event-stream")
					_, _ = w.Write([]byte("data: " +
						`{"choices":[{"index":0,"delta":{"content":"ok"},"finish_reason":"stop"}]}` +
						"\n\ndata: [DONE]\n\n"))
					return
				}
				_, _ = w.Write([]byte(
					`{"choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`,
				))
			}))
			defer server.Close()
			settings := api.DefaultOpenAICompletions()
			settings.PromptCacheKey = cacheKeyEnabled
			client := New(Config{Endpoint: api.Endpoint{BaseURL: server.URL, Model: "m"}, Settings: settings})
			opts := &api.Options{PromptCacheKey: "shared-cache-prefix"}
			messages := []api.Message{{Role: "user", Content: "First turn"}}
			ctx := types.WithSessionID(context.Background(), "business-session")
			_, err := client.Chat(ctx, messages, opts)
			require.NoError(t, err)

			messages = append(messages,
				api.Message{Role: "assistant", Content: "ok"},
				api.Message{Role: "user", Content: "Second turn"},
			)
			// A new HTTP request context still carries the same business session.
			ctx = types.WithSessionID(context.Background(), "business-session")
			stream, err := client.ChatStream(ctx, messages, opts)
			require.NoError(t, err)
			var streamedContent string
			for chunk := range stream {
				streamedContent += chunk.Content
			}
			assert.Equal(t, "ok", streamedContent)

			messages = append(messages,
				api.Message{Role: "assistant", ToolCalls: []api.ToolCall{{
					ID: "call-1", Type: "function", Function: api.FunctionCall{Name: "search", Arguments: `{}`},
				}}},
				api.Message{Role: "tool", ToolCallID: "call-1", Content: "Search result"},
			)
			_, err = client.Chat(ctx, messages, opts)
			require.NoError(t, err)
			require.Len(t, headers, 3)
			for i, h := range headers {
				assert.Equal(t, "business-session", h.Get("X-Conversation-ID"))
				assert.Equal(t, "WeKnora", h.Get("User-Agent"))
				if cacheKeyEnabled {
					assert.Equal(t, "business-session", h.Get("x-session-affinity"))
					assert.Equal(t, "shared-cache-prefix", requests[i]["prompt_cache_key"])
				}
			}
			lastMessages := requests[2]["messages"].([]any)
			assert.Equal(t, "tool", lastMessages[len(lastMessages)-1].(map[string]any)["role"])
			assert.Equal(t, "shared-cache-prefix", opts.PromptCacheKey)
		})
	}
}
