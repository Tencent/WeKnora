package chat

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The OpenAI API treats max_tokens and max_completion_tokens as mutually
// exclusive, and providers such as Volcano Ark reject requests carrying both
// with a 400. See Tencent/WeKnora#3014.
func TestBuildChatCompletionRequest_MaxTokensMutuallyExclusive(t *testing.T) {
	chat := newTestRemoteChat(t)
	messages := []Message{{Role: "user", Content: "hello"}}

	t.Run("both set keeps only max_completion_tokens", func(t *testing.T) {
		opts := &ChatOptions{MaxTokens: 2048, MaxCompletionTokens: 4096}
		req := chat.BuildChatCompletionRequest(messages, opts, false)
		assert.Equal(t, 4096, req.MaxCompletionTokens)
		assert.Zero(t, req.MaxTokens)

		// Both the go-openai path and the raw-HTTP path serialize this struct,
		// so the wire request must carry only the modern field.
		body, err := json.Marshal(req)
		require.NoError(t, err)
		assert.Contains(t, string(body), `"max_completion_tokens":4096`)
		assert.NotContains(t, string(body), `"max_tokens"`)
	})

	t.Run("legacy max_tokens alone is preserved", func(t *testing.T) {
		opts := &ChatOptions{MaxTokens: 2048}
		req := chat.BuildChatCompletionRequest(messages, opts, false)
		assert.Equal(t, 2048, req.MaxTokens)
		assert.Zero(t, req.MaxCompletionTokens)

		body, err := json.Marshal(req)
		require.NoError(t, err)
		assert.Contains(t, string(body), `"max_tokens":2048`)
		assert.NotContains(t, string(body), "max_completion_tokens")
	})

	t.Run("max_completion_tokens alone is preserved", func(t *testing.T) {
		opts := &ChatOptions{MaxCompletionTokens: 4096}
		req := chat.BuildChatCompletionRequest(messages, opts, false)
		assert.Equal(t, 4096, req.MaxCompletionTokens)
		assert.Zero(t, req.MaxTokens)
	})

	// DeepSeek documents max_tokens only and silently ignores
	// max_completion_tokens, so its adapter migrates the budget back onto the
	// legacy field.
	t.Run("deepseek adapter migrates the budget to max_tokens", func(t *testing.T) {
		opts := &ChatOptions{MaxTokens: 2048, MaxCompletionTokens: 4096}
		req := chat.BuildChatCompletionRequest(messages, opts, false)
		deepseekProvider{}.ShapeRequest(&req, opts, false)
		assert.Equal(t, 4096, req.MaxTokens)
		assert.Zero(t, req.MaxCompletionTokens)

		body, err := json.Marshal(req)
		require.NoError(t, err)
		assert.Contains(t, string(body), `"max_tokens":4096`)
		assert.NotContains(t, string(body), "max_completion_tokens")
	})
}
