package chat

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestAnthropicThinking pins the extended-thinking block on the Anthropic
// Messages protocol (design §4.3 continuous-vendor收编): the resolved level
// maps to thinking.budget_tokens, max_tokens stays strictly greater than the
// budget, and off/unset thinking leaves the request unchanged (pre-thinking
// behavior preserved).
func TestAnthropicThinking(t *testing.T) {
	msgs := []Message{{Role: "user", Content: "hi"}}
	newChat := func(t *testing.T) *AnthropicChat {
		t.Helper()
		c, err := NewAnthropicChat(&ChatConfig{
			Source:    "remote",
			Provider:  "anthropic",
			ModelName: "claude-sonnet-4",
			APIKey:    "k",
		})
		require.NoError(t, err)
		return c
	}

	t.Run("thinking on with level maps to budget block", func(t *testing.T) {
		c := newChat(t)
		req := c.buildRequest(context.Background(), msgs, &ChatOptions{
			Thinking:      ptrBool(true),
			ThinkingLevel: "high",
		})
		require.NotNil(t, req.Thinking)
		assert.Equal(t, "enabled", req.Thinking.Type)
		assert.Equal(t, 16384, req.Thinking.BudgetTokens)
		assert.Greater(t, req.MaxTokens, req.Thinking.BudgetTokens, "max_tokens must exceed the thinking budget")
	})

	t.Run("level empty resolves to provider default medium", func(t *testing.T) {
		c := newChat(t)
		req := c.buildRequest(context.Background(), msgs, &ChatOptions{
			Thinking: ptrBool(true),
		})
		require.NotNil(t, req.Thinking)
		assert.Equal(t, 8192, req.Thinking.BudgetTokens)
	})

	t.Run("model-level level used when call level empty", func(t *testing.T) {
		c := newChat(t)
		c.modelThinkingLevel = "low"
		req := c.buildRequest(context.Background(), msgs, &ChatOptions{
			Thinking: ptrBool(true),
		})
		require.NotNil(t, req.Thinking)
		assert.Equal(t, 2048, req.Thinking.BudgetTokens)
	})

	t.Run("thinking off emits no block", func(t *testing.T) {
		c := newChat(t)
		req := c.buildRequest(context.Background(), msgs, &ChatOptions{
			Thinking:      ptrBool(false),
			ThinkingLevel: "high",
		})
		assert.Nil(t, req.Thinking)
	})

	t.Run("thinking nil emits no block (pre-thinking behavior)", func(t *testing.T) {
		c := newChat(t)
		req := c.buildRequest(context.Background(), msgs, nil)
		assert.Nil(t, req.Thinking)
		assert.Equal(t, 1024, req.MaxTokens)
	})

	t.Run("caller budget above thinking budget is preserved", func(t *testing.T) {
		c := newChat(t)
		req := c.buildRequest(context.Background(), msgs, &ChatOptions{
			Thinking:            ptrBool(true),
			ThinkingLevel:       "low",
			MaxCompletionTokens: 32000,
		})
		require.NotNil(t, req.Thinking)
		assert.Equal(t, 32000, req.MaxTokens, "caller's larger budget kept")
		assert.Greater(t, req.MaxTokens, req.Thinking.BudgetTokens)
	})

	t.Run("level outside selected levels falls back to provider default", func(t *testing.T) {
		c := newChat(t)
		c.selectedLevels = []string{"low", "high"} // medium excluded
		req := c.buildRequest(context.Background(), msgs, &ChatOptions{
			Thinking:      ptrBool(true),
			ThinkingLevel: "medium",
		})
		// medium rejected by the selection set; provider default is also
		// medium → also rejected → no block.
		assert.Nil(t, req.Thinking)
	})

	t.Run("wire JSON shape matches Messages API thinking block", func(t *testing.T) {
		c := newChat(t)
		req := c.buildRequest(context.Background(), msgs, &ChatOptions{
			Thinking:      ptrBool(true),
			ThinkingLevel: "high",
		})
		data, err := json.Marshal(req)
		require.NoError(t, err)
		assert.Contains(t, string(data), `"thinking":{"type":"enabled","budget_tokens":16384}`)
	})
}
