// Package novita registers the Novita AI gateway.
//
// Facts (https://docs.novita.ai — novita.ai/docs/* redirects there):
//   - chat, embedding and VLM share https://api.novita.ai/openai/v1;
//     /v1/rerank, /v1/completions, /v1/batches and /v1/files sit alongside;
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
package novita

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
const ID = "novita"

// BaseURL is the OpenAI-compatible gateway endpoint.
const BaseURL = "https://api.novita.ai/openai/v1"

func init() {
	catalog.Register(&catalog.Vendor{
		ID:           ID,
		Name:         "Novita AI",
		Names:        map[string]string{"zh-CN": "Novita AI"},
		Description:  "moonshotai/kimi-k3, zai-org/glm-5.2, deepseek/deepseek-v4-pro, qwen/qwen3-embedding-0.6b, etc.",
		Website:      "https://novita.ai",
		Icon:         icon,
		API:          api.APIOpenAICompletions,
		Order:        52,
		RequiresAuth: true,
		Auth:         catalog.AuthBearer,
		URLPatterns:  []string{"api.novita.ai", "novita.ai"},
		DefaultBaseURLs: map[types.ModelType]string{
			types.ModelTypeKnowledgeQA: BaseURL,
			types.ModelTypeEmbedding:   BaseURL,
			types.ModelTypeVLLM:        BaseURL,
		},
		ModelTypes: []types.ModelType{
			types.ModelTypeKnowledgeQA,
			types.ModelTypeEmbedding,
			types.ModelTypeVLLM,
		},
		Compat: catalog.VendorCompat{
			OpenAICompletions: catalog.OpenAICompletionsCompat{
				MaxTokensField: catalog.Ptr("max_tokens"),
				ThinkingFormat: catalog.Ptr(catalog.ThinkingFormatEnableThinking),
			},
		},
		Models: catalog.MustParseModels(modelsJSON),
	})
}
