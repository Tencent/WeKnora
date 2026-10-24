// Package providers registers DeepInfra's native text rerank endpoint.
// https://docs.deepinfra.com/apis/reranker
package providers

import (
	"github.com/Tencent/WeKnora/internal/models/api"
	"github.com/Tencent/WeKnora/internal/types"
)

// DeepInfraID identifies DeepInfra in stored model configurations.
const DeepInfraID = "deepinfra"

// DeepInfraRerankBaseURL prefixes the model-specific inference endpoint.
const DeepInfraRerankBaseURL = "https://api.deepinfra.com/v1/inference"

func newDeepInfraProvider() *Definition {
	return &Definition{
		ID: DeepInfraID, Name: "DeepInfra",
		Description: "DeepInfra native text reranking (Qwen3-Reranker-4B)",
		Website:     "https://deepinfra.com", Icon: genericIcon,
		API: api.APIOpenAICompletions, RerankAPI: api.RerankPairwise,
		Order: 58, RequiresAuth: true, Auth: AuthBearer,
		// Do not reclassify existing OpenAI-compatible /v1/openai rows.
		URLPatterns:     []string{"api.deepinfra.com/v1/inference"},
		DefaultBaseURLs: map[types.ModelType]string{types.ModelTypeRerank: DeepInfraRerankBaseURL},
		ModelTypes:      []types.ModelType{types.ModelTypeRerank},
		Compat: VendorCompat{Rerank: api.RerankCompat{
			// The official reranker guide specifies relevance probabilities,
			// not logits. The 4B model's batch limit is in its catalog entry:
			// https://api.deepinfra.com/models/Qwen/Qwen3-Reranker-4B
			ScoreScale: api.Ptr(api.ScoreProbability),
		}},
	}
}
