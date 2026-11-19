// Package providers registers Standard Compute's OpenAI-compatible model router.
//
// Facts (checked against https://api.stdcmpt.com/v1 on 2026-11-09):
//   - one base URL serves chat completions; model ids are `vendor/model`
//     (deepseek/deepseek-v4.1-flash), as listed with context lengths and input
//     modalities by the public GET /v1/models;
//   - the model id `StandardCompute` is routed: each request goes to the
//     cheapest enabled model that can handle it, with automatic fallback. It is
//     the default chat model;
//   - the output cap is read from `max_tokens` (also `max_completion_tokens`);
//   - thinking is a flat `reasoning_effort` string with minimal, low, medium
//     and high. xhigh and max are not offered, so they clamp to high, and no
//     off value is sent, so a model keeps its own default when off is chosen;
//   - image input is limited to the entries whose /v1/models record lists
//     "image" among its input modalities.
//
// Not served: embeddings, rerank and audio. A knowledge base pairs these chat
// models with an embedding model from another vendor.
package providers

import (
	_ "embed"

	"github.com/Tencent/WeKnora/internal/models/api"
	"github.com/Tencent/WeKnora/internal/types"
)

//go:embed assets/standardcompute.svg
var standardComputeIcon []byte

// StandardComputeID is the provider identifier stored on model rows.
const StandardComputeID = "standardcompute"

// StandardComputeBaseURL is the OpenAI-compatible router endpoint.
const StandardComputeBaseURL = "https://api.stdcmpt.com/v1"

func newStandardComputeProvider() *Definition {
	return &Definition{
		ID:    StandardComputeID,
		Name:  "Standard Compute",
		Names: map[string]string{"zh-CN": "Standard Compute"},
		Description: "StandardCompute (routed), deepseek/deepseek-v4.1-flash, z-ai/glm-5.3, " +
			"qwen/qwen3.8-flash, etc.",
		Website:      "https://standardcompute.com",
		Icon:         standardComputeIcon,
		API:          api.APIOpenAICompletions,
		Order:        54,
		RequiresAuth: true,
		Auth:         AuthBearer,
		URLPatterns:  []string{"api.stdcmpt.com"},
		DefaultBaseURLs: map[types.ModelType]string{
			types.ModelTypeKnowledgeQA: StandardComputeBaseURL,
			types.ModelTypeVLLM:        StandardComputeBaseURL,
		},
		ModelTypes: []types.ModelType{
			types.ModelTypeKnowledgeQA,
			types.ModelTypeVLLM,
		},
		Compat: VendorCompat{
			OpenAICompletions: api.OpenAICompletionsCompat{
				MaxTokensField:          api.Ptr("max_tokens"),
				ThinkingFormat:          api.Ptr(api.ThinkingFormatOpenAI),
				SupportsReasoningEffort: api.Ptr(true),
			},
		},
	}
}
