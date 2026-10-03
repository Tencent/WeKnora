// Package providers registers the Requesty router.
//
// Facts (https://docs.requesty.ai/features/reasoning and
// https://docs.requesty.ai/frameworks/openai):
//   - output cap is `max_tokens`; `max_completion_tokens` appears nowhere in
//     the documentation;
//   - thinking is a flat `reasoning_effort` string (there is no
//     OpenRouter-style reasoning object). Accepted values are
//     none | min | low | medium | high | xhigh | max, plus a numeric budget
//     passed as a string. Note the weakest rung is spelled `min`, not
//     `minimal`, which is what the vendor level map corrects;
//   - `max` is not the top rung everywhere: "`xhigh` is OpenAI's highest
//     reasoning effort. It is not the same as `max`. `max` is normalized to
//     `high` for OpenAI models, whereas `xhigh` is a strictly higher tier."
//     The OpenAI-routed entries therefore send `xhigh` for both xhigh and
//     max; Anthropic-routed ones keep `max` as their top rung;
//   - model ids are always `provider/model`: "To specify a model, you must
//     include the provider prefix". Every id in the live catalog carries
//     one, and the bare `model_canonical_name` a record reports is metadata,
//     not a usable value. The catalog entries below are prefixed
//     accordingly;
//   - embeddings and VLM share the https://router.requesty.ai/v1 base; the
//     Anthropic-style surface is a separate path
//     (/anthropic/v1/messages) and xhigh is unreachable through it.
//
// Unverified:
//   - Requesty warns "Do not hardcode model versions — model availability
//     changes", and several routes only exist in dated form
//     (deepseek/deepseek-v4-pro-0813). The undated deepseek-v4-flash has no
//     first-party route at all, so it is left out rather than guessed;
//   - gemini-3.8-flash is routed under `vertex/` only; there is no
//     `google/gemini-3.8-flash`, and the newest `google/` Gemini Flash is
//     3.6;
//   - GET /v1/models is chat-only; embedding models live at
//     /v1/models/embedding, which the docs do not mention.
package providers

import (
	_ "embed"

	"github.com/Tencent/WeKnora/internal/models/api"
	"github.com/Tencent/WeKnora/internal/types"
)

//go:embed assets/requesty.svg
var requestyIcon []byte

// RequestyID is the provider identifier stored on model rows.
const RequestyID = "requesty"

// RequestyBaseURL is the OpenAI-compatible router endpoint.
const RequestyBaseURL = "https://router.requesty.ai/v1"

func newRequestyProvider() *Definition {
	return &Definition{
		ID:           RequestyID,
		Name:         "Requesty",
		Names:        map[string]string{"zh-CN": "Requesty"},
		Description:  "openai/gpt-5.5, anthropic/claude-opus-5, google/gemini-3.1-pro-preview, zai/glm-5.2, etc.",
		Website:      "https://requesty.ai",
		Icon:         requestyIcon,
		API:          api.APIOpenAICompletions,
		Order:        42,
		RequiresAuth: true,
		Auth:         AuthBearer,
		URLPatterns:  []string{"router.requesty.ai", "requesty.ai"},
		DefaultBaseURLs: map[types.ModelType]string{
			types.ModelTypeKnowledgeQA: RequestyBaseURL,
			types.ModelTypeEmbedding:   RequestyBaseURL,
			types.ModelTypeVLLM:        RequestyBaseURL,
		},
		ModelTypes: []types.ModelType{
			types.ModelTypeKnowledgeQA,
			types.ModelTypeEmbedding,
			types.ModelTypeVLLM,
			types.ModelTypeASR,
		},
		Compat: VendorCompat{
			Transcriptions: api.TranscriptionsCompat{
				// https://docs.requesty.ai/api-reference/endpoint/audio-transcriptions-create:
				// OpenAI-compatible multipart, json by default, verbose_json
				// available. "The maximum upload size per request is 32 MB."
				MaxFileBytes: api.Ptr(32 << 20),
				// "flac, mp3, mp4, mpeg, mpga, m4a, ogg, wav, webm"; language
				// is an ISO 639-1 form field.
				Formats:       []string{"flac", "mp3", "mp4", "mpeg", "mpga", "m4a", "ogg", "wav", "webm"},
				LanguageParam: api.Ptr(api.LanguageForm),
			},
			Embeddings: api.EmbeddingsCompat{
				// https://docs.requesty.ai/api-reference/endpoint/embeddings-create:
				// model, input, encoding_format, dimensions.
				SendEncodingFormat: api.Ptr(true),
				DimensionsField:    api.Ptr("dimensions"),
			},
			OpenAICompletions: api.OpenAICompletionsCompat{
				MaxTokensField:          api.Ptr("max_tokens"),
				ThinkingFormat:          api.Ptr(api.ThinkingFormatOpenAI),
				SupportsReasoningEffort: api.Ptr(true),
			},
		},
		// Requesty spells the weakest rung "min" and accepts the whole
		// ladder; "off" is the literal "none".
		ThinkingLevels: api.ThinkingLevelMap{
			api.ReasoningOff:     api.StringPtr("none"),
			api.ReasoningMinimal: api.StringPtr("min"),
			api.ReasoningXHigh:   api.StringPtr("xhigh"),
			api.ReasoningMax:     api.StringPtr("max"),
		},
	}
}
