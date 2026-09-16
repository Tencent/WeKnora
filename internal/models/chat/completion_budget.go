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
// Default is max_completion_tokens (OpenAI Chat Completions, Azure, Ark),
// matching Pi. Only providers whose docs (or Pi's catalog) use the legacy
// name stay on max_tokens. WeKnora-only hosts that document max_tokens
// (LKEAP) are listed here too. Unknown OpenAI-compat hosts — including
// Aliyun DashScope — keep the default. GPT-5 / o-series always use
// max_completion_tokens.
func wireCompletionTokenField(name provider.ProviderName, model string) completionTokenField {
	if provider.IsOpenAIReasoningOrGPT5Model(model) {
		return completionTokenFieldMaxCompletionTokens
	}
	switch name {
	case provider.ProviderDeepSeek, // api-docs.deepseek.com: max_tokens only
		provider.ProviderZhipu,       // open.bigmodel.cn: max_tokens only (Pi isZai)
		provider.ProviderSiliconFlow, // docs.siliconflow.com schema: max_tokens
		provider.ProviderMoonshot,    // Pi useMaxTokens (moonshot.ai)
		provider.ProviderNvidia,      // Pi useMaxTokens (NIM / vLLM)
		provider.ProviderGeneric,     // self-hosted vLLM typically ignores the new field
		provider.ProviderGPUStack,    // private vLLM-class runtime
		provider.ProviderLKEAP:       // cloud.tencent.com/document/product/1772/115969: max_tokens only
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
