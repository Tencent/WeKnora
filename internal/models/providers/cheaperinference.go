// Package providers registers the Cheaper Inference router.
//
// Facts (https://cheaperinference.com/docs):
//   - the OpenAI-compatible base is https://api.cheaperinference.com/v1 and
//     auth is `Authorization: Bearer <key>`; an Anthropic-compatible
//     Messages surface sits at https://api.cheaperinference.com, which this
//     vendor does not use;
//   - "POST https://api.cheaperinference.com/v1/chat/completions accepts the
//     standard OpenAI chat request body: model, messages, temperature,
//     max_tokens, tools and so on", and the docs say to "Set `max_tokens` on
//     every request", so the output cap is `max_tokens`;
//   - model ids are bare (`gpt-5.4-mini`, not `openai/gpt-5.4-mini`): "Copy
//     the exact model ID into the `model` field";
//   - vision-capable chat models accept OpenAI `image_url` parts, so the VLM
//     type shares the chat base URL;
//   - there is no embeddings, rerank or audio endpoint. The vendor only
//     opens chat and VLM; a knowledge base pairs it with an embedding model
//     from another vendor.
package providers

import (
	_ "embed"

	"github.com/Tencent/WeKnora/internal/models/api"
	"github.com/Tencent/WeKnora/internal/types"
)

//go:embed assets/cheaperinference.svg
var cheaperinferenceIcon []byte

// CheaperinferenceID is the provider identifier stored on model rows.
const CheaperinferenceID = "cheaperinference"

// CheaperinferenceBaseURL is the OpenAI-compatible router endpoint.
const CheaperinferenceBaseURL = "https://api.cheaperinference.com/v1"

func newCheaperinferenceProvider() *Definition {
	return &Definition{
		ID:    CheaperinferenceID,
		Name:  "Cheaper Inference",
		Names: map[string]string{"zh-CN": "Cheaper Inference"},
		Description: "gpt-5.4-mini, gpt-5.4, claude-sonnet-5, etc. " +
			"Each model costs 15–60% less than the list price of its lab.",
		Website:      "https://cheaperinference.com",
		Icon:         cheaperinferenceIcon,
		API:          api.APIOpenAICompletions,
		Order:        43,
		RequiresAuth: true,
		Auth:         AuthBearer,
		URLPatterns:  []string{"api.cheaperinference.com", "cheaperinference.com"},
		DefaultBaseURLs: map[types.ModelType]string{
			types.ModelTypeKnowledgeQA: CheaperinferenceBaseURL,
			types.ModelTypeVLLM:        CheaperinferenceBaseURL,
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
