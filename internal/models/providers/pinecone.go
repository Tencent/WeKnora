// Package providers registers Pinecone's standalone hosted rerank endpoint.
// https://docs.pinecone.io/guides/search/rerank-results
package providers

import (
	_ "embed"

	"github.com/Tencent/WeKnora/internal/models/api"
	"github.com/Tencent/WeKnora/internal/types"
)

//go:embed assets/pinecone.svg
var pineconeIcon []byte

// PineconeID identifies the Pinecone provider in the model catalog.
const PineconeID = "pinecone"

// PineconeRerankBaseURL is the default Pinecone Inference API endpoint.
const PineconeRerankBaseURL = "https://api.pinecone.io"

func newPineconeProvider() *Definition {
	return &Definition{
		ID: PineconeID, Name: "Pinecone",
		Description: "Pinecone Inference standalone reranking (bge-reranker-v2-m3, pinecone-rerank-v0)",
		Website:     "https://www.pinecone.io", Icon: pineconeIcon,
		API: api.APIOpenAICompletions, RerankAPI: api.RerankPinecone,
		Order: 56, RequiresAuth: true, Auth: AuthAPIKeyHeader,
		URLPatterns:     []string{"api.pinecone.io"},
		DefaultBaseURLs: map[types.ModelType]string{types.ModelTypeRerank: PineconeRerankBaseURL},
		ModelTypes:      []types.ModelType{types.ModelTypeRerank},
		Headers:         map[string]string{"X-Pinecone-Api-Version": "2026-07"},
	}
}
