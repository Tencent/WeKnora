package chat

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/provider"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestBuildOutbound_ReasoningEffort pins the OpenAI thinking-level passthrough
// (design §4.3): the resolved level lands on reasoning_effort for o-series /
// GPT-5 models only, after the full chain (call > model > provider default)
// has been folded by resolveThinkingLevelOpts.
func TestBuildOutbound_ReasoningEffort(t *testing.T) {
	msgs := []Message{{Role: "user", Content: "hi"}}

	t.Run("call-level level reaches reasoning_effort", func(t *testing.T) {
		c := newOutboundChat(t, string(provider.ProviderOpenAI), "o3-mini", nil)
		body, _, _, err := c.buildOutbound(context.Background(), msgs, &ChatOptions{
			Thinking:      ptrBool(true),
			ThinkingLevel: "high",
		}, false)
		require.NoError(t, err)
		js := mustJSON(t, body)
		assert.Contains(t, js, `"reasoning_effort":"high"`)
	})

	t.Run("model-level level used when call level empty", func(t *testing.T) {
		c := newOutboundChat(t, string(provider.ProviderOpenAI), "gpt-5", nil)
		c.modelThinkingLevel = "low"
		body, _, _, err := c.buildOutbound(context.Background(), msgs, &ChatOptions{
			Thinking: ptrBool(true),
		}, false)
		require.NoError(t, err)
		js := mustJSON(t, body)
		assert.Contains(t, js, `"reasoning_effort":"low"`)
	})

	t.Run("provider default used when call and model empty", func(t *testing.T) {
		c := newOutboundChat(t, string(provider.ProviderOpenAI), "o3-mini", nil)
		// OpenAI provider caps: DefaultLevel = medium.
		body, _, _, err := c.buildOutbound(context.Background(), msgs, &ChatOptions{
			Thinking: ptrBool(true),
		}, false)
		require.NoError(t, err)
		js := mustJSON(t, body)
		assert.Contains(t, js, `"reasoning_effort":"medium"`)
	})

	t.Run("level outside selected levels falls back to provider default", func(t *testing.T) {
		c := newOutboundChat(t, string(provider.ProviderOpenAI), "o3-mini", nil)
		c.selectedLevels = []string{"low", "high"} // medium excluded
		body, _, _, err := c.buildOutbound(context.Background(), msgs, &ChatOptions{
			Thinking:      ptrBool(true),
			ThinkingLevel: "medium", // not in selected
		}, false)
		require.NoError(t, err)
		js := mustJSON(t, body)
		// medium rejected; provider default is also medium → also rejected →
		// no level emitted at all.
		assert.NotContains(t, js, "reasoning_effort")
	})

	t.Run("thinking off suppresses level even when set", func(t *testing.T) {
		c := newOutboundChat(t, string(provider.ProviderOpenAI), "o3-mini", nil)
		body, _, _, err := c.buildOutbound(context.Background(), msgs, &ChatOptions{
			Thinking:      ptrBool(false),
			ThinkingLevel: "high",
		}, false)
		require.NoError(t, err)
		js := mustJSON(t, body)
		assert.NotContains(t, js, "reasoning_effort")
	})

	t.Run("non-reasoning OpenAI model gets no reasoning_effort", func(t *testing.T) {
		c := newOutboundChat(t, string(provider.ProviderOpenAI), "gpt-4o", nil)
		body, _, _, err := c.buildOutbound(context.Background(), msgs, &ChatOptions{
			Thinking:      ptrBool(true),
			ThinkingLevel: "high",
		}, false)
		require.NoError(t, err)
		js := mustJSON(t, body)
		assert.NotContains(t, js, "reasoning_effort")
	})

	t.Run("azure reasoning model also passes level through", func(t *testing.T) {
		c := newOutboundChat(t, string(provider.ProviderAzureOpenAI), "o4-mini", nil)
		body, _, _, err := c.buildOutbound(context.Background(), msgs, &ChatOptions{
			Thinking:      ptrBool(true),
			ThinkingLevel: "low",
		}, false)
		require.NoError(t, err)
		js := mustJSON(t, body)
		assert.Contains(t, js, `"reasoning_effort":"low"`)
	})
}
