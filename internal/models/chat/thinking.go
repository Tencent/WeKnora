package chat

import (
	"strings"

	"github.com/Tencent/WeKnora/internal/models/provider"
	"github.com/sashabaranov/go-openai"
)

// ExtraConfigThinkingControl is the model parameters.extra_config key for
// selecting how ChatOptions.Thinking is translated to provider HTTP fields.
// The accepted values mirror the strings the frontend writes (see
// ModelEditorDialog.vue): "none", "enable_thinking", "thinking_type",
// "chat_template_kwargs", "reasoning_effort".
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
// `chat_template_kwargs.enable_thinking` (vLLM / NVIDIA / generic local
// deployments). Emits nothing when opts.Thinking is unset.
type chatTemplateKwargs struct{}

func (chatTemplateKwargs) Apply(req *openai.ChatCompletionRequest, opts *ChatOptions, _ bool) (any, bool) {
	if opts == nil || opts.Thinking == nil {
		return nil, false
	}
	req.ChatTemplateKwargs = map[string]interface{}{
		"enable_thinking": *opts.Thinking,
	}
	return req, true
}

// GLMChatCompletionRequest adds Zhipu GLM's top-level `reasoning_effort` and
// `thinking` fields. GLM-5.x always reasons; the effort only sizes it.
type GLMChatCompletionRequest struct {
	openai.ChatCompletionRequest
	Thinking        *ThinkingConfig `json:"thinking,omitempty"`
	ReasoningEffort string          `json:"reasoning_effort,omitempty"`
}

// effortStrategy encodes a reasoning-effort level. The instances below differ
// only in where the level goes on the wire and which levels that wire format
// accepts; validation and the legacy-boolean fallback are shared.
//
// A level outside `valid` is dropped rather than forwarded: effort-aware chat
// templates silently fall back to their own default for unknown values, which
// would make the request look like it took effect when it did not.
type effortStrategy struct {
	// name is the extra_config.thinking_control value selecting this strategy.
	name string
	// valid lists the levels this wire format accepts.
	valid []string
	// aliases rewrites a requested level before validation.
	aliases map[string]string
	// onTrue and onFalse map the legacy ChatOptions.Thinking boolean, so agents
	// configured before effort levels existed keep working.
	onTrue  string
	onFalse string
	// emit writes the resolved level onto the outbound body.
	emit func(req *openai.ChatCompletionRequest, effort string) any
}

func (s effortStrategy) Apply(req *openai.ChatCompletionRequest, opts *ChatOptions, _ bool) (any, bool) {
	if opts == nil {
		return nil, false
	}
	effort := s.normalize(opts.ThinkingEffort)
	if effort == "" && opts.Thinking != nil {
		if *opts.Thinking {
			effort = s.onTrue
		} else {
			effort = s.onFalse
		}
	}
	if effort == "" {
		return nil, false
	}
	return s.emit(req, effort), true
}

func (s effortStrategy) normalize(effort string) string {
	level := strings.ToLower(strings.TrimSpace(effort))
	if alias, ok := s.aliases[level]; ok {
		level = alias
	}
	for _, valid := range s.valid {
		if level == valid {
			return level
		}
	}
	return ""
}

// reasoningEffort carries the level in chat_template_kwargs.reasoning_effort,
// the format Hunyuan Hy3/Hy4 read.
func reasoningEffort() effortStrategy {
	return effortStrategy{
		name:    "reasoning_effort",
		valid:   []string{"no_think", "low", "high", "max"},
		onTrue:  "high",
		onFalse: "no_think",
		emit: func(req *openai.ChatCompletionRequest, effort string) any {
			req.ChatTemplateKwargs = map[string]interface{}{"reasoning_effort": effort}
			return req
		},
	}
}

// glmReasoningEffort carries the level in the top-level reasoning_effort field
// next to thinking.type=enabled, as the Zhipu BigModel API expects. GLM-5.x
// cannot disable reasoning, so no_think degrades to the lightest level instead
// of turning it off.
func glmReasoningEffort() effortStrategy {
	return effortStrategy{
		name:    "glm_reasoning_effort",
		valid:   []string{"low", "high", "max"},
		aliases: map[string]string{"no_think": "low"},
		onTrue:  "high",
		onFalse: "low",
		emit: func(req *openai.ChatCompletionRequest, effort string) any {
			return GLMChatCompletionRequest{
				ChatCompletionRequest: *req,
				Thinking:              &ThinkingConfig{Type: "enabled"},
				ReasoningEffort:       effort,
			}
		},
	}
}

// effortStrategyName returns the thinking_control name when the strategy is an
// effort selector, and "" for every other strategy.
func effortStrategyName(strategy ThinkingStrategy) string {
	if s, ok := strategy.(effortStrategy); ok {
		return s.name
	}
	return ""
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
	case "reasoning_effort":
		return reasoningEffort()
	case "glm_reasoning_effort":
		return glmReasoningEffort()
	default:
		// "chat_template_kwargs" and any unknown non-empty value.
		return chatTemplateKwargs{}
	}
}

// resolveThinkingStrategy picks the strategy for an adapter: the
// extra_config.thinking_control override when it applies, otherwise the
// adapter's own default.
//
// chat_template_kwargs.reasoning_effort is the OpenAI-compatible / vLLM form,
// so a saved reasoning_effort override is honored on any provider (self-hosted
// Hy, GLM, or anything whose template reads that field). The Zhipu top-level
// reasoning_effort field is not portable and stays gated to the Zhipu adapter.
func resolveThinkingStrategy(adapter providerAdapter, extraConfig map[string]string) ThinkingStrategy {
	override := parseThinkingOverride(extraConfig)
	if override == nil {
		return adapter.Thinking()
	}
	if effortStrategyName(override) == "glm_reasoning_effort" {
		if _, ok := adapter.(zhipuReasoningProvider); !ok {
			return adapter.Thinking()
		}
	}
	return override
}

// EffectiveThinkingControl reports the provider field that will carry
// ChatOptions.Thinking. It intentionally shares the same adapter/override
// resolution as the real request path so diagnostics do not guess from the
// frontend selection.
func EffectiveThinkingControl(config *ChatConfig) string {
	if config == nil {
		return "none"
	}
	providerName := provider.ProviderName(config.Provider)
	if providerName == "" {
		providerName = provider.DetectProvider(config.BaseURL)
	}
	adapter := resolveProvider(providerName, config.ModelName)
	return thinkingStrategyName(resolveThinkingStrategy(adapter, config.ExtraConfig))
}

func thinkingStrategyName(strategy ThinkingStrategy) string {
	switch s := strategy.(type) {
	case enableThinking:
		return "enable_thinking"
	case thinkingTypeField:
		return "thinking_type"
	case chatTemplateKwargs:
		return "chat_template_kwargs"
	case effortStrategy:
		return s.name
	default:
		return "none"
	}
}
