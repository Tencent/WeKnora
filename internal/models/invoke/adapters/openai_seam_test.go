package adapters

// openai_seam_test.go — P1c seam ③④ consumption-side unit tests: the
// adapter honors the entry-folded Endpoint overrides and backfills native
// prompt-cache counters. The 25+7 golden reconciliation gate stays in
// golden_*_reconcile_test.go.

import (
	"net/http"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/invoke"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestThinkingControlOverride(t *testing.T) {
	// "" → no override; the per-vendor/model default in thinkingFor applies.
	assert.Nil(t, thinkingControlOverride(""))
	// Known tokens map to their strategies (non-nil).
	for _, token := range []string{"none", "enable_thinking", "thinking_type", "chat_template_kwargs"} {
		assert.NotNil(t, thinkingControlOverride(token), "token=%q", token)
	}
	// Unknown non-empty falls back to chat_template_kwargs (never an error) —
	// the same strategy the known token resolves to.
	assert.Equal(t,
		thinkingControlOverride("chat_template_kwargs") != nil,
		thinkingControlOverride("mystery") != nil)
}

func TestAzureAPIVersionOverride(t *testing.T) {
	a := &openaiAdapter{name: invoke.ProviderAzureOpenAI, spec: openaiVendorSpec{azure: true}}

	// Default when the entry folded no override.
	assert.Contains(t,
		a.requestURL(invoke.Endpoint{BaseURL: "https://x.openai.azure.com"}, "gpt-4o"),
		"api-version=2023-05-15")
	// ExtraConfig["api_version"] folded through Endpoint.APIVersion.
	assert.Contains(t,
		a.requestURL(invoke.Endpoint{BaseURL: "https://x.openai.azure.com", APIVersion: "2024-10-21"}, "gpt-4o"),
		"api-version=2024-10-21")
}

func newVolcengineAdapterForTest() *openaiAdapter {
	return &openaiAdapter{name: invoke.ProviderVolcengine, spec: specFor(invoke.ProviderVolcengine)}
}

// TestThinkingControlNoneSuppressesWireField: "none" (noThinking) must send
// no thinking-related field even where the provider default would.
func TestThinkingControlNoneSuppressesWireField(t *testing.T) {
	a := newVolcengineAdapterForTest()
	ep := invoke.Endpoint{BaseURL: "http://x", ThinkingControl: "none"}
	opts := &invoke.ChatOptions{Messages: thinkingTestMessages(), Thinking: new(true)}
	req, err := a.BuildChatRequest(ep, "doubao-pro", opts)
	require.NoError(t, err)
	assert.NotContains(t, string(req.Body), `"thinking"`)
}

// TestThinkingControlOverrideForcesThinkingType: the override wins over the
// per-vendor default and produces the {"thinking":{"type":...}} wrapper.
func TestThinkingControlOverrideForcesThinkingType(t *testing.T) {
	a := newVolcengineAdapterForTest()
	ep := invoke.Endpoint{BaseURL: "http://x", ThinkingControl: "thinking_type"}
	opts := &invoke.ChatOptions{Messages: thinkingTestMessages(), Thinking: new(true)}
	req, err := a.BuildChatRequest(ep, "doubao-pro", opts)
	require.NoError(t, err)
	assert.Contains(t, string(req.Body), `"thinking":{"type":"enabled"}`)
}

// TestParseChatResponseBackfillsCacheUsage (seam ③): deepseek hit/miss
// counters land in the Usage cache detail fields.
func TestParseChatResponseBackfillsCacheUsage(t *testing.T) {
	a := &openaiAdapter{name: invoke.ProviderDeepSeek, spec: openaiVendorSpec{forceRaw: true, shape: shapeDeepSeek}}
	body := []byte(`{"choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],
		"usage":{"prompt_tokens":4096,"completion_tokens":10,"total_tokens":4106,
		"prompt_cache_hit_tokens":3072,"prompt_cache_miss_tokens":1024}}`)
	resp, err := a.ParseChatResponse(http.StatusOK, http.Header{}, body)
	require.NoError(t, err)
	assert.Equal(t, 3072, resp.Usage.CacheReadTokens)
	assert.Equal(t, 1024, resp.Usage.CacheMissTokens)
	assert.True(t, resp.Usage.CacheReported)
	assert.Equal(t, 4096, resp.Usage.PromptTokens) // totals untouched
}

func thinkingTestMessages() []invoke.Message {
	return []invoke.Message{{Role: "user", Content: []invoke.Part{{Text: "hi"}}}}
}
