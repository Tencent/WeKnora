package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestX04ExplicitZeroAndAbsentUsage(t *testing.T) {
	t.Setenv("SSRF_WHITELIST", "127.0.0.1")
	for _, reported := range []bool{true, false} {
		t.Run(fmt.Sprint(reported), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				usage := ""
				if reported {
					usage = `,"usage":{"prompt_tokens":0,"completion_tokens":0,"total_tokens":0}`
				}
				if _, err := fmt.Fprintf(w, "{\"choices\":[{\"message\":{\"role\":\"assistant\",\"content\":\"ok\"},"+
					"\"finish_reason\":\"stop\"}]%s}", usage); err != nil {
					t.Error(err)
				}
			}))
			defer server.Close()
			c, err := NewRemoteAPIChat(&ChatConfig{BaseURL: server.URL, Provider: "openai", ModelName: "fixture"})
			require.NoError(t, err)
			resp, err := c.Chat(context.Background(), []Message{{Role: "user", Content: "test"}}, nil)
			require.NoError(t, err)
			require.Equal(t, reported, resp.Usage.UsageReported)
		})
	}
}

func TestX04BudgetIsSentToProvider(t *testing.T) {
	t.Setenv("SSRF_WHITELIST", "127.0.0.1")
	var received map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
			t.Error(err)
			return
		}
		if _, err := fmt.Fprint(w, "{\"choices\":[{\"message\":{\"role\":\"assistant\",\"content\":\"ok\"},"+
			"\"finish_reason\":\"stop\"}]}"); err != nil {
			t.Error(err)
		}
	}))
	defer server.Close()
	c, err := NewChat(
		&ChatConfig{
			Source:          types.ModelSourceRemote,
			BaseURL:         server.URL,
			Provider:        "openai",
			ModelName:       "fixture",
			ContextWindow:   4096,
			MaxOutputTokens: 300,
		},
		nil,
	)
	require.NoError(t, err)
	options := &ChatOptions{MaxTokens: 4096, MaxCompletionTokens: 5000}
	_, err = c.Chat(context.Background(), []Message{{Role: "user", Content: "test"}}, options)
	require.NoError(t, err)
	for _, key := range []string{"max_tokens", "max_completion_tokens"} {
		if value, ok := received[key]; ok {
			require.LessOrEqual(t, value.(float64), float64(300))
		}
	}
	require.Equal(t, 4096, options.MaxTokens)
}

func TestX04RequestIdentityIncludesCachePolicy(t *testing.T) {
	ctx := context.Background()
	messages := []Message{{Role: "user", Content: "same"}}
	key := func(fingerprint string, o *ChatOptions) string {
		return EffectiveRequestKey(ctx, 7, "same", fingerprint, messages, o)
	}
	short := key("v1", &ChatOptions{CacheRetention: CacheRetentionShort, PromptCacheKey: "route"})
	require.NotEqual(t, short, key("v1", &ChatOptions{CacheRetention: CacheRetentionLong, PromptCacheKey: "route"}))
	require.NotEqual(t, short, key("v2", &ChatOptions{CacheRetention: CacheRetentionShort, PromptCacheKey: "route"}))
	prefix := strings.Repeat("r", 80)
	require.NotEqual(t, clampPromptCacheKey(prefix+"a"), clampPromptCacheKey(prefix+"b"))
}

func TestX04AnthropicFailurePreservesUsage(t *testing.T) {
	for _, suffix := range []string{
		"data: {broken}\n\n",
		"data: {\"type\":\"error\",\"error\":{\"message\":\"fixture error\"}}\n\n",
		"",
	} {
		stream := make(chan types.StreamResponse)
		body := "data: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":9," +
			"\"output_tokens\":1}}}\n\n" + suffix
		go processAnthropicStream(
			context.Background(),
			"fixture",
			&http.Response{
				Body: io.NopCloser(
					strings.NewReader(
						body,
					),
				),
			},
			stream,
		)
		var terminal types.StreamResponse
		for r := range stream {
			terminal = r
		}
		require.Equal(t, types.ResponseTypeError, terminal.ResponseType)
		require.NotNil(t, terminal.Usage)
		require.Equal(t, 10, terminal.Usage.TotalTokens)
	}
}
