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

// RerankBaseURL is the proxy root. LiteLLM documents rerank at /rerank
// rather than /v1/rerank, so it does not hang off the chat base URL.
const RerankBaseURL = "http://your_litellm_proxy"

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
			types.ModelTypeRerank:      RerankBaseURL,
			types.ModelTypeVLLM:        BaseURL,
		},
		ModelTypes: []types.ModelType{
			types.ModelTypeKnowledgeQA,
			types.ModelTypeEmbedding,
			types.ModelTypeRerank,
			types.ModelTypeVLLM,
			types.ModelTypeASR,
		},
		ExtraFields: []catalog.ExtraField{{
			Key:    catalog.ExtraScoreScale,
			Label:  "Rerank score scale",
			Labels: map[string]string{"zh-CN": "Rerank 分数标度"},
			Type:   "select",
			Options: []catalog.ExtraFieldOption{
				{
					Label:  "Unbounded score (Qwen3-Reranker class)",
					Labels: map[string]string{"zh-CN": "无界分数（Qwen3-Reranker 一类）"},
					Value:  "logit",
				},
				{
					Label:  "0..1 relevance (BGE class)",
					Labels: map[string]string{"zh-CN": "0~1 相关度（BGE 一类）"},
					Value:  "probability",
				},
			},
			Placeholder:  "match the reranker actually deployed behind this endpoint",
			Placeholders: map[string]string{"zh-CN": "按这个端点后面实际部署的重排模型选择"},
			// A gateway serves whatever reranker was deployed behind it, and
			// the two families disagree. The vendor default follows the
			// documentation; the operator knows which model is actually there.
			Required:   false,
			ModelTypes: []types.ModelType{types.ModelTypeRerank},
		}},
		Compat: catalog.VendorCompat{
			// Transcriptions keeps the baseline: the proxy serves the OpenAI
			// /audio/transcriptions route for whichever upstream it is
			// configured with (https://docs.litellm.ai/docs/audio_transcription).
			Embeddings: catalog.EmbeddingsCompat{
				// https://docs.litellm.ai/docs/embedding/supported_embedding:
				// model, input, user, dimensions, encoding_format; anything else
				// is forwarded to the upstream as a provider-specific kwarg.
				SendEncodingFormat: catalog.Ptr(true),
				DimensionsField:    catalog.Ptr("dimensions"),
			},
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
