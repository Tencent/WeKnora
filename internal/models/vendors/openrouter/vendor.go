// Package openrouter registers the OpenRouter gateway.
//
// Facts (https://openrouter.ai/docs/guides/best-practices/reasoning-tokens —
// the old /docs/use-cases/ path now redirects there):
//   - `max_tokens` is documented as deprecated ("Maximum tokens
//     (deprecated, use max_completion_tokens)") and some upstreams enforce a
//     minimum of 16 on it, so this vendor keeps the protocol default
//     `max_completion_tokens`;
//   - thinking is expressed with the unified `reasoning` object
//     ({"effort": ...} or {"enabled": bool}) which OpenRouter translates for
//     every upstream. The effort enum is
//     max | xhigh | high | medium | low | minimal | none, which is why xhigh
//     and max are enabled vendor-wide; per-model support is gated by
//     `supported_efforts` in /api/v1/models, and models flagged mandatory
//     (today gemini-3.8-flash, gemini-3.1-pro-preview, glm-5.3, grok-4.6)
//     reject an off switch entirely. Claude upstreams reject "none" and send
//     minimal as low, so the anthropic/* family is "off": null with minimal
//     folded onto low;
//   - Anthropic-routed models honour `cache_control` breakpoints, ephemeral
//     with a 5m or 1h ttl and at most four explicit breakpoints (the
//     anthropic/* family sets cache_control_format "anthropic");
//   - `prompt_cache_key` is accepted as the sticky routing key (OpenRouter's
//     own preferred field is `session_id`) and usage reports
//     prompt_tokens_details.cached_tokens;
//   - embeddings are routed through the same /api/v1 base
//     (https://openrouter.ai/docs/api_reference/embeddings).
//
// Unverified: GET /api/v1/models lists chat models only — embedding models
// live behind /api/v1/embeddings/models — so any future auto-sync of this
// catalog has to read both listings or it will silently lose embeddings.
package openrouter

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
const ID = "openrouter"

// BaseURL is the OpenAI-compatible gateway endpoint.
const BaseURL = "https://openrouter.ai/api/v1"

func init() {
	catalog.Register(&catalog.Vendor{
		ID:    ID,
		Name:  "OpenRouter",
		Names: map[string]string{"zh-CN": "OpenRouter"},
		Description: "openai/gpt-5.5, anthropic/claude-opus-5, google/gemini-3.8-flash, " +
			"deepseek/deepseek-v4-pro, etc.",
		Website:      "https://openrouter.ai",
		Icon:         icon,
		API:          api.APIOpenAICompletions,
		Order:        40,
		RequiresAuth: true,
		Auth:         catalog.AuthBearer,
		URLPatterns:  []string{"openrouter.ai"},
		DefaultBaseURLs: map[types.ModelType]string{
			types.ModelTypeKnowledgeQA: BaseURL,
			types.ModelTypeEmbedding:   BaseURL,
			types.ModelTypeRerank:      BaseURL,
			types.ModelTypeVLLM:        BaseURL,
		},
		ModelTypes: []types.ModelType{
			types.ModelTypeKnowledgeQA,
			types.ModelTypeEmbedding,
			types.ModelTypeRerank,
			types.ModelTypeVLLM,
			types.ModelTypeASR,
		},
		Compat: catalog.VendorCompat{
			Transcriptions: catalog.TranscriptionsCompat{
				// https://openrouter.ai/docs/api/api-reference/stt/create-transcription:
				// OpenAI-style multipart file + model, json by default. "Max 25
				// MB; send larger files as base64 JSON via input_audio."
				MaxFileBytes: catalog.Ptr(25 << 20),
				// language is an ISO-639-1 form field. The format "is derived
				// from the filename extension", with no closed list.
				LanguageParam: catalog.Ptr(catalog.LanguageForm),
			},
			Embeddings: catalog.EmbeddingsCompat{
				// https://openrouter.ai/docs/api/api-reference/embeddings/create-embeddings:
				// model, input, dimensions, encoding_format, input_type, provider,
				// user. input_type is a free-form string handed to whichever
				// upstream serves the model ("e.g. search_query,
				// search_document"), so its vocabulary is the upstream's, and on
				// an asymmetric model it changes the document vectors. Not
				// declared, for the reason Jina's task is not
				// (Tencent/WeKnora#1401).
				SendEncodingFormat: catalog.Ptr(true),
				DimensionsField:    catalog.Ptr("dimensions"),
			},
			OpenAICompletions: catalog.OpenAICompletionsCompat{
				ThinkingFormat:          catalog.Ptr(catalog.ThinkingFormatOpenRouter),
				SupportsReasoningEffort: catalog.Ptr(true),
				PromptCacheKey:          catalog.Ptr(true),
				PromptCacheAccounting:   catalog.Ptr(true),
			},
		},
		// The reasoning.effort enum covers the whole ladder; per-model
		// support is gated upstream, not here.
		ThinkingLevels: api.ThinkingLevelMap{
			api.ReasoningXHigh: api.StringPtr("xhigh"),
			api.ReasoningMax:   api.StringPtr("max"),
		},
		Models: catalog.MustParseModels(modelsJSON),
	})
}
