package chat

import (
	"github.com/Tencent/WeKnora/internal/models/provider"
	"github.com/sashabaranov/go-openai"
)

// completionTokenField is the single Chat Completions wire name for a
// completion budget. OpenAI treats max_tokens and max_completion_tokens as
// mutually exclusive; gateways such as Volcengine Ark reject requests that
// carry both (Tencent/WeKnora#3014).
type completionTokenField string

const (
	completionTokenFieldMaxTokens           completionTokenField = "max_tokens"
	completionTokenFieldMaxCompletionTokens completionTokenField = "max_completion_tokens"
)

// CompletionBudget is the single per-call generation cap. MaxTokens and
// MaxCompletionTokens on ChatOptions are input aliases for the same budget
// (YAML, older callers, and the agent UI all feed one of them). When both are
// set, the newer MaxCompletionTokens wins.
func (o *ChatOptions) CompletionBudget() int {
	if o == nil {
		return 0
	}
	if o.MaxCompletionTokens > 0 {
		return o.MaxCompletionTokens
	}
	return o.MaxTokens
}

// wireCompletionTokenField picks the Chat Completions JSON key for this
// provider+model, following the earendil-works/pi compat.maxTokensField
// pattern: one internal budget, exactly one outbound field.
//
// Default is max_completion_tokens (OpenAI Chat Completions, Azure, Ark).
// Providers that document only max_tokens — or silently ignore the newer
// field, as DeepSeek does — stay on the legacy name. GPT-5 / o-series always
// use max_completion_tokens, even when the configured provider would
// otherwise send max_tokens.
func wireCompletionTokenField(name provider.ProviderName, model string) completionTokenField {
	if provider.IsOpenAIReasoningOrGPT5Model(model) {
		return completionTokenFieldMaxCompletionTokens
	}
	switch name {
	case provider.ProviderDeepSeek,
		provider.ProviderGeneric,
		provider.ProviderNvidia,
		provider.ProviderMoonshot,
		provider.ProviderZhipu,
		provider.ProviderGPUStack,
		provider.ProviderAliyun,
		provider.ProviderLKEAP,
		provider.ProviderSiliconFlow,
		provider.ProviderHunyuan,
		provider.ProviderMiniMax,
		provider.ProviderMimo,
		provider.ProviderModelScope,
		provider.ProviderQianfan,
		provider.ProviderQiniu,
		provider.ProviderLongCat,
		provider.ProviderNovita,
		provider.ProviderLiteLLM,
		provider.ProviderGemini,
		provider.ProviderWeKnoraCloud:
		return completionTokenFieldMaxTokens
	default:
		return completionTokenFieldMaxCompletionTokens
	}
}

func applyCompletionBudget(req *openai.ChatCompletionRequest, budget int, field completionTokenField) {
	if req == nil || budget <= 0 {
		return
	}
	switch field {
	case completionTokenFieldMaxTokens:
		req.MaxTokens = budget
	default:
		req.MaxCompletionTokens = budget
	}
}
