package chat

import (
	"encoding/json"
	"testing"

	"github.com/sashabaranov/go-openai"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func ptrBool(b bool) *bool { return &b }

// TestThinkingStrategy_NilThinking verifies the strategies that defer to the
// model default emit nothing when ChatOptions.Thinking is unset.
func TestThinkingStrategy_NilThinking(t *testing.T) {
	req := openai.ChatCompletionRequest{Model: "test"}
	strategies := []ThinkingStrategy{
		noThinking{},
		enableThinking{}, // not alwaysSend
		thinkingTypeField{},
		chatTemplateKwargs{},
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

func TestReasoningEffort(t *testing.T) {
	s := reasoningEffort()
	req := openai.ChatCompletionRequest{Model: "hy4-preview"}

	custom, raw := s.Apply(&req, &ChatOptions{ThinkingEffort: "no_think"}, true)
	require.True(t, raw)
	out := custom.(*openai.ChatCompletionRequest)
	assert.Equal(t, "no_think", out.ChatTemplateKwargs["reasoning_effort"])

	custom, raw = s.Apply(&req, &ChatOptions{ThinkingEffort: "low", Thinking: ptrBool(false)}, true)
	require.True(t, raw)
	assert.Equal(t, "low", custom.(*openai.ChatCompletionRequest).ChatTemplateKwargs["reasoning_effort"])

	custom, raw = s.Apply(&req, &ChatOptions{Thinking: ptrBool(true)}, true)
	require.True(t, raw)
	assert.Equal(t, "high", custom.(*openai.ChatCompletionRequest).ChatTemplateKwargs["reasoning_effort"])

	custom, raw = s.Apply(&req, &ChatOptions{Thinking: ptrBool(false)}, true)
	require.True(t, raw)
	assert.Equal(t, "no_think", custom.(*openai.ChatCompletionRequest).ChatTemplateKwargs["reasoning_effort"])

	custom, raw = s.Apply(&req, &ChatOptions{ThinkingEffort: "medium"}, true)
	assert.Nil(t, custom)
	assert.False(t, raw)

	custom, raw = s.Apply(&req, &ChatOptions{ThinkingEffort: "max"}, true)
	require.True(t, raw)
	assert.Equal(t, "max", custom.(*openai.ChatCompletionRequest).ChatTemplateKwargs["reasoning_effort"])
}

func TestGLMReasoningEffort(t *testing.T) {
	s := glmReasoningEffort()
	req := openai.ChatCompletionRequest{Model: "glm-5.3"}

	for _, level := range []string{"low", "high", "max"} {
		custom, raw := s.Apply(&req, &ChatOptions{ThinkingEffort: level}, true)
		require.True(t, raw)
		out, ok := custom.(GLMChatCompletionRequest)
		require.True(t, ok)
		assert.Equal(t, level, out.ReasoningEffort)
		require.NotNil(t, out.Thinking)
		assert.Equal(t, "enabled", out.Thinking.Type)
	}

	// GLM-5.x cannot disable reasoning, so no_think degrades to the lightest
	// level instead of dropping out of the request.
	custom, raw := s.Apply(&req, &ChatOptions{ThinkingEffort: "no_think"}, true)
	require.True(t, raw)
	assert.Equal(t, "low", custom.(GLMChatCompletionRequest).ReasoningEffort)

	custom, raw = s.Apply(&req, &ChatOptions{Thinking: ptrBool(true)}, true)
	require.True(t, raw)
	assert.Equal(t, "high", custom.(GLMChatCompletionRequest).ReasoningEffort)

	custom, raw = s.Apply(&req, &ChatOptions{Thinking: ptrBool(false)}, true)
	require.True(t, raw)
	assert.Equal(t, "low", custom.(GLMChatCompletionRequest).ReasoningEffort)

	custom, raw = s.Apply(&req, &ChatOptions{ThinkingEffort: "medium"}, true)
	assert.Nil(t, custom)
	assert.False(t, raw)
}

func TestParseThinkingOverride(t *testing.T) {
	// Compared by thinking_control name rather than Go type: the two effort
	// selectors share the effortStrategy type and differ only by name.
	cases := map[string]string{
		"none":                 "none",
		"enable_thinking":      "enable_thinking",
		"thinking_type":        "thinking_type",
		"chat_template_kwargs": "chat_template_kwargs",
		"reasoning_effort":     "reasoning_effort",
		"glm_reasoning_effort": "glm_reasoning_effort",
		"something-unknown":    "chat_template_kwargs", // legacy default-mode fallback
	}
	for value, want := range cases {
		got := parseThinkingOverride(map[string]string{ExtraConfigThinkingControl: value})
		assert.Equal(t, want, thinkingStrategyName(got), "value=%q", value)
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
	assert.Equal(t, "reasoning_effort", EffectiveThinkingControl(&ChatConfig{
		Provider:    "hunyuan",
		ModelName:   "hy4-preview",
		ExtraConfig: map[string]string{ExtraConfigThinkingControl: "reasoning_effort"},
	}))
	assert.Equal(t, "reasoning_effort", EffectiveThinkingControl(&ChatConfig{
		Provider:    "generic",
		ModelName:   "hy4-preview",
		ExtraConfig: map[string]string{ExtraConfigThinkingControl: "reasoning_effort"},
	}))
	assert.Equal(t, "reasoning_effort", EffectiveThinkingControl(&ChatConfig{
		Provider:    "generic",
		ModelName:   "qwen3",
		ExtraConfig: map[string]string{ExtraConfigThinkingControl: "reasoning_effort"},
	}))
}
