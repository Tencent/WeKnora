package chat

import (
	"strconv"
	"strings"

	"github.com/Tencent/WeKnora/internal/models/provider"
	"github.com/sashabaranov/go-openai"
)

// ExtraConfigThinkingControl is the model parameters.extra_config key for
// selecting how ChatOptions.Thinking is translated to provider HTTP fields.
// The accepted values mirror the strings the frontend writes (see
// ModelEditorDialog.vue): "none", "enable_thinking", "thinking_type",
// "chat_template_kwargs", "chat_template_kwargs_thinking",
// "reasoning_effort", "openrouter_reasoning".
const ExtraConfigThinkingControl = "thinking_control"

// Wire-format request bodies used by providers that express extended-thinking
// through a non-standard top-level field. They embed the standard OpenAI
// request so all other fields are marshalled unchanged.

// QwenChatCompletionRequest adds Aliyun Qwen's `enable_thinking` boolean.
type QwenChatCompletionRequest struct {
	openai.ChatCompletionRequest
	EnableThinking *bool `json:"enable_thinking,omitempty"`
}

// ThinkingConfig is the `{ "type": "enabled"|"disabled" }` block used by
// LKEAP / Volcengine style providers.
type ThinkingConfig struct {
	Type string `json:"type"`
}

// ThinkingChatCompletionRequest adds the `thinking` object for providers that
// use the `{ "thinking": { "type": ... } }` wire format.
type ThinkingChatCompletionRequest struct {
	openai.ChatCompletionRequest
	Thinking *ThinkingConfig `json:"thinking,omitempty"`
}

// ReasoningConfig is OpenRouter's top-level `reasoning` block.
type ReasoningConfig struct {
	Enabled   *bool  `json:"enabled,omitempty"`
	Effort    string `json:"effort,omitempty"`
	MaxTokens int    `json:"max_tokens,omitempty"`
	Exclude   *bool  `json:"exclude,omitempty"`
}

// ReasoningChatCompletionRequest adds a provider-specific `reasoning` object.
type ReasoningChatCompletionRequest struct {
	openai.ChatCompletionRequest
	Reasoning *ReasoningConfig `json:"reasoning,omitempty"`
}

// ThinkingStrategy encodes how ChatOptions.Thinking is mapped onto a provider's
// HTTP request. Apply returns (customBody, useRawHTTP):
//   - (nil, false) means "send the standard OpenAI request unchanged" (the
//     caller keeps using the SDK path).
//   - a non-nil customBody must be sent verbatim over raw HTTP because it
//     carries fields the OpenAI SDK would strip.
//
// When opts.Thinking is nil most strategies emit nothing, deferring to the
// model's own default; the exception is enableThinking{alwaysSend: true}
// (Aliyun Qwen), which must always pin the field.
type ThinkingStrategy interface {
	Apply(req *openai.ChatCompletionRequest, opts *ChatOptions, isStream bool) (customBody any, useRawHTTP bool)
}

// noThinking sends no thinking-related fields at all.
type noThinking struct{}

func (noThinking) Apply(*openai.ChatCompletionRequest, *ChatOptions, bool) (any, bool) {
	return nil, false
}

// enableThinking encodes thinking via Qwen's `enable_thinking` boolean.
//
//   - alwaysSend: pin the field even when opts.Thinking is nil (Aliyun Qwen
//     thinking models require it on every request; default value is false).
//   - disableOnNonStream: force enable_thinking=false for non-stream requests
//     (Qwen3 rejects thinking in non-stream mode).
type enableThinking struct {
	alwaysSend         bool
	disableOnNonStream bool
}

func (s enableThinking) Apply(req *openai.ChatCompletionRequest, opts *ChatOptions, isStream bool) (any, bool) {
	thinking := false
	switch {
	case opts != nil && opts.Thinking != nil:
		thinking = *opts.Thinking
	case !s.alwaysSend:
		return nil, false
	}
	if s.disableOnNonStream && !isStream {
		thinking = false
	}
	qwenReq := QwenChatCompletionRequest{ChatCompletionRequest: *req}
	qwenReq.EnableThinking = &thinking
	return qwenReq, true
}

// thinkingTypeField encodes thinking via the `{ "thinking": { "type": ... } }`
// object (LKEAP / Volcengine). Emits nothing when opts.Thinking is unset.
type thinkingTypeField struct{}

func (thinkingTypeField) Apply(req *openai.ChatCompletionRequest, opts *ChatOptions, _ bool) (any, bool) {
	if opts == nil || opts.Thinking == nil {
		return nil, false
	}
	r := ThinkingChatCompletionRequest{ChatCompletionRequest: *req}
	thinkingType := "disabled"
	if *opts.Thinking {
		thinkingType = "enabled"
	}
	r.Thinking = &ThinkingConfig{Type: thinkingType}
	return r, true
}

// chatTemplateKwargs encodes thinking via the standard request's
// `chat_template_kwargs.<key>` (vLLM / NVIDIA / generic local deployments).
// Emits nothing when opts.Thinking is unset.
type chatTemplateKwargs struct {
	key string
}

