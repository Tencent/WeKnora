// Package providers includes Hugging Face Text Embeddings Inference, which
// serves a model chosen at startup.
// Its native rerank route is POST /rerank with query/texts and a bare array
// response; it does not accept a model field or use the OpenAI /v1 prefix.
// https://huggingface.co/docs/text-embeddings-inference/quick_tour#re-rankers
// https://github.com/huggingface/text-embeddings-inference/blob/main/docs/openapi.json
package providers

import (
	"fmt"
	"strings"

	"github.com/Tencent/WeKnora/internal/models/api"
	"github.com/Tencent/WeKnora/internal/types"
)

// HuggingFaceTEIID is the provider ID for self-hosted TEI rerank models.
const HuggingFaceTEIID = "huggingface_tei"

func newHuggingFaceTEIProvider() *Definition {
	return &Definition{
		ID:           HuggingFaceTEIID,
		Name:         "Hugging Face TEI",
		Names:        map[string]string{"zh-CN": "Hugging Face TEI"},
		Description:  "Self-hosted reranker served by Text Embeddings Inference",
		Descriptions: map[string]string{"zh-CN": "通过 Text Embeddings Inference 自部署的重排模型"},
		Website:      "https://huggingface.co/docs/text-embeddings-inference",
		Icon:         genericIcon,
		API:          api.APIOpenAICompletions,
		RerankAPI:    api.RerankTEI,
		Order:        61,
		RequiresAuth: false,
		Auth:         AuthBearer, // Only TEI servers started with --api-key need this.
		ModelTypes:   []types.ModelType{types.ModelTypeRerank},
		Compat: VendorCompat{Rerank: api.RerankCompat{
			// TEI rejects requests above --max-client-batch-size, which
			// defaults to 32.
			MaxDocuments: api.Ptr(32),
		}},
		Validate: func(cfg *Config) error {
			if cfg == nil {
				return fmt.Errorf("config is nil")
			}
			if strings.TrimSpace(cfg.BaseURL) == "" {
				return fmt.Errorf("base URL is required for Hugging Face TEI provider")
			}
			if strings.TrimSpace(cfg.ModelName) == "" {
				return fmt.Errorf("model name is required")
			}
			return nil
		},
	}
}
