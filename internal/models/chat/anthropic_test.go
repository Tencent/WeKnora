package chat

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/provider"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAnthropicChat(t *testing.T) {
	t.Setenv("SSRF_WHITELIST", "127.0.0.1")

	var capturedHeaders http.Header
	var capturedRequest anthropicRequest

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/messages", r.URL.Path)
		capturedHeaders = r.Header.Clone()
		require.NoError(t, json.NewDecoder(r.Body).Decode(&capturedRequest))

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id":"msg_123",
			"type":"message",
			"role":"assistant",
			"content":[{"type":"text","text":"hello"}],
			"stop_reason":"end_turn",
			"usage":{"input_tokens":3,"output_tokens":2}
		}`))
	}))
	defer server.Close()

	chat, err := NewAnthropicChat(&ChatConfig{
		Source:    types.ModelSourceRemote,
		BaseURL:   server.URL,
		ModelName: "claude-sonnet-4-5",
		APIKey:    "test-key",
		Provider:  string(provider.ProviderAnthropic),
		CustomHeaders: map[string]string{
			"anthropic-beta": "test-beta",
		},
	})
	require.NoError(t, err)

	resp, err := chat.Chat(context.Background(), []Message{
		{Role: "system", Content: "You are helpful."},
		{Role: "user", Content: "Hi"},
	}, &ChatOptions{MaxTokens: 7, Temperature: 0.2})
	require.NoError(t, err)

	assert.Equal(t, "test-key", capturedHeaders.Get("x-api-key"))
	assert.Equal(t, anthropicVersion, capturedHeaders.Get("anthropic-version"))
	assert.Equal(t, "test-beta", capturedHeaders.Get("anthropic-beta"))
	assert.Equal(t, "claude-sonnet-4-5", capturedRequest.Model)
	assert.Equal(t, 7, capturedRequest.MaxTokens)
	assert.Equal(t, "You are helpful.", capturedRequest.System)
	require.Len(t, capturedRequest.Messages, 1)
	assert.Equal(t, "user", capturedRequest.Messages[0].Role)
	assert.Equal(t, "Hi", capturedRequest.Messages[0].Content)
	assert.Equal(t, "hello", resp.Content)
	assert.Equal(t, "end_turn", resp.FinishReason)
	assert.Equal(t, 3, resp.Usage.PromptTokens)
	assert.Equal(t, 2, resp.Usage.CompletionTokens)
	assert.Equal(t, 5, resp.Usage.TotalTokens)
}

func TestNewRemoteChat_AnthropicProvider(t *testing.T) {
	chat, err := NewRemoteChat(&ChatConfig{
		Source:    types.ModelSourceRemote,
		ModelName: "claude-sonnet-4-5",
		APIKey:    "test-key",
		Provider:  string(provider.ProviderAnthropic),
	})
	require.NoError(t, err)
	_, ok := chat.(*AnthropicChat)
	assert.True(t, ok)
}
