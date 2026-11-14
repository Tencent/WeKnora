// Package providers registers Voyage AI's embedding and rerank APIs.
//
// Facts (https://docs.voyageai.com):
//   - all endpoints live under https://api.voyageai.com/v1 and authenticate
//     with `Authorization: Bearer`;
//   - /embeddings takes `input` (at most 1,000 texts), `input_type` (document
//     or query) and `output_dimension` (2048, 1024, 512 or 256 on the
//     voyage-4 family, voyage-code-4 and the 3 / 3.5 generation; voyage-finance-2,
//     voyage-law-2 and voyage-multimodal-3 accept 1024 only);
//   - /contextualizedembeddings serves voyage-context-4 and voyage-context-3;
//   - /multimodalembeddings serves voyage-multimodal-3.5 and voyage-multimodal-3
//     and takes images as base64 data URLs (png, jpeg, webp, gif; 20 MB);
//   - /rerank takes `top_k` and `return_documents` and answers under `data`;
//   - a failure is {"detail": "<message>"}.
//
// Voyage states per-request token ceilings (120K, 320K or 1M summed over the
// inputs, by model). Counting those needs Voyage's own tokenizer, which has no
// Go implementation, so nothing here estimates tokens: a batch is sent as the
// caller built it, split only at the documented 1,000 inputs per request.
//
// Voyage has no chat model, so ModelTypes lists embedding and rerank only. A
// model named by the row decides the protocol: voyage-context-* and
// voyage-multimodal-* resolve to their own endpoints through their catalog
// entries, every other name to /embeddings.
package providers

import (
	"github.com/Tencent/WeKnora/internal/models/api"
	"github.com/Tencent/WeKnora/internal/types"
)

// VoyageID is the provider identifier stored on model rows.
const VoyageID = "voyage"

// VoyageBaseURL serves embeddings, contextualized and multimodal embeddings
// and rerank.
const VoyageBaseURL = "https://api.voyageai.com/v1"

func newVoyageProvider() *Definition {
	return &Definition{
		ID:           VoyageID,
		Name:         "Voyage AI",
		Description:  "voyage-4-large, voyage-context-4, voyage-multimodal-3.5, rerank-3, etc.",
		Website:      "https://www.voyageai.com",
		Icon:         genericIcon,
		API:          api.APIOpenAICompletions,
		EmbeddingAPI: api.EmbeddingVoyage,
		RerankAPI:    api.RerankVoyage,
		Order:        58,
		RequiresAuth: true,
		Auth:         AuthBearer,
		URLPatterns:  []string{"api.voyageai.com"},
		DefaultBaseURLs: map[types.ModelType]string{
			types.ModelTypeEmbedding: VoyageBaseURL,
			types.ModelTypeRerank:    VoyageBaseURL,
		},
		ModelTypes: []types.ModelType{
			types.ModelTypeEmbedding,
			types.ModelTypeRerank,
		},
		Compat: VendorCompat{
			Embeddings: api.EmbeddingsCompat{
				// input_type distinguishes the two sides of a retrieval pair;
				// its values are the neutral ones. output_dimension narrows
				// the vector (Matryoshka) and is sent only when the row opted
				// in. The list ceiling is 1,000 inputs for every endpoint.
				InputTypeField:  api.Ptr("input_type"),
				DimensionsField: api.Ptr("output_dimension"),
				MaxBatchSize:    api.Ptr(1000),
			},
			Rerank: api.RerankCompat{
				// "The number of documents cannot exceed 1,000."
				// return_documents: true is what this vendor is asked for;
				// results are matched by index either way.
				MaxDocuments:   api.Ptr(1000),
				SendReturnDocs: api.Ptr(true),
			},
		},
	}
}
