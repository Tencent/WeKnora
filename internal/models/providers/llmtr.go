// Package providers registers LLMTR's OpenAI-compatible inference API.
//
// Facts (from the public GET https://llmtr.com/v1/models listing; this
// file does not include live completion or embedding calls):
//   - one base URL, https://llmtr.com/v1, serves chat completions
//     (/v1/chat/completions) and embeddings (/v1/embeddings); model ids
//     are `vendor/model` (qwen/qwen3.8-flash);
//   - both `max_tokens` and `max_completion_tokens` appear in
//     supported_parameters. This provider sends `max_tokens`, matching
//     other OpenAI-compatible gateways in this package;
//   - thinking is not uniform across the catalog: some models list a
//     `reasoning` parameter, some list `reasoning_effort`, and some list
//     neither. No vendor-wide thinking format is declared;
//   - embeddings are advertised at /v1/embeddings. The listing does not
//     include vector dimensions, so this catalog does not seed embedding
//     models and does not declare encoding_format or dimensions fields;
//   - many chat models advertise image (and sometimes video or audio)
//     input, so the VLM model type shares the same base URL.
package providers

import (
	_ "embed"

	"github.com/Tencent/WeKnora/internal/models/api"
	"github.com/Tencent/WeKnora/internal/types"
)

//go:embed assets/llmtr.svg
var llmtrIcon []byte

// LlmtrID is the provider identifier stored on model rows.
const LlmtrID = "llmtr"

// LlmtrBaseURL is the OpenAI-compatible inference endpoint.
const LlmtrBaseURL = "https://llmtr.com/v1"

func newLlmtrProvider() *Definition {
	return &Definition{
		ID:    LlmtrID,
		Name:  "LLMTR",
		Names: map[string]string{"zh-CN": "LLMTR"},
		Description: "qwen/qwen3.8-flash, qwen/qwen3.8-max, qwen/qwen3.8-27b-free, " +
			"google/gemini-3.8-flash, etc.",
		Website:      "https://llmtr.com",
		Icon:         llmtrIcon,
		API:          api.APIOpenAICompletions,
		Order:        43,
		RequiresAuth: true,
		Auth:         AuthBearer,
		URLPatterns:  []string{"llmtr.com"},
		DefaultBaseURLs: map[types.ModelType]string{
			types.ModelTypeKnowledgeQA: LlmtrBaseURL,
			types.ModelTypeEmbedding:   LlmtrBaseURL,
			types.ModelTypeVLLM:        LlmtrBaseURL,
		},
		ModelTypes: []types.ModelType{
			types.ModelTypeKnowledgeQA,
			types.ModelTypeEmbedding,
			types.ModelTypeVLLM,
		},
		Compat: VendorCompat{
			OpenAICompletions: api.OpenAICompletionsCompat{
				MaxTokensField: api.Ptr("max_tokens"),
			},
		},
	}
}
