// Package providers registers the Novita AI gateway.
//
// Facts (https://docs.novita.ai — novita.ai/docs/* redirects there):
//   - chat, embedding, VLM and rerank share https://api.novita.ai/openai/v1;
//     /v1/completions, /v1/batches and /v1/files sit alongside;
//   - rerank is the Cohere shape — {model, query, documents, top_n} answering
//     results[].{index, relevance_score, document.text} — so it needs no
//     vendor settings beyond the type being open
//     (https://docs.novita.ai/api-reference/model-apis-llm-create-rerank).
//     No ceiling on the document count is documented, and the reference
//     lists no model ids, so none are shipped: the operator names the model;
//   - output cap is `max_tokens`, documented as required;
//     `max_completion_tokens` is never mentioned;
//   - the thinking switch is a **top-level** `enable_thinking` boolean
//     (default true) — "Controls the switches between thinking and
//     non-thinking modes" — not `chat_template_kwargs.enable_thinking` and
//     not `reasoning_effort`, neither of which appears anywhere in the docs.
//     There is no graded effort, so supports_reasoning_effort stays off and
//     models that cannot stop reasoning are marked "off": null.
//
// Unverified:
//   - the documented enable_thinking support list still names glm-4.5 and
//     deepseek-v3.1/v3.2-exp, so whether the current flagships (glm-5.x,
//     deepseek-v4-*, kimi-k3, minimax-m2.7) honour it is not stated. The
//     switch is only sent when a caller asks for one, so a model that
//     ignores it degrades to its own default rather than failing;
//   - the three embedding entries are served by the API but the embeddings
//     reference restricts `model` to a single enum value (baai/bge-m3), and
//     they are absent from the default /v1/models listing.
package providers

import (
	_ "embed"

	"github.com/Tencent/WeKnora/internal/models/api"
	"github.com/Tencent/WeKnora/internal/types"
)

//go:embed assets/novita.svg
var novitaIcon []byte

// NovitaID is the provider identifier stored on model rows.
const NovitaID = "novita"

// NovitaBaseURL is the OpenAI-compatible gateway endpoint.
const NovitaBaseURL = "https://api.novita.ai/openai/v1"

func newNovitaProvider() *Definition {
	return &Definition{
		ID:           NovitaID,
		Name:         "Novita AI",
		Names:        map[string]string{"zh-CN": "Novita AI"},
		Description:  "moonshotai/kimi-k3, zai-org/glm-5.2, deepseek/deepseek-v4-pro, qwen/qwen3-embedding-0.6b, etc.",
		Website:      "https://novita.ai",
		Icon:         novitaIcon,
		API:          api.APIOpenAICompletions,
		Order:        52,
		RequiresAuth: true,
		Auth:         AuthBearer,
		URLPatterns:  []string{"api.novita.ai", "novita.ai"},
		DefaultBaseURLs: map[types.ModelType]string{
			types.ModelTypeKnowledgeQA: NovitaBaseURL,
			types.ModelTypeEmbedding:   NovitaBaseURL,
			types.ModelTypeRerank:      NovitaBaseURL,
			types.ModelTypeVLLM:        NovitaBaseURL,
		},
		ModelTypes: []types.ModelType{
			types.ModelTypeKnowledgeQA,
			types.ModelTypeEmbedding,
			types.ModelTypeRerank,
			types.ModelTypeVLLM,
		},
		Compat: VendorCompat{
			Embeddings: api.EmbeddingsCompat{
				// https://novita.ai/docs/api-reference/model-apis-llm-create-embeddings:
				// input, model, encoding_format. Nothing else.
				SendEncodingFormat: api.Ptr(true),
			},
			OpenAICompletions: api.OpenAICompletionsCompat{
				MaxTokensField: api.Ptr("max_tokens"),
				ThinkingFormat: api.Ptr(api.ThinkingFormatEnableThinking),
			},
		},
	}
}
