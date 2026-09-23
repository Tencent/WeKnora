// Package providers registers Jina AI's embedding and rerank APIs.
//
// Facts (https://jina.ai/embeddings and https://jina.ai/reranker):
//   - both endpoints live under https://api.jina.ai/v1 and authenticate with
//     `Authorization: Bearer`;
//   - /v1/embeddings matches the input and output JSON schema of OpenAI's
//     text-embedding-3-large (it is a superset: `task`, `normalized`,
//     `embedding_type` and `dimensions` are Jina-only additions);
//     /v1/rerank takes the Cohere-style {model, query, documents, top_n}
//     body;
//   - the v5 generation is a family of four rather than one id:
//     omni (multimodal) and text, each in small (1024 dims, 32K tokens) and
//     nano (768 dims, 8K tokens). jina-embeddings-v4 still defaults to 2048
//     dimensions over 32K tokens and v3 to 1024 over 8K; v4 supports
//     Matryoshka truncation down to 128 through `dimensions`;
//   - jina-reranker-v3.5 and v3 take 131K tokens and are the current
//     recommendations; m0 (10K) and v2-base-multilingual (1K) remain listed;
//   - there is no chat model, so ModelTypes lists embedding and rerank only.
//
// Unverified: api.jina.ai does not serve a fetchable OpenAPI document, so
// the OpenAI-schema compatibility claim rests on the wording of the
// embeddings FAQ rather than a spec.
package providers

import (
	_ "embed"

	"github.com/Tencent/WeKnora/internal/models/api"
	"github.com/Tencent/WeKnora/internal/types"
)

//go:embed assets/jina.svg
var jinaIcon []byte

// JinaID is the provider identifier stored on model rows.
const JinaID = "jina"

// JinaBaseURL serves embeddings and rerank.
const JinaBaseURL = "https://api.jina.ai/v1"

func newJinaProvider() *Definition {
	return &Definition{
		ID:           JinaID,
		Name:         "Jina AI",
		Names:        map[string]string{"zh-CN": "Jina"},
		Description:  "jina-embeddings-v5-text-small, jina-embeddings-v4, jina-reranker-v3.5, jina-reranker-m0, etc.",
		Website:      "https://jina.ai",
		Icon:         jinaIcon,
		API:          api.APIOpenAICompletions,
		Order:        50,
		RequiresAuth: true,
		Auth:         AuthBearer,
		URLPatterns:  []string{"api.jina.ai"},
		DefaultBaseURLs: map[types.ModelType]string{
			types.ModelTypeEmbedding: JinaBaseURL,
			types.ModelTypeRerank:    JinaBaseURL,
		},
		ModelTypes: []types.ModelType{
			types.ModelTypeEmbedding,
			types.ModelTypeRerank,
		},
		Compat: VendorCompat{
			Embeddings: api.EmbeddingsCompat{
				// https://jina.ai/embeddings/: model, input, an optional task,
				// dimensions, embedding_type, normalized, late_chunking and a
				// boolean truncate that defaults to false (an over-long input is
				// an error). truncate: true is what this vendor has always been
				// sent. task is deliberately not declared: the adapter it selects
				// changes the document vectors, so turning it on would leave every
				// existing index half in one space and half in another
				// (Tencent/WeKnora#1401).
				DimensionsField: api.Ptr("dimensions"),
				TruncateField:   api.Ptr("truncate"),
				TruncateValue:   api.Ptr("true"),
			},
			Rerank: api.RerankCompat{
				// return_documents echoes the text back. Results are matched
				// by index, so this is not needed to map them; it is kept
				// because it is what this vendor has always been sent.
				SendReturnDocs: api.Ptr(true),
			},
		},
	}
}
