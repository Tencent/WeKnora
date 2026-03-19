package chat

import (
	"context"
	"strings"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/models/provider"
	"github.com/sashabaranov/go-openai"
)

// ProviderSpec describes provider-specific behavior for chat completions.
// Each spec is registered with a ProviderName and optionally a model matcher.
type ProviderSpec struct {
	Provider provider.ProviderName
	// ModelMatcher: if non-nil, this spec only applies when the model name matches.
	// Used for sub-provider routing (e.g. Qwen3 within Aliyun).
	ModelMatcher func(modelName string) bool
	// RequestCustomizer: provider-specific request modification.
	RequestCustomizer func(req *openai.ChatCompletionRequest, opts *ChatOptions, isStream bool) (any, bool)
	// EndpointCustomizer: provider-specific endpoint URL override.
	EndpointCustomizer func(baseURL string, modelID string, isStream bool) string
}

// chatProviderSpecs is the ordered list of provider specs.
// Order matters: more specific specs (with ModelMatcher) should come before generic ones.
var chatProviderSpecs = []ProviderSpec{
	// Qwen3 (must be before generic Aliyun — only matches Qwen3 models)
	{
		Provider:          provider.ProviderAliyun,
		ModelMatcher:      func(name string) bool { return provider.IsQwen3Model(name) },
		RequestCustomizer: qwen3RequestCustomizer,
	},
	// LKEAP
	{
		Provider:          provider.ProviderLKEAP,
		RequestCustomizer: lkeapRequestCustomizer,
	},
	// DeepSeek
	{
		Provider:          provider.ProviderDeepSeek,
		RequestCustomizer: deepseekRequestCustomizer,
	},
	// Generic (vLLM)
	{
		Provider:          provider.ProviderGeneric,
		RequestCustomizer: genericRequestCustomizer,
	},
	// NVIDIA
	{
		Provider:           provider.ProviderNvidia,
		EndpointCustomizer: nvidiaEndpointCustomizer,
	},
}

// findProviderSpec finds the matching spec for the given provider and model name.
func findProviderSpec(providerName provider.ProviderName, modelName string) *ProviderSpec {
	for i := range chatProviderSpecs {
		spec := &chatProviderSpecs[i]
		if spec.Provider != providerName {
			continue
		}
		if spec.ModelMatcher != nil && !spec.ModelMatcher(modelName) {
			continue
		}
		return spec
	}
	return nil
}

// --- Type definitions (moved from wrapper files) ---

// QwenChatCompletionRequest Qwen 模型的自定义请求结构体
type QwenChatCompletionRequest struct {
	openai.ChatCompletionRequest
	EnableThinking *bool `json:"enable_thinking,omitempty"`
}

// LKEAPThinkingConfig 思维链配置（LKEAP 特有格式）
type LKEAPThinkingConfig struct {
	Type string `json:"type"` // "enabled" 或 "disabled"
}

// LKEAPChatCompletionRequest LKEAP 自定义请求结构体
type LKEAPChatCompletionRequest struct {
	openai.ChatCompletionRequest
	Thinking *LKEAPThinkingConfig `json:"thinking,omitempty"` // 思维链开关（仅 V3.x 系列）
}

// --- Customizer functions ---

// qwen3RequestCustomizer 自定义 Qwen3 请求
// Qwen3 模型在非流式请求时需要显式禁用 thinking
func qwen3RequestCustomizer(
	req *openai.ChatCompletionRequest, _ *ChatOptions, isStream bool,
) (any, bool) {
	if !isStream {
		qwenReq := QwenChatCompletionRequest{
			ChatCompletionRequest: *req,
		}
		enableThinking := false
		qwenReq.EnableThinking = &enableThinking
		return qwenReq, true
	}
	return nil, false
}

// lkeapRequestCustomizer 自定义 LKEAP 请求
// 仅对 DeepSeek V3.x 系列模型设置 thinking 参数；R1 系列默认开启思维链
// 参考：https://cloud.tencent.com/document/product/1772/115963
func lkeapRequestCustomizer(
	req *openai.ChatCompletionRequest, opts *ChatOptions, _ bool,
) (any, bool) {
	modelName := req.Model
	if !strings.Contains(strings.ToLower(modelName), "deepseek-v3") || opts == nil || opts.Thinking == nil {
		return nil, false
	}

	lkeapReq := LKEAPChatCompletionRequest{
		ChatCompletionRequest: *req,
	}

	thinkingType := "disabled"
	if *opts.Thinking {
		thinkingType = "enabled"
	}
	lkeapReq.Thinking = &LKEAPThinkingConfig{Type: thinkingType}

	return lkeapReq, true
}

// deepseekRequestCustomizer 自定义 DeepSeek 请求
// DeepSeek 模型不支持 tool_choice 参数，需要清除
func deepseekRequestCustomizer(
	req *openai.ChatCompletionRequest, opts *ChatOptions, _ bool,
) (any, bool) {
	if opts != nil && opts.ToolChoice != "" {
		logger.Infof(context.Background(), "deepseek model, skip tool_choice")
		req.ToolChoice = nil
	}
	return nil, false
}

// genericRequestCustomizer 自定义 Generic 请求
// Generic provider（如 vLLM）使用 ChatTemplateKwargs 传递 thinking 参数
func genericRequestCustomizer(
	req *openai.ChatCompletionRequest, opts *ChatOptions, _ bool,
) (any, bool) {
	thinking := false
	if opts != nil && opts.Thinking != nil {
		thinking = *opts.Thinking
	}
	req.ChatTemplateKwargs = map[string]interface{}{
		"enable_thinking": thinking,
	}
	return nil, false
}

// nvidiaEndpointCustomizer 自定义 NVIDIA 请求地址
// NVIDIA 模型使用 BaseURL 作为完整请求地址
func nvidiaEndpointCustomizer(baseURL string, _ string, _ bool) string {
	return baseURL
}
