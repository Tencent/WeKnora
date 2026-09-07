package chat

import (
	"encoding/json"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/provider"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestChatOptionsCompletionBudget(t *testing.T) {
	assert.Zero(t, (*ChatOptions)(nil).CompletionBudget())
	assert.Zero(t, (&ChatOptions{}).CompletionBudget())
	assert.Equal(t, 128, (&ChatOptions{MaxTokens: 128}).CompletionBudget())
	assert.Equal(t, 256, (&ChatOptions{MaxCompletionTokens: 256}).CompletionBudget())
	assert.Equal(t, 256, (&ChatOptions{MaxTokens: 128, MaxCompletionTokens: 256}).CompletionBudget())
}

func TestWireCompletionTokenField(t *testing.T) {
	assert.Equal(t, completionTokenFieldMaxCompletionTokens,
		wireCompletionTokenField(provider.ProviderOpenAI, "gpt-4o"))
	assert.Equal(t, completionTokenFieldMaxCompletionTokens,
		wireCompletionTokenField(provider.ProviderVolcengine, "doubao-seed-2-0-mini"))
	assert.Equal(t, completionTokenFieldMaxTokens,
		wireCompletionTokenField(provider.ProviderDeepSeek, "deepseek-chat"))
	assert.Equal(t, completionTokenFieldMaxTokens,
		wireCompletionTokenField(provider.ProviderGeneric, "qwen3"))
	assert.Equal(t, completionTokenFieldMaxTokens,
		wireCompletionTokenField(provider.ProviderNvidia, "llama-3"))
	// GPT-5 / o-series always use the modern field, even on a max_tokens provider.
	assert.Equal(t, completionTokenFieldMaxCompletionTokens,
		wireCompletionTokenField(provider.ProviderGeneric, "gpt-5-mini"))
}

func TestBuildChatCompletionRequest_OneWireTokenField(t *testing.T) {
	messages := []Message{{Role: "user", Content: "hello"}}

	t.Run("volcengine both aliases send only max_completion_tokens", func(t *testing.T) {
		c := newOutboundChat(t, string(provider.ProviderVolcengine), "doubao-seed-2-0-mini", nil)
		req := c.BuildChatCompletionRequest(messages, &ChatOptions{
			MaxTokens: 2048, MaxCompletionTokens: 4096,
		}, false)
		assert.Equal(t, 4096, req.MaxCompletionTokens)
		assert.Zero(t, req.MaxTokens)

		body, err := json.Marshal(req)
		require.NoError(t, err)
		assert.Contains(t, string(body), `"max_completion_tokens":4096`)
		assert.NotContains(t, string(body), `"max_tokens"`)
	})

	t.Run("deepseek sends max_tokens", func(t *testing.T) {
		c := newOutboundChat(t, string(provider.ProviderDeepSeek), "deepseek-chat", nil)
		req := c.BuildChatCompletionRequest(messages, &ChatOptions{
			MaxTokens: 2048, MaxCompletionTokens: 4096,
		}, false)
		c.adapter.ShapeRequest(&req, &ChatOptions{MaxTokens: 2048, MaxCompletionTokens: 4096}, false)
		assert.Equal(t, 4096, req.MaxTokens)
		assert.Zero(t, req.MaxCompletionTokens)

		body, err := json.Marshal(req)
		require.NoError(t, err)
		assert.Contains(t, string(body), `"max_tokens":4096`)
		assert.NotContains(t, string(body), "max_completion_tokens")
	})

	t.Run("generic vLLM sends max_tokens from MaxTokens-only callers", func(t *testing.T) {
		c := newOutboundChat(t, string(provider.ProviderGeneric), "qwen3", nil)
		req := c.BuildChatCompletionRequest(messages, &ChatOptions{MaxTokens: 2048}, false)
		assert.Equal(t, 2048, req.MaxTokens)
		assert.Zero(t, req.MaxCompletionTokens)
	})

	t.Run("openai gpt-4o sends max_completion_tokens", func(t *testing.T) {
		c := newOutboundChat(t, string(provider.ProviderOpenAI), "gpt-4o", nil)
		req := c.BuildChatCompletionRequest(messages, &ChatOptions{MaxTokens: 128}, false)
		assert.Zero(t, req.MaxTokens)
		assert.Equal(t, 128, req.MaxCompletionTokens)
	})
}

func TestOllamaBuildChatRequestUsesCompletionBudget(t *testing.T) {
	c := &OllamaChat{modelName: "llama3"}
	req := c.buildChatRequest(
		[]Message{{Role: "user", Content: "hi"}},
		&ChatOptions{MaxCompletionTokens: 512},
		false,
	)
	assert.Equal(t, 512, req.Options["num_predict"])
}

func TestBuildLangfuseModelParamsUsesCompletionBudget(t *testing.T) {
	params := buildLangfuseModelParams(&ChatOptions{MaxTokens: 128, MaxCompletionTokens: 256})
	assert.Equal(t, 256, params["max_completion_tokens"])
	_, hasLegacy := params["max_tokens"]
	assert.False(t, hasLegacy)
}
