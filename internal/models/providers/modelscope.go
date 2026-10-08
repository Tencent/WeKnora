// Package providers registers ModelScope's API-Inference service.
//
// Facts (https://modelscope.cn/docs/model-service/API-Inference/intro,
// https://modelscope.cn/docs/model-service/API-Inference/limits):
//   - the OpenAI-compatible base URL is https://api-inference.modelscope.cn/v1
//     and auth is `Authorization: Bearer <ModelScope Access Token>` (the
//     account must be bound to a verified Aliyun account);
//   - the same host also serves an OpenAI Responses endpoint on /v1 (Qwen3.5
//     family only) and a beta Anthropic Messages endpoint on the bare host
//     (https://api-inference.modelscope.cn/anthropic/v1/messages); this vendor
//     only selects Chat Completions;
//   - model ids are ModelScope repo ids ("Owner/Model-Name"); which of them
//     API-Inference serves changes over time — the docs state outright that
//     older models are taken offline as newer ones land, and only the model
//     page (the API-Inference panel) is authoritative;
//   - API-Inference is a free, non-commercial service with dynamic rate limits
//     and no per-token price: calls are metered in 魔粒 credits
//     (0.5 / 1 / 2 per call by model tier), which is why the entries below
//     carry a zero token cost rather than a real one;
//   - the documented scope is open LLM, multimodal (MLLM) and AIGC
//     text-to-image models.
//
// Unverified: the thinking switch — ModelScope documents no thinking
// parameter, so the top-level `enable_thinking` of the DashScope/Bailian
// channel behind API-Inference is kept as a guess, as is `max_tokens` for the
// output cap; embedding support is not documented anywhere on the
// API-Inference pages, so the Embedding model type and the two Qwen3-Embedding
// entries are unconfirmed; every model id, context window, max output and
// thinking flag below predates the current lineup, and as of 2026-09-20 none
// of them advertises a "ModelScope" provider on
// https://modelscope.cn/api/v1/inference/list_model_providers (models that are
// served, such as Qwen/Qwen3.5-35B-A3B or ZhipuAI/GLM-5.2, do) — they are kept
// because no official page states their retirement, but the list needs a
// refresh against the model pages.
package providers

import (
	_ "embed"

	"github.com/Tencent/WeKnora/internal/models/api"
	"github.com/Tencent/WeKnora/internal/types"
)

//go:embed assets/modelscope.svg
var modelscopeIcon []byte

// ModelscopeID is the provider identifier stored on model rows.
const ModelscopeID = "modelscope"

// ModelscopeBaseURL is the OpenAI-compatible inference endpoint.
const ModelscopeBaseURL = "https://api-inference.modelscope.cn/v1"

func newModelscopeProvider() *Definition {
	return &Definition{
		ID:           ModelscopeID,
		Name:         "ModelScope",
		Names:        map[string]string{"zh-CN": "魔搭 ModelScope"},
		Description:  "Qwen/Qwen3-235B-A22B-Instruct-2507, ZhipuAI/GLM-4.6, Qwen/Qwen3-Embedding-8B, etc.",
		Website:      "https://modelscope.cn",
		Icon:         modelscopeIcon,
		API:          api.APIOpenAICompletions,
		Order:        19,
		RequiresAuth: true,
		Auth:         AuthBearer,
		URLPatterns:  []string{"modelscope.cn"},
		DefaultBaseURLs: map[types.ModelType]string{
			types.ModelTypeKnowledgeQA: ModelscopeBaseURL,
			types.ModelTypeEmbedding:   ModelscopeBaseURL,
			types.ModelTypeVLLM:        ModelscopeBaseURL,
		},
		ModelTypes: []types.ModelType{
			types.ModelTypeKnowledgeQA,
			types.ModelTypeEmbedding,
			types.ModelTypeVLLM,
		},
		Compat: VendorCompat{
			// Embeddings keeps the bare baseline: /v1/embeddings answers, but no
			// ModelScope page documents a single parameter of it.
			OpenAICompletions: api.OpenAICompletionsCompat{
				MaxTokensField: api.Ptr("max_tokens"),
				ThinkingFormat: api.Ptr(api.ThinkingFormatEnableThinking),
			},
		},
	}
}
