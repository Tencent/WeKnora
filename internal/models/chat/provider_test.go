package chat

import (
	"encoding/json"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/provider"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/sashabaranov/go-openai"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestResolveProvider pins the provider+model routing table, including the
// sub-model matchers (reasoning models, Qwen thinking, LKEAP DeepSeek V3) and
// the baseProvider fallback for everything else.
func TestResolveProvider(t *testing.T) {
	cases := []struct {
		name  string
		prov  provider.ProviderName
		model string
		want  providerAdapter
	}{
		{"deepseek", provider.ProviderDeepSeek, "deepseek-chat", deepseekProvider{}},
		{"lkeap v3", provider.ProviderLKEAP, "deepseek-v3.1", lkeapProvider{}},
		{"lkeap r1 falls back", provider.ProviderLKEAP, "deepseek-r1", baseProvider{}},
		{"qwen thinking", provider.ProviderAliyun, "qwen3-32b", qwenThinkingProvider{}},
		{"generic", provider.ProviderGeneric, "anything", genericProvider{}},
		{"gemini", provider.ProviderGemini, "gemini-3-flash-preview", geminiProvider{}},
		{"nvidia", provider.ProviderNvidia, "anything", nvidiaProvider{}},
		{"volcengine", provider.ProviderVolcengine, "doubao", volcengineProvider{}},
		{"openai non-reasoning falls back", provider.ProviderOpenAI, "gpt-4o", baseProvider{}},
		{"openai reasoning", provider.ProviderOpenAI, "gpt-5", openAIReasoningProvider{}},
		{"azure non-reasoning", provider.ProviderAzureOpenAI, "gpt-4", azureProvider{}},
		{"azure reasoning", provider.ProviderAzureOpenAI, "gpt-5-mini", azureReasoningProvider{}},
		{"moonshot fixed temp", provider.ProviderMoonshot, "moonshot-v1-8k", moonshotProvider{}},
		{"moonshot other falls back", provider.ProviderMoonshot, "kimi-latest", baseProvider{}},
		{"weknora cloud", provider.ProviderWeKnoraCloud, "anything", weKnoraCloudProvider{}},
		{"unknown falls back", provider.ProviderName("nope"), "x", baseProvider{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.IsType(t, tc.want, resolveProvider(tc.prov, tc.model))
		})
	}
}

func newOutboundChat(t *testing.T, providerName, model string, extra map[string]string) *RemoteAPIChat {
	t.Helper()
	c, err := NewRemoteAPIChat(&ChatConfig{
		Source:      types.ModelSourceRemote,
		ModelName:   model,
		APIKey:      "k",
		ModelID:     model,
		Provider:    providerName,
		ExtraConfig: extra,
	})
	require.NoError(t, err)
	return c
}

// TestBuildOutbound_Thinking is the characterization suite for the merged
// thinking-control path: it asserts that buildOutbound produces the same wire
// formats the pre-refactor provider customizers did.
func TestBuildOutbound_Thinking(t *testing.T) {
	msgs := []Message{{Role: "user", Content: "hi"}}

	t.Run("generic explicit thinking_type overrides legacy kwargs", func(t *testing.T) {
		c := newOutboundChat(t, string(provider.ProviderGeneric), "deepseek-v4-flash",
			map[string]string{ExtraConfigThinkingControl: "thinking_type"})
		body, _, useRaw, err := c.buildOutbound(msgs, &ChatOptions{Thinking: ptrBool(false)}, true)
		require.NoError(t, err)
		require.True(t, useRaw)
		js := mustJSON(t, body)
		assert.Contains(t, js, `"thinking"`)
		assert.Contains(t, js, `"disabled"`)
		assert.NotContains(t, js, "chat_template_kwargs")
	})

	t.Run("generic legacy chat_template_kwargs", func(t *testing.T) {
		c := newOutboundChat(t, string(provider.ProviderGeneric), "qwen", nil)
		body, _, useRaw, err := c.buildOutbound(msgs, &ChatOptions{Thinking: ptrBool(false)}, true)
		require.NoError(t, err)
		require.True(t, useRaw)
		assert.Contains(t, mustJSON(t, body), "chat_template_kwargs")
	})

	t.Run("none keeps the standard SDK request", func(t *testing.T) {
		c := newOutboundChat(t, string(provider.ProviderGeneric), "x",
			map[string]string{ExtraConfigThinkingControl: "none"})
		body, _, useRaw, err := c.buildOutbound(msgs, &ChatOptions{Thinking: ptrBool(false)}, true)
		require.NoError(t, err)
		assert.False(t, useRaw)
		_, ok := body.(*openai.ChatCompletionRequest)
		assert.True(t, ok)
	})

	t.Run("qwen non-stream forces disabled", func(t *testing.T) {
		c := newOutboundChat(t, string(provider.ProviderAliyun), "qwen3-32b", nil)
		body, _, useRaw, err := c.buildOutbound(msgs, &ChatOptions{Thinking: ptrBool(true)}, false)
		require.NoError(t, err)
		require.True(t, useRaw)
		assert.Contains(t, mustJSON(t, body), `"enable_thinking":false`)
	})

	t.Run("qwen stream honors requested true", func(t *testing.T) {
		c := newOutboundChat(t, string(provider.ProviderAliyun), "qwen3-32b", nil)
		body, _, _, err := c.buildOutbound(msgs, &ChatOptions{Thinking: ptrBool(true)}, true)
		require.NoError(t, err)
		assert.Contains(t, mustJSON(t, body), `"enable_thinking":true`)
	})

	t.Run("volcengine thinking enabled", func(t *testing.T) {
		c := newOutboundChat(t, string(provider.ProviderVolcengine), "doubao", nil)
		body, _, useRaw, err := c.buildOutbound(msgs, &ChatOptions{Thinking: ptrBool(true)}, true)
		require.NoError(t, err)
		require.True(t, useRaw)
		js := mustJSON(t, body)
		assert.Contains(t, js, `"thinking"`)
		assert.Contains(t, js, `"enabled"`)
	})

	t.Run("lkeap deepseek-v3 emits thinking type", func(t *testing.T) {
		c := newOutboundChat(t, string(provider.ProviderLKEAP), "deepseek-v3.1", nil)
		body, _, useRaw, err := c.buildOutbound(msgs, &ChatOptions{Thinking: ptrBool(false)}, true)
		require.NoError(t, err)
		require.True(t, useRaw)
		assert.Contains(t, mustJSON(t, body), `"thinking"`)
	})

	t.Run("lkeap r1 left untouched", func(t *testing.T) {
		c := newOutboundChat(t, string(provider.ProviderLKEAP), "deepseek-r1", nil)
		body, _, useRaw, err := c.buildOutbound(msgs, &ChatOptions{Thinking: ptrBool(false)}, true)
		require.NoError(t, err)
		assert.False(t, useRaw)
		_, ok := body.(*openai.ChatCompletionRequest)
		assert.True(t, ok)
	})
}

// TestBuildOutbound_ShapeRequest covers the param-shaping providers that used
// to live inline in BuildChatCompletionRequest.
func TestBuildOutbound_ShapeRequest(t *testing.T) {
	msgs := []Message{{Role: "user", Content: "hi"}}

	t.Run("deepseek strips tool_choice", func(t *testing.T) {
		c := newOutboundChat(t, string(provider.ProviderDeepSeek), "deepseek-chat", nil)
		body, _, useRaw, err := c.buildOutbound(msgs, &ChatOptions{ToolChoice: "auto"}, false)
		require.NoError(t, err)
		assert.False(t, useRaw)
		req := body.(*openai.ChatCompletionRequest)
		assert.Nil(t, req.ToolChoice)
	})

	t.Run("moonshot pins temperature to 1", func(t *testing.T) {
		c := newOutboundChat(t, string(provider.ProviderMoonshot), "moonshot-v1-8k", nil)
		body, _, _, err := c.buildOutbound(msgs, &ChatOptions{Temperature: 0.7, TopP: 0.9}, false)
		require.NoError(t, err)
		req := body.(*openai.ChatCompletionRequest)
		assert.EqualValues(t, 1, req.Temperature)
		assert.EqualValues(t, 0, req.TopP)
	})
}

func TestBuildOutbound_GeminiProviderMetadata(t *testing.T) {
	c := newOutboundChat(t, string(provider.ProviderGemini), "gemini-3-flash-preview", nil)
	messages := []Message{
		{Role: "user", Content: "find docs"},
		{
			Role: "assistant",
			ToolCalls: []ToolCall{{
				ID:               "call_1",
				Type:             "function",
				ProviderMetadata: types.ToolCallMetadata{"google": json.RawMessage(`{"thought_signature":"gemini-signature"}`)},
				Function: FunctionCall{
					Name:      "wiki_search",
					Arguments: `{"query":"MACS"}`,
				},
			}},
		},
	}

	body, _, useRaw, err := c.buildOutbound(messages, &ChatOptions{}, false)
	require.NoError(t, err)
	require.True(t, useRaw)

	js := mustJSON(t, body)
	assert.Contains(t, js, `"extra_content"`)
	assert.Contains(t, js, `"thought_signature":"gemini-signature"`)
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	require.NoError(t, err)
	return string(b)
}
