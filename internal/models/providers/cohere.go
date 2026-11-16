// Package providers registers Cohere's hosted text rerank API.
// https://docs.cohere.com/v2/reference/rerank
package providers

import (
	"github.com/Tencent/WeKnora/internal/models/api"
	"github.com/Tencent/WeKnora/internal/types"
)

// CohereID identifies Cohere in stored model configurations.
const CohereID = "cohere"

// CohereRerankBaseURL serves Cohere's v2 rerank API.
const CohereRerankBaseURL = "https://api.cohere.com/v2"

func newCohereProvider() *Definition {
	return &Definition{
		ID: CohereID, Name: "Cohere",
		Description: "Cohere text reranking (rerank-v4.0-pro, rerank-v4.0-fast, rerank-v3.5)",
		Website:     "https://cohere.com", Icon: genericIcon,
		API: api.APIOpenAICompletions, RerankAPI: api.RerankCohere,
		Order: 57, RequiresAuth: true, Auth: AuthBearer,
		URLPatterns:     []string{"api.cohere.com"},
		DefaultBaseURLs: map[types.ModelType]string{types.ModelTypeRerank: CohereRerankBaseURL},
		ModelTypes:      []types.ModelType{types.ModelTypeRerank},
		Compat: VendorCompat{Rerank: api.RerankCompat{
			// The reference recommends no more than 1,000 documents per
			// request; use that as our batch size, not a claimed hard limit.
			// Omitting top_n returns all results. v2 does not need echoed
			// documents: the caller restores their text by result index.
			MaxDocuments: api.Ptr(1000),
		}},
	}
}
