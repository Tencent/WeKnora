// Package providers registers Google's Gemini API.
//
// Facts (https://ai.google.dev/gemini-api/docs/thinking):
//   - the native protocol is generateContent under
//     https://generativelanguage.googleapis.com/v1beta with the
//     `x-goog-api-key` header;
//   - the two thinking knobs are generation-specific and mutually
//     exclusive: Gemini 3.x takes `thinkingConfig.thinkingLevel`, Gemini 2.5
//     takes `thinkingConfig.thinkingBudget` and ignores/rejects
//     thinkingLevel; sending both in one call is a 400. The per-model level
//     sets differ too — 3.8 Flash, 3.7 Flash and 3.1 Pro accept
//     low | medium | high, 3.6 Flash, 3.5 Flash, 3.5 Flash Lite, 3.1 Flash
//     Lite and 3 Flash Preview add minimal, and 3 Pro Preview accepts only
//     low | high, which is what the conservative gemini-3* family entry
//     assumes;
//   - no thinkingLevel value turns thinking off, so every Gemini 3 entry is
//     "off": null. On 2.5, Pro is the exception that cannot be switched off
//     (its budget must be 128..32768, or -1 for dynamic) while Flash and
//     Flash Lite accept budget 0; the vendor budget ladder starts at 128 for
//     that reason and tops out at 32768;
//   - pointing base_url at https://generativelanguage.googleapis.com/v1beta/openai
//     keeps the OpenAI-compatible Chat Completions protocol (Resolve detects
//     the /openai suffix) and switches the credential to
//     `Authorization: Bearer`, the only form that facade documents
//     (https://ai.google.dev/gemini-api/docs/openai). Every row created
//     before the native protocol became the default carries exactly that base
//     URL, so this is the auth those rows have always used; on that facade
//     `reasoning_effort` is translated to
//     thinking_level or thinking_budget per model,
//     `extra_body.google.thinking_config` carries include_thoughts, and usage
//     reports cached tokens (the OpenAICompletions vendor compat below);
//   - embeddings are served natively and on the facade:
//     gemini-embedding-001 defaults to 3072 dimensions over 2048 input
//     tokens, gemini-embedding-2 to 3072 over 8192 and accepts multimodal
//     input (https://ai.google.dev/gemini-api/docs/embeddings).
//
// Unverified:
//   - `extra_content` on tool calls is not named anywhere in the current
//     OpenAI-compat guide; the tool_call_extra_fields entry is kept because
//     dropping an opaque round-trip field is the riskier direction.
//   - gemini-embedding-2 is listed as `gemini-embedding-2` in the embeddings
//     guide but as `gemini-embedding-2-preview` on the models page and in the
//     OpenAI-compat samples, so both spellings resolve to one entry.
//   - gemini-3.1-flash-lite is on the models page but absent from the
//     thinking guide's level table, so its minimal rung is an assumption
//     carried over from the other Flash Lite tiers.
package providers

import (
	_ "embed"

	"github.com/Tencent/WeKnora/internal/models/api"
	"github.com/Tencent/WeKnora/internal/types"
)

//go:embed assets/gemini.svg
var geminiIcon []byte

// GeminiID is the provider identifier stored on model rows.
const GeminiID = "gemini"

// GeminiBaseURL is the native Gemini API endpoint.
const GeminiBaseURL = "https://generativelanguage.googleapis.com/v1beta"

// GeminiOpenAICompatBaseURL keeps the OpenAI Chat Completions protocol.
const GeminiOpenAICompatBaseURL = GeminiBaseURL + "/openai"

func newGeminiProvider() *Definition {
	return &Definition{
		ID:    GeminiID,
		Name:  "Google Gemini",
		Names: map[string]string{"zh-CN": "Google Gemini"},
		Description: "gemini-3.8-flash, gemini-3.1-pro-preview, gemini-2.5-pro, gemini-embedding-2, etc. " +
			"Native generateContent by default; set base_url to " + GeminiOpenAICompatBaseURL +
			" to keep the OpenAI-compatible protocol.",
		Descriptions: map[string]string{
			"zh-CN": "gemini-3.8-flash, gemini-3.1-pro-preview, gemini-2.5-pro, gemini-embedding-2 等。" +
				"默认走原生 generateContent 协议；将 base_url 指向 " + GeminiOpenAICompatBaseURL + " 可继续使用 OpenAI 兼容协议。",
		},
		Website:      "https://aistudio.google.com",
		Icon:         geminiIcon,
		API:          api.APIGoogleGenerativeAI,
		EmbeddingAPI: api.EmbeddingGoogle,
		Order:        33,
		RequiresAuth: true,
		Auth:         AuthGoogleAPIKey,
		// The OpenAI-compatible facade documents only Authorization: Bearer.
		// Sending x-goog-api-key there would change the credential of every
		// row stored before the native protocol became the default — they all
		// carry the .../v1beta/openai base URL.
		AuthByAPI: map[api.API]AuthStyle{
			api.APIOpenAICompletions: AuthBearer,
		},
		URLPatterns: []string{"generativelanguage.googleapis.com"},
		DefaultBaseURLs: map[types.ModelType]string{
			types.ModelTypeKnowledgeQA: GeminiBaseURL,
			types.ModelTypeEmbedding:   GeminiBaseURL,
		},
		ModelTypes: []types.ModelType{
			types.ModelTypeKnowledgeQA,
			types.ModelTypeEmbedding,
		},
		Compat: VendorCompat{
			// https://ai.google.dev/api/embeddings: per request model, content
			// and embedContentConfig {taskType, title, outputDimensionality,
			// autoTruncate, …}; the same option names at the top level of the
			// request are marked deprecated. The pre-catalog client sent the
			// width top-level as output_dimensionality, which the proto JSON
			// parser reads as the same field. taskType exists on
			// gemini-embedding-001 only and is deliberately not declared: it
			// changes the document vectors, so turning it on would split every
			// existing index across two spaces (Tencent/WeKnora#1401).
			Embeddings: api.EmbeddingsCompat{
				DimensionsField: api.Ptr("outputDimensionality"),
			},
			// Used when the operator points base_url at the /openai facade.
			OpenAICompletions: api.OpenAICompletionsCompat{
				ToolCallExtraFields:     []string{"extra_content"},
				ThinkingFormat:          api.Ptr(api.ThinkingFormatOpenAI),
				SupportsReasoningEffort: api.Ptr(true),
				PromptCacheAccounting:   api.Ptr(true),
			},
			// Native protocol keeps the generativelanguage defaults.
			GoogleGenerativeAI: api.GoogleGenerativeAICompat{},
		},
	}
}
