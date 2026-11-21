// Package providers registers Runcrate's OpenAI-compatible inference API.
//
// Facts (checked against https://api.runcrate.ai/v1 on 2026-10-25):
//   - one base URL serves chat completions and embeddings; model ids are
//     always `vendor/model` (deepseek-ai/DeepSeek-V4.1-Flash), as listed by
//     the public GET /v1/models;
//   - the output cap is honoured as `max_tokens` (also as
//     `max_completion_tokens`); finish_reason is "length" when it is hit;
//   - thinking is a flat `reasoning_effort` string. none, minimal, low,
//     medium, high, xhigh and max are all accepted, and "none" turns
//     reasoning off where the model allows it (0 reasoning tokens on
//     DeepSeek V4.1 Flash). Models whose reasoning cannot be switched off
//     carry `off: null` in overrides.json;
//   - embeddings accept encoding_format (float and base64) and
//     `dimensions` (Qwen3-Embedding-8B returned 512 values when asked for
//     512);
//   - image input in chat works on DeepSeek V4.1 Flash, GLM-5.3 Flash and
//     Kimi K2.6.
package providers

import (
	_ "embed"

	"github.com/Tencent/WeKnora/internal/models/api"
	"github.com/Tencent/WeKnora/internal/types"
)

//go:embed assets/runcrate.svg
var runcrateIcon []byte

// RuncrateID is the provider identifier stored on model rows.
const RuncrateID = "runcrate"

// RuncrateBaseURL is the OpenAI-compatible inference endpoint.
const RuncrateBaseURL = "https://api.runcrate.ai/v1"

func newRuncrateProvider() *Definition {
	return &Definition{
		ID:    RuncrateID,
		Name:  "Runcrate",
		Names: map[string]string{"zh-CN": "Runcrate"},
		Description: "deepseek-ai/DeepSeek-V4.1-Flash, zai-org/GLM-5.3, Qwen/Qwen3.8-Flash, " +
			"Qwen/Qwen3-Embedding-8B, etc.",
		Website: "https://www.runcrate.ai/inference/free" +
			"?utm_source=WeKnora&utm_medium=directory&utm_campaign=gpu_pricing",
		Icon:         runcrateIcon,
		API:          api.APIOpenAICompletions,
		Order:        53,
		RequiresAuth: true,
		Auth:         AuthBearer,
		URLPatterns:  []string{"api.runcrate.ai"},
		DefaultBaseURLs: map[types.ModelType]string{
			types.ModelTypeKnowledgeQA: RuncrateBaseURL,
			types.ModelTypeEmbedding:   RuncrateBaseURL,
			types.ModelTypeVLLM:        RuncrateBaseURL,
		},
		ModelTypes: []types.ModelType{
			types.ModelTypeKnowledgeQA,
			types.ModelTypeEmbedding,
			types.ModelTypeVLLM,
		},
		Compat: VendorCompat{
			Embeddings: api.EmbeddingsCompat{
				SendEncodingFormat: api.Ptr(true),
				DimensionsField:    api.Ptr("dimensions"),
			},
			OpenAICompletions: api.OpenAICompletionsCompat{
				MaxTokensField:          api.Ptr("max_tokens"),
				ThinkingFormat:          api.Ptr(api.ThinkingFormatOpenAI),
				SupportsReasoningEffort: api.Ptr(true),
			},
		},
		// Runcrate accepts the whole ladder up to max; "off" is the literal
		// "none".
		ThinkingLevels: api.ThinkingLevelMap{
			api.ReasoningOff:   api.StringPtr("none"),
			api.ReasoningXHigh: api.StringPtr("xhigh"),
			api.ReasoningMax:   api.StringPtr("max"),
		},
	}
}
