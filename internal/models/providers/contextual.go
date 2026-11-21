// Package providers registers Contextual AI's hosted text rerank API.
// https://docs.contextual.ai/api-reference/rerank/rerank
package providers

import (
	"github.com/Tencent/WeKnora/internal/models/api"
	"github.com/Tencent/WeKnora/internal/types"
)

// ContextualID identifies Contextual AI in stored model configurations.
const ContextualID = "contextual"

// ContextualRerankBaseURL serves Contextual AI's v1 rerank API.
const ContextualRerankBaseURL = "https://api.contextual.ai/v1"

func newContextualProvider() *Definition {
	return &Definition{
		ID: ContextualID, Name: "Contextual AI",
		Description: "Contextual AI text reranking",
		Website:     "https://contextual.ai", Icon: genericIcon,
		API: api.APIOpenAICompletions, RerankAPI: api.RerankCohere,
		Order: 59, RequiresAuth: true, Auth: AuthBearer,
		URLPatterns:     []string{"api.contextual.ai"},
		DefaultBaseURLs: map[types.ModelType]string{types.ModelTypeRerank: ContextualRerankBaseURL},
		ModelTypes:      []types.ModelType{types.ModelTypeRerank},
		Compat: VendorCompat{Rerank: api.RerankCompat{
			// Request every document: the reference does not specify the
			// result count when top_n is omitted. Scores are already 0..1.
			// Its token budgets cannot be expressed as character limits.
			SendTopN: api.Ptr(true),
		}},
	}
}
