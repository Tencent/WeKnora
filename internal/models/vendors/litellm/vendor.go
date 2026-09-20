// Package litellm registers a self-hosted LiteLLM proxy
// (https://github.com/BerriAI/litellm).
//
// Facts (https://docs.litellm.ai/docs/reasoning_content):
//   - the proxy speaks plain OpenAI Chat Completions and translates
//     `reasoning_effort` for every upstream it fronts, so the OpenAI
//     thinking format is used and graded effort is supported. The documented
//     vocabulary is minimal | low | medium | high | xhigh | max (mapped onto
//     Anthropic budgets of 1024/1024/2048/4096/8192/16384), plus `none` to
//     turn thinking off — which is why the vendor level map spells "off" as
//     "none" and keeps xhigh and max enabled;
//   - both `max_tokens` and `max_completion_tokens` are listed as supported
//     input params (https://docs.litellm.ai/docs/completion/input) with no
//     stated preference, so the protocol default max_completion_tokens
//     stands;
//   - the default base URL is a placeholder the operator must replace; its
//     hostname contains "litellm" so DetectByURL recognises catalog rows,
//     while loopback URLs stay generic (they are SSRF-blocked unless
//     whitelisted). The proxy's own default is port 4000, and both
//     /v1/chat/completions and /chat/completions are documented;
//   - the catalog ships no models: deployments name their own routes.
//
// Unverified: `chat_template_kwargs` is not named in the LiteLLM docs. It
// should reach a vLLM/SGLang upstream under the general rule that "LiteLLM
// treats any non-openai param as a provider-specific param, and passes it to
// the provider in the request body", but a proxy configured with
// allowed_openai_params may drop it, so operators fronting open-weight
// models may need thinking_control=chat_template_kwargs plus that allowlist.
package litellm

import (
	_ "embed"

	"github.com/Tencent/WeKnora/internal/models/api"
	"github.com/Tencent/WeKnora/internal/models/catalog"
	"github.com/Tencent/WeKnora/internal/types"
)

//go:embed models.json
var modelsJSON []byte

//go:embed icon.svg
var icon []byte

// ID is the provider identifier stored on model rows.
const ID = "litellm"

// BaseURL is a placeholder the operator replaces with a reachable proxy.
const BaseURL = "http://your_litellm_proxy/v1"

func init() {
	catalog.Register(&catalog.Vendor{
		ID:           ID,
		Name:         "LiteLLM",
		Names:        map[string]string{"zh-CN": "LiteLLM"},
		Description:  "Self-hosted LiteLLM proxy: one OpenAI-compatible endpoint to 100+ providers.",
		Descriptions: map[string]string{"zh-CN": "自建 LiteLLM 代理：一个 OpenAI 兼容入口对接 100+ 模型服务。"},
		Website:      "https://docs.litellm.ai",
		Icon:         icon,
		API:          api.APIOpenAICompletions,
		Order:        41,
		RequiresAuth: true,
		Auth:         catalog.AuthBearer,
		URLPatterns:  []string{"litellm"},
		DefaultBaseURLs: map[types.ModelType]string{
			types.ModelTypeKnowledgeQA: BaseURL,
			types.ModelTypeEmbedding:   BaseURL,
			types.ModelTypeVLLM:        BaseURL,
		},
		ModelTypes: []types.ModelType{
			types.ModelTypeKnowledgeQA,
			types.ModelTypeEmbedding,
			types.ModelTypeVLLM,
		},
		Compat: catalog.VendorCompat{
			OpenAICompletions: catalog.OpenAICompletionsCompat{
				ThinkingFormat:          catalog.Ptr(catalog.ThinkingFormatOpenAI),
				SupportsReasoningEffort: catalog.Ptr(true),
			},
		},
		// LiteLLM documents the whole ladder plus "none" as the off switch.
		ThinkingLevels: api.ThinkingLevelMap{
			api.ReasoningOff:   api.StringPtr("none"),
			api.ReasoningXHigh: api.StringPtr("xhigh"),
			api.ReasoningMax:   api.StringPtr("max"),
		},
		Models: catalog.MustParseModels(modelsJSON),
	})
}
