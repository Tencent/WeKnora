package chat

import (
	"strings"

	"github.com/sashabaranov/go-openai"
)

// ExtraConfigThinkingControl is the model parameters.extra_config key for
// selecting how ChatOptions.Thinking is translated to provider HTTP fields.
// The accepted values mirror the strings the frontend writes (see
// ModelEditorDialog.vue): "none", "enable_thinking", "thinking_type",
// "chat_template_kwargs".
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
	default:
		// "chat_template_kwargs" and any unknown non-empty value.
		return chatTemplateKwargs{}
	}
}
