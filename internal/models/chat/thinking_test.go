package chat

import (
	"encoding/json"
	"testing"

	"github.com/sashabaranov/go-openai"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func ptrBool(b bool) *bool { return &b }

func mustMarshal(t *testing.T, v any) []byte {
	t.Helper()
	body, err := json.Marshal(v)
	require.NoError(t, err)
	return body
}

// TestThinkingStrategy_NilThinking verifies the strategies that defer to the
// model default emit nothing when ChatOptions.Thinking is unset.
func TestThinkingStrategy_NilThinking(t *testing.T) {
	req := openai.ChatCompletionRequest{Model: "test"}
	strategies := []ThinkingStrategy{
		noThinking{},
		enableThinking{}, // not alwaysSend
		thinkingTypeField{},
		chatTemplateKwargs{},
		chatTemplateKwargs{key: "thinking"},
		reasoningEffort{},
		openRouterReasoning{},
	}
	for _, s := range strategies {
		custom, raw := s.Apply(&req, nil, true)
		assert.Nil(t, custom, "%T", s)
		assert.False(t, raw, "%T", s)
	}
}

// TestEnableThinking_QwenSemantics pins the Aliyun Qwen behavior: thinking is
// always sent, defaults to false, and is forced off on non-stream requests.
func TestEnableThinking_QwenSemantics(t *testing.T) {
	s := enableThinking{alwaysSend: true, disableOnNonStream: true}
	req := openai.ChatCompletionRequest{Model: "qwen3-32b"}

	t.Run("non-stream forces false even when requested true", func(t *testing.T) {
		custom, raw := s.Apply(&req, &ChatOptions{Thinking: ptrBool(true)}, false)
		require.True(t, raw)
		qwen, ok := custom.(QwenChatCompletionRequest)
		require.True(t, ok)
		require.NotNil(t, qwen.EnableThinking)
		assert.False(t, *qwen.EnableThinking)
	})

	t.Run("stream honors requested true", func(t *testing.T) {
		custom, raw := s.Apply(&req, &ChatOptions{Thinking: ptrBool(true)}, true)
		require.True(t, raw)
		qwen := custom.(QwenChatCompletionRequest)
		require.NotNil(t, qwen.EnableThinking)
		assert.True(t, *qwen.EnableThinking)
	})

	t.Run("stream defaults to false when unset", func(t *testing.T) {
		custom, raw := s.Apply(&req, nil, true)
		require.True(t, raw)
		qwen := custom.(QwenChatCompletionRequest)
		require.NotNil(t, qwen.EnableThinking)
		assert.False(t, *qwen.EnableThinking)
	})
}

// TestEnableThinking_ExtraConfigSemantics pins the extra_config "enable_thinking"
// override: only sent when explicitly requested.
func TestEnableThinking_ExtraConfigSemantics(t *testing.T) {
	s := enableThinking{}
	req := openai.ChatCompletionRequest{Model: "qwen3"}

	custom, raw := s.Apply(&req, &ChatOptions{Thinking: ptrBool(true)}, true)
	require.True(t, raw)
	qwen := custom.(QwenChatCompletionRequest)
	require.NotNil(t, qwen.EnableThinking)
	assert.True(t, *qwen.EnableThinking)

	custom, raw = s.Apply(&req, nil, true)
	assert.Nil(t, custom)
	assert.False(t, raw)
}

func TestThinkingTypeField(t *testing.T) {
	s := thinkingTypeField{}
	req := openai.ChatCompletionRequest{Model: "ds-v3"}

	custom, raw := s.Apply(&req, &ChatOptions{Thinking: ptrBool(false)}, true)
	require.True(t, raw)
	typed, ok := custom.(ThinkingChatCompletionRequest)
	require.True(t, ok)
	require.NotNil(t, typed.Thinking)
	assert.Equal(t, "disabled", typed.Thinking.Type)

	custom, raw = s.Apply(&req, &ChatOptions{Thinking: ptrBool(true)}, true)
	require.True(t, raw)
	assert.Equal(t, "enabled", custom.(ThinkingChatCompletionRequest).Thinking.Type)
}

func TestChatTemplateKwargs(t *testing.T) {
	s := chatTemplateKwargs{}
	req := openai.ChatCompletionRequest{Model: "vllm"}

	custom, raw := s.Apply(&req, &ChatOptions{Thinking: ptrBool(true)}, true)
	require.True(t, raw)
	out, ok := custom.(*openai.ChatCompletionRequest)
	require.True(t, ok)
	assert.Equal(t, true, out.ChatTemplateKwargs["enable_thinking"])

	body, err := json.Marshal(custom)
	require.NoError(t, err)
	assert.Contains(t, string(body), "chat_template_kwargs")
}

func TestChatTemplateKwargsThinkingKey(t *testing.T) {
	s := chatTemplateKwargs{key: "thinking"}
	req := openai.ChatCompletionRequest{Model: "nim-step"}

	custom, raw := s.Apply(&req, &ChatOptions{Thinking: ptrBool(false)}, true)
	require.True(t, raw)
	out, ok := custom.(*openai.ChatCompletionRequest)
	require.True(t, ok)
	assert.Equal(t, false, out.ChatTemplateKwargs["thinking"])
	assert.NotContains(t, out.ChatTemplateKwargs, "enable_thinking")
}

func TestReasoningEffort(t *testing.T) {
	s := reasoningEffort{enabledEffort: "high", disabledEffort: "none"}
	req := openai.ChatCompletionRequest{Model: "grok"}

	custom, raw := s.Apply(&req, &ChatOptions{Thinking: ptrBool(true)}, true)
	assert.Nil(t, custom)
	assert.False(t, raw)
	assert.Equal(t, "high", req.ReasoningEffort)

	req = openai.ChatCompletionRequest{Model: "grok"}
	custom, raw = s.Apply(&req, &ChatOptions{Thinking: ptrBool(false)}, true)
	assert.Nil(t, custom)
	assert.False(t, raw)
	assert.Equal(t, "none", req.ReasoningEffort)
}

func TestOpenRouterReasoning(t *testing.T) {
	s := openRouterReasoning{effort: "medium", maxTokens: 2048, exclude: ptrBool(true)}
	req := openai.ChatCompletionRequest{Model: "openrouter/reasoner"}

	custom, raw := s.Apply(&req, &ChatOptions{Thinking: ptrBool(true)}, true)
	require.True(t, raw)
	require.NotNil(t, custom)
	js := string(mustMarshal(t, custom))
	assert.Contains(t, js, `"reasoning"`)
	assert.Contains(t, js, `"enabled":true`)
	assert.Contains(t, js, `"effort":"medium"`)
	assert.Contains(t, js, `"max_tokens":2048`)
	assert.Contains(t, js, `"exclude":true`)

	custom, raw = s.Apply(&req, &ChatOptions{Thinking: ptrBool(false)}, true)
	require.True(t, raw)
	js = string(mustMarshal(t, custom))
	assert.Contains(t, js, `"enabled":false`)
	assert.NotContains(t, js, `"effort"`)
}

func TestParseThinkingOverride(t *testing.T) {
	cases := map[string]ThinkingStrategy{
		"none":                          noThinking{},
		"enable_thinking":               enableThinking{},
		"thinking_type":                 thinkingTypeField{},
		"chat_template_kwargs":          chatTemplateKwargs{},
		"chat_template_kwargs_thinking": chatTemplateKwargs{},
		"reasoning_effort":              reasoningEffort{},
		"openrouter_reasoning":          openRouterReasoning{},
		"something-unknown":             chatTemplateKwargs{}, // legacy default-mode fallback
	}
	for value, want := range cases {
		got := parseThinkingOverride(map[string]string{ExtraConfigThinkingControl: value})
		assert.IsType(t, want, got, "value=%q", value)
	}

	assert.Nil(t, parseThinkingOverride(nil))
	assert.Nil(t, parseThinkingOverride(map[string]string{}))
	assert.Nil(t, parseThinkingOverride(map[string]string{ExtraConfigThinkingControl: ""}))
}

func TestEffectiveThinkingControl(t *testing.T) {
	assert.Equal(t, "enable_thinking", EffectiveThinkingControl(&ChatConfig{
		Provider:  "aliyun",
		ModelName: "qwen3-32b",
	}))
	assert.Equal(t, "chat_template_kwargs", EffectiveThinkingControl(&ChatConfig{
		Provider:    "generic",
		ModelName:   "qwen3",
		ExtraConfig: map[string]string{ExtraConfigThinkingControl: "chat_template_kwargs"},
	}))
	assert.Equal(t, "none", EffectiveThinkingControl(&ChatConfig{
		Provider:    "generic",
		ModelName:   "qwen3",
		ExtraConfig: map[string]string{ExtraConfigThinkingControl: "none"},
	}))
	assert.Equal(t, "chat_template_kwargs_thinking", EffectiveThinkingControl(&ChatConfig{
		Provider:    "generic",
		ModelName:   "step-3.7-flash",
		ExtraConfig: map[string]string{ExtraConfigThinkingControl: "chat_template_kwargs_thinking"},
	}))
	assert.Equal(t, "reasoning_effort", EffectiveThinkingControl(&ChatConfig{
		Provider:    "openai",
		ModelName:   "gpt-5",
		ExtraConfig: map[string]string{ExtraConfigThinkingControl: "reasoning_effort"},
	}))
	assert.Equal(t, "openrouter_reasoning", EffectiveThinkingControl(&ChatConfig{
		Provider:    "openrouter",
		ModelName:   "openai/gpt-oss-120b",
		ExtraConfig: map[string]string{ExtraConfigThinkingControl: "openrouter_reasoning"},
	}))
}
