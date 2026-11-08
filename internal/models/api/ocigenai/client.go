package ocigenai

import (
	"context"
	"fmt"

	"github.com/Tencent/WeKnora/internal/models/api"
)

// The inference API version and its two actions used here.
const (
	embedPath  = "/20231130/actions/embedText"
	rerankPath = "/20231130/actions/rerankText"
)

// Config is shared by both clients. Endpoint.Auth must be the signer from
// NewAuth; CompartmentID is required by every on-demand call.
type Config struct {
	Endpoint      api.Endpoint
	CompartmentID string
	// Dimensions, when positive, asks for a narrower vector
	// (outputDimensions; Embed 4 takes 256, 512, 1024 or 1536).
	Dimensions int
	Retry      api.RetryPolicy
}

type servingMode struct {
	ServingType string `json:"servingType"`
	ModelID     string `json:"modelId"`
}

func (c Config) onDemand() servingMode {
	return servingMode{ServingType: "ON_DEMAND", ModelID: c.Endpoint.Model}
}

// Embedder calls embedText.
type Embedder struct{ cfg Config }

// NewEmbedder returns an embedText client.
func NewEmbedder(cfg Config) *Embedder { return &Embedder{cfg: cfg} }

type embedRequest struct {
	Inputs           []string    `json:"inputs"`
	ServingMode      servingMode `json:"servingMode"`
	CompartmentID    string      `json:"compartmentId"`
	InputType        string      `json:"inputType"`
	Truncate         string      `json:"truncate"`
	OutputDimensions int         `json:"outputDimensions,omitempty"`
}

type embedResponse struct {
	Embeddings [][]float32 `json:"embeddings"`
}

// body is the embedText body. Over-long inputs are cut at the
// end (the API default, NONE, fails the whole batch instead).
func (e *Embedder) body(texts []string, kind api.EmbedInputType) embedRequest {
	inputType := "SEARCH_DOCUMENT"
	if kind == api.EmbedQuery {
		inputType = "SEARCH_QUERY"
	}
	return embedRequest{
		Inputs:           texts,
		ServingMode:      e.cfg.onDemand(),
		CompartmentID:    e.cfg.CompartmentID,
		InputType:        inputType,
		Truncate:         "END",
		OutputDimensions: max(e.cfg.Dimensions, 0),
	}
}

// Embed implements api.Embedder. The response lists vectors in input order.
func (e *Embedder) Embed(ctx context.Context, texts []string, kind api.EmbedInputType) ([][]float32, error) {
	var out embedResponse
	err := e.cfg.Endpoint.PostJSONWithRetry(ctx, e.cfg.Endpoint.Resolve(embedPath),
		e.body(texts, kind), &out, e.cfg.Retry, "embedding")
	if err != nil {
		return nil, err
	}
	if len(out.Embeddings) != len(texts) {
		return nil, fmt.Errorf("OCI Generative AI returned %d embeddings for %d inputs",
			len(out.Embeddings), len(texts))
	}
	return api.PlaceEmbeddings(len(texts), len(out.Embeddings), func(i int) (int, []float32) {
		return i, out.Embeddings[i]
	})
}

// Reranker calls rerankText.
type Reranker struct{ cfg Config }

// NewReranker returns a rerankText client.
func NewReranker(cfg Config) *Reranker { return &Reranker{cfg: cfg} }

type rerankRequest struct {
	Input         string      `json:"input"`
	Documents     []string    `json:"documents"`
	ServingMode   servingMode `json:"servingMode"`
	CompartmentID string      `json:"compartmentId"`
	TopN          int         `json:"topN"`
	IsEcho        bool        `json:"isEcho"`
}

type rerankResponse struct {
	DocumentRanks []struct {
		Index          *int     `json:"index"`
		RelevanceScore *float64 `json:"relevanceScore"`
	} `json:"documentRanks"`
}

// body is the rerankText body. Every document is scored
// (topN = all) and none is echoed back: callers index their own slice.
func (r *Reranker) body(query string, documents []string) rerankRequest {
	return rerankRequest{
		Input:         query,
		Documents:     documents,
		ServingMode:   r.cfg.onDemand(),
		CompartmentID: r.cfg.CompartmentID,
		TopN:          len(documents),
	}
}

// Rerank implements api.Reranker. Scores are 0..1 relevance probabilities.
func (r *Reranker) Rerank(ctx context.Context, query string, documents []string) ([]api.RerankResult, error) {
	var out rerankResponse
	if err := r.cfg.Endpoint.PostJSON(ctx, r.cfg.Endpoint.Resolve(rerankPath),
		r.body(query, documents), &out); err != nil {
		return nil, err
	}
	results := make([]api.RerankResult, 0, len(out.DocumentRanks))
	for _, rank := range out.DocumentRanks {
		if rank.Index == nil || rank.RelevanceScore == nil {
			return nil, fmt.Errorf("OCI Generative AI rerank: result without index or score")
		}
		if *rank.Index < 0 || *rank.Index >= len(documents) {
			return nil, fmt.Errorf("rerank index %d out of range for %d documents", *rank.Index, len(documents))
		}
		results = append(results, api.RerankResult{Index: *rank.Index, Score: *rank.RelevanceScore})
	}
	return results, nil
}
