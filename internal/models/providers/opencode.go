// Package providers registers the OpenCode Zen and OpenCode Go gateways.
//
// Facts (https://opencode.ai/docs/providers/, https://opencode.ai/docs/zen/
// and https://opencode.ai/docs/go/, read 2026-10-26):
//   - both are OpenAI-compatible gateways authenticated with an OpenCode
//     Console API key sent as `Authorization: Bearer`. Zen is pay-as-you-go,
//     Go is a subscription;
//   - Zen serves OpenAI-shaped traffic under
//     https://opencode.ai/inference/openai/v1 and Go under
//     https://opencode.ai/inference/go/openai/v1. Each model is documented on
//     exactly one route: GPT, Grok and Muse models on `/responses`, the rest
//     (GLM, Kimi, DeepSeek, MiniMax, Qwen3.8 Max, ...) on `/chat/completions`.
//     The vendor default is Chat Completions and the Responses models carry
//     `"api": "openai-responses"` in catalog/data/overrides.json, so the
//     endpoint table, not a host rule, decides the protocol;
//   - the same host also serves Anthropic Messages (`/inference/anthropic/v1`,
//     `/inference/go/anthropic/v1`), Google GenAI (`/inference/google/v1beta`,
//     `/inference/go/google/v1beta`) and a SystemOne route. Models documented
//     only on those routes (Claude, Gemini, Qwen3.8 Flash, Qwen3.6 Plus and
//     Qwen3.5 Plus on Zen, MiniMax M3 and the Qwen models on Go, Jev) are left
//     out of these catalogs;
//   - no embedding, rerank, VLM or ASR endpoint is documented, so both
//     presets offer KnowledgeQA only;
//   - both live on opencode.ai, so URL detection keys Go on its
//     `/inference/go/` path segment (the longer pattern wins) and leaves every
//     other opencode.ai address to Zen. The trailing slash keeps Zen's
//     `/inference/google/` route from being read as Go.
//
// Left out on purpose: Zen's free-tier models (Big Pickle and the `-free`
// ids) — "Free-tier chat models require OpenCode; use paid models or Go from
// other clients" — and the models listed under "Deprecated models".
//
// Output cap: neither page names the field for the Chat Completions route,
// but the endpoint tables pair every /chat/completions model with the
// `@ai-sdk/openai-compatible` package, which sends `max_tokens`. That is also
// what these models received through the generic provider before this preset
// existed, and what the deepseek / nvidia presets send to the same upstream
// families; an upstream that does not know `max_completion_tokens` would drop
// the cap silently. /responses models send `max_output_tokens` regardless.
//
// Unverified: no thinking switch is documented for the Chat Completions
// route, so both presets send no thinking fields.
package providers

import (
	_ "embed"

	"github.com/Tencent/WeKnora/internal/models/api"
	"github.com/Tencent/WeKnora/internal/types"
)

//go:embed assets/opencode.svg
var opencodeIcon []byte

// OpencodeZenID is the provider identifier stored on model rows.
const OpencodeZenID = "opencode_zen"

// OpencodeZenBaseURL is Zen's OpenAI-compatible endpoint
// (<base>/chat/completions and <base>/responses).
const OpencodeZenBaseURL = "https://opencode.ai/inference/openai/v1"

// OpencodeGoID is the provider identifier stored on model rows.
const OpencodeGoID = "opencode_go"

// OpencodeGoBaseURL is Go's OpenAI-compatible endpoint.
const OpencodeGoBaseURL = "https://opencode.ai/inference/go/openai/v1"

func newOpencodeZenProvider() *Definition {
	return &Definition{
		ID:    OpencodeZenID,
		Name:  "OpenCode Zen",
		Names: map[string]string{"zh-CN": "OpenCode Zen"},
		Description: "gpt-6.1-sol, grok-4.7, qwen3.8-max, deepseek-v4-pro, kimi-k3, glm-5.3, etc. " +
			"(pay-as-you-go)",
		Descriptions: map[string]string{
			"zh-CN": "gpt-6.1-sol、grok-4.7、qwen3.8-max、deepseek-v4-pro、kimi-k3、glm-5.3 等（按量付费）",
		},
		Website:      "https://opencode.ai/zen",
		Icon:         opencodeIcon,
		API:          api.APIOpenAICompletions,
		Order:        43,
		RequiresAuth: true,
		Auth:         AuthBearer,
		URLPatterns:  []string{"opencode.ai"},
		DefaultBaseURLs: map[types.ModelType]string{
			types.ModelTypeKnowledgeQA: OpencodeZenBaseURL,
		},
		ModelTypes: []types.ModelType{types.ModelTypeKnowledgeQA},
		Compat: VendorCompat{
			OpenAICompletions: api.OpenAICompletionsCompat{
				MaxTokensField: api.Ptr("max_tokens"),
			},
		},
	}
}

func newOpencodeGoProvider() *Definition {
	return &Definition{
		ID:    OpencodeGoID,
		Name:  "OpenCode Go",
		Names: map[string]string{"zh-CN": "OpenCode Go"},
		Description: "kimi-k3, glm-5.3, deepseek-v4-pro, mimo-v2.6-pro, grok-4.7, gpt-6-luna, etc. " +
			"(subscription)",
		Descriptions: map[string]string{
			"zh-CN": "kimi-k3、glm-5.3、deepseek-v4-pro、mimo-v2.6-pro、grok-4.7、gpt-6-luna 等（订阅制）",
		},
		Website:      "https://opencode.ai/go",
		Icon:         opencodeIcon,
		API:          api.APIOpenAICompletions,
		Order:        44,
		RequiresAuth: true,
		Auth:         AuthBearer,
		URLPatterns:  []string{"opencode.ai/inference/go/"},
		DefaultBaseURLs: map[types.ModelType]string{
			types.ModelTypeKnowledgeQA: OpencodeGoBaseURL,
		},
		ModelTypes: []types.ModelType{types.ModelTypeKnowledgeQA},
		Compat: VendorCompat{
			OpenAICompletions: api.OpenAICompletionsCompat{
				MaxTokensField: api.Ptr("max_tokens"),
			},
		},
	}
}