func (s chatTemplateKwargs) Apply(req *openai.ChatCompletionRequest, opts *ChatOptions, _ bool) (any, bool) {
	if opts == nil || opts.Thinking == nil {
		return nil, false
	}
	key := strings.TrimSpace(s.key)
	if key == "" {
		key = "enable_thinking"
	}
	req.ChatTemplateKwargs = map[string]interface{}{
		key: *opts.Thinking,
	}
	return req, true
}

// reasoningEffort encodes thinking as top-level `reasoning_effort`. This is
// useful for OpenAI-compatible providers that use effort values rather than an
// explicit boolean toggle.
type reasoningEffort struct {
	enabledEffort  string
	disabledEffort string
}

func (s reasoningEffort) Apply(req *openai.ChatCompletionRequest, opts *ChatOptions, _ bool) (any, bool) {
	if opts == nil || opts.Thinking == nil {
		return nil, false
	}
	if *opts.Thinking {
		req.ReasoningEffort = defaultString(s.enabledEffort, "medium")
	} else {
		req.ReasoningEffort = defaultString(s.disabledEffort, "none")
	}
	return nil, false
}

// openRouterReasoning encodes thinking through OpenRouter's top-level
// `reasoning` object.
type openRouterReasoning struct {
	effort    string
	maxTokens int
	exclude   *bool
}

func (s openRouterReasoning) Apply(req *openai.ChatCompletionRequest, opts *ChatOptions, _ bool) (any, bool) {
	if opts == nil || opts.Thinking == nil {
		return nil, false
	}
	enabled := *opts.Thinking
	cfg := &ReasoningConfig{Enabled: &enabled}
	if enabled {
		cfg.Effort = strings.TrimSpace(s.effort)
		cfg.MaxTokens = s.maxTokens
		cfg.Exclude = s.exclude
	}
	r := ReasoningChatCompletionRequest{ChatCompletionRequest: *req, Reasoning: cfg}
	return r, true
}

// parseThinkingOverride reads extra_config.thinking_control and returns the
// strategy it selects, or nil when unset (the provider adapter's default
// strategy then applies). An unrecognized non-empty value falls back to
// chat_template_kwargs, preserving the legacy default-mode behavior.
func parseThinkingOverride(extraConfig map[string]string) ThinkingStrategy {
	if extraConfig == nil {
		return nil
	}
	switch strings.ToLower(strings.TrimSpace(extraConfig[ExtraConfigThinkingControl])) {
	case "":
		return nil
	case "none":
		return noThinking{}
	case "enable_thinking":
		return enableThinking{}
	case "thinking_type":
		return thinkingTypeField{}
	case "chat_template_kwargs_thinking":
		return chatTemplateKwargs{key: "thinking"}
	case "reasoning_effort":
		return reasoningEffort{
			enabledEffort:  extraConfigString(extraConfig, "reasoning_effort", "medium"),
			disabledEffort: extraConfigString(extraConfig, "reasoning_effort_disabled", "none"),
		}
	case "openrouter_reasoning":
		return openRouterReasoning{
			effort:    extraConfigString(extraConfig, "reasoning_effort", ""),
			maxTokens: extraConfigInt(extraConfig, "reasoning_max_tokens"),
			exclude:   extraConfigBoolPtr(extraConfig, "reasoning_exclude"),
		}
	default:
		// "chat_template_kwargs" and any unknown non-empty value.
		return chatTemplateKwargs{}
	}
}

// EffectiveThinkingControl reports the provider field that will carry
// ChatOptions.Thinking. It intentionally shares the same adapter/override
// resolution as the real request path so diagnostics do not guess from the
// frontend selection.
func EffectiveThinkingControl(config *ChatConfig) string {
	if config == nil {
		return "none"
	}
	if override := parseThinkingOverride(config.ExtraConfig); override != nil {
		return thinkingStrategyName(override)
	}
	providerName := provider.ProviderName(config.Provider)
	if providerName == "" {
		providerName = provider.DetectProvider(config.BaseURL)
	}
	return thinkingStrategyName(resolveProvider(providerName, config.ModelName).Thinking())
}

func thinkingStrategyName(strategy ThinkingStrategy) string {
	switch s := strategy.(type) {
	case enableThinking:
		return "enable_thinking"
	case thinkingTypeField:
		return "thinking_type"
	case chatTemplateKwargs:
		if s.key == "thinking" {
			return "chat_template_kwargs_thinking"
		}
		return "chat_template_kwargs"
	case reasoningEffort:
		return "reasoning_effort"
	case openRouterReasoning:
		return "openrouter_reasoning"
	default:
		return "none"
	}
}

func defaultString(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return strings.TrimSpace(value)
}

func extraConfigString(extraConfig map[string]string, key, fallback string) string {
	if extraConfig == nil {
		return fallback
	}
	return defaultString(extraConfig[key], fallback)
}

func extraConfigInt(extraConfig map[string]string, key string) int {
	if extraConfig == nil {
		return 0
	}
	n, err := strconv.Atoi(strings.TrimSpace(extraConfig[key]))
	if err != nil || n <= 0 {
		return 0
	}
	return n
}

func extraConfigBoolPtr(extraConfig map[string]string, key string) *bool {
	if extraConfig == nil {
		return nil
	}
	raw := strings.TrimSpace(extraConfig[key])
	if raw == "" {
		return nil
	}
	b, err := strconv.ParseBool(raw)
	if err != nil {
		return nil
	}
	return &b
}
