// Package providers registers the Opper gateway.
//
// Facts (https://docs.opper.ai/build/gateway/drop-in-sdks,
// https://docs.opper.ai/build/gateway/completions,
// https://docs.opper.ai/build/multimodal/vision-pdfs and the published
// schema at https://api.opper.ai/v3/openapi.yaml):
//   - chat, embeddings and the model list share the OpenAI-compatible base
//     https://api.opper.ai/v3/compat, and auth is `Authorization: Bearer
//     <key>` ("Opper reads the key from `Authorization: Bearer`");
//   - a model id is a bare pool name (`claude-sonnet-4-6`, `gpt-5.5`), which
//     Opper routes across the providers that serve the model, or
//     `provider/model` (`anthropic/claude-sonnet-4-6`) to pin one route;
//     GET /v3/compat/models lists both;
//   - output cap is `max_tokens`, the field the chat guide documents ("Cap
//     the response length"); `max_completion_tokens` is in the request
//     schema without a description;
//   - thinking is a flat `reasoning_effort` string: "Reasoning models (the
//     GPT-5 family, Claude with extended thinking) also accept
//     `reasoning_effort: "low" | "medium" | "high"`". minimal is not in that
//     list, so the vendor level map sends it as low; xhigh and max stay off;
//   - thinking text comes back in `reasoning_content`, and usage reports
//     prompt_tokens_details.cached_tokens;
//   - tool calls in a response carry `extra_content` (Gemini's
//     thought_signature), and the request schema accepts it back on replayed
//     assistant turns, so it is round-tripped opaquely;
//   - images go in the chat messages as `image_url` parts ("Vision and PDF
//     are model capabilities, so you send the media to a regular chat model
//     that supports them"), so the VLM type shares the chat base URL;
//   - embeddings take model, input, dimensions, encoding_format and user
//     (https://docs.opper.ai/v3-api-reference/compatibility/create-embeddings);
//   - the catalog ships no models: the pools follow the live list at
//     GET /v3/compat/models, so the operator names the model.
//
// Unverified:
//   - the rungs differ per model: GET /v3/models lists
//     params.reasoning.supported (none, low, medium, high and xhigh for
//     gpt-5.4-mini; low, medium, high and max for claude-sonnet-4-6). The
//     vendor map keeps the three rungs the chat guide documents, and "off"
//     sends no field, so the routed model's own default applies;
//   - the docs do not say whether `temperature` is dropped for upstreams
//     that reject it while reasoning, and with no catalog entries there is
//     no per-model rule that turns sampling off.
package providers

import (
	_ "embed"

	"github.com/Tencent/WeKnora/internal/models/api"
	"github.com/Tencent/WeKnora/internal/types"
)

//go:embed assets/opper.svg
var opperIcon []byte

// OpperID is the provider identifier stored on model rows.
const OpperID = "opper"

// OpperBaseURL is the OpenAI-compatible gateway endpoint.
const OpperBaseURL = "https://api.opper.ai/v3/compat"

func newOpperProvider() *Definition {
	return &Definition{
		ID:           OpperID,
		Name:         "Opper",
		Names:        map[string]string{"zh-CN": "Opper"},
		Description:  "claude-sonnet-4-6, gpt-5.5, gemini-3.8-flash, deepseek-v4-pro, etc.",
		Website:      "https://opper.ai",
		Icon:         opperIcon,
		API:          api.APIOpenAICompletions,
		Order:        43,
		RequiresAuth: true,
		Auth:         AuthBearer,
		URLPatterns:  []string{"api.opper.ai"},
		DefaultBaseURLs: map[types.ModelType]string{
			types.ModelTypeKnowledgeQA: OpperBaseURL,
			types.ModelTypeEmbedding:   OpperBaseURL,
			types.ModelTypeVLLM:        OpperBaseURL,
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
				PromptCacheAccounting:   api.Ptr(true),
				ToolCallExtraFields:     []string{"extra_content"},
			},
		},
		// Opper documents low | medium | high; minimal folds onto low.
		ThinkingLevels: api.ThinkingLevelMap{
			api.ReasoningMinimal: api.StringPtr("low"),
		},
	}
}
