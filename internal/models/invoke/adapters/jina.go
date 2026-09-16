package adapters

// jina.go — the jina vendor adapter. WeKnora serves no jina chat shard (v1
// routed jina to the embedding/rerank factories only), so this is not an
// openai composite: both facets are bespoke request shapes over the shared
// response envelopes (data[] embeddings, results[] rerank — the parsers live
// in the facet files).

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/Tencent/WeKnora/internal/models/invoke"
)

// --- embedding: v1 jina.go field order {model, input, truncate, dimensions}
// — different from the merged openai struct, hence its own type. ---

type jinaEmbedRequest struct {
	Model      string   `json:"model"`
	Input      []string `json:"input"`
	Truncate   bool     `json:"truncate,omitempty"`
	Dimensions int      `json:"dimensions,omitempty"`
}

func buildJinaEmbedding(ep invoke.Endpoint, model string, opts *invoke.EmbeddingOptions) (*invoke.Request, error) {
	if model == "" {
		return nil, fmt.Errorf("model name is required")
	}
	base := ep.BaseURL
	if base == "" {
		base = "https://api.jina.ai/v1"
	}
	body := jinaEmbedRequest{Model: model, Input: opts.Inputs, Truncate: true}
	if opts.SupportsDimensionOverride && opts.Dimensions > 0 {
		body.Dimensions = opts.Dimensions
	}
	data, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	header := http.Header{}
	header.Set("Content-Type", "application/json")
	header.Set("Authorization", "Bearer "+ep.Credentials.APIKey)
	return &invoke.Request{
		Method:  http.MethodPost,
		URL:     base + "/embeddings",
		Header:  header,
		Body:    data,
		Timeout: embeddingRequestTimeout,
	}, nil
}

// jinaEmbeddingAdapter serves the embedding-only vendor (no chat shard — v1
// routed jina to the embedding factory only).
type jinaEmbeddingAdapter struct {
	name invoke.ProviderName
}

var _ invoke.EmbeddingAdapter = (*jinaEmbeddingAdapter)(nil)

// Provider returns the canonical provider name.
func (a *jinaEmbeddingAdapter) Provider() string { return string(a.name) }

// Capabilities reports the embedding-only shard.
func (a *jinaEmbeddingAdapter) Capabilities() invoke.Capabilities {
	return invoke.Capabilities{Embedding: embeddingCapsFor(a.name)}
}

// BuildEmbeddingRequest builds the jina wire request.
func (a *jinaEmbeddingAdapter) BuildEmbeddingRequest(
	ep invoke.Endpoint, model string, opts *invoke.EmbeddingOptions,
) (*invoke.Request, error) {
	return buildJinaEmbedding(ep, model, opts)
}

// ParseEmbeddingResponse parses the shared openai data[] shape.
func (a *jinaEmbeddingAdapter) ParseEmbeddingResponse(
	status int, header http.Header, body []byte,
) (*invoke.EmbeddingResponse, error) {
	return parseOpenAIEmbeddingResponse(status, header, body)
}

// --- rerank ---

type jinaRerankRequest struct {
	Model           string   `json:"model"`
	Query           string   `json:"query"`
	Documents       []string `json:"documents"`
	TopN            int      `json:"top_n,omitempty"`
	ReturnDocuments bool     `json:"return_documents,omitempty"`
}

func buildJinaRerank(ep invoke.Endpoint, model string, opts *invoke.RerankOptions) (*invoke.Request, error) {
	base := ep.BaseURL
	if base == "" {
		base = "https://api.jina.ai/v1"
	}
	body := jinaRerankRequest{
		Model:           model,
		Query:           opts.Query,
		Documents:       opts.Documents,
		ReturnDocuments: true, // v1 constant; top_n never set (0 → omitted)
	}
	return buildRerankRequestShared(base, ep, body, "/rerank")
}

// jinaRerankAdapter adds the rerank facet to the embedding-only jina adapter.
type jinaRerankAdapter struct {
	jinaEmbeddingAdapter
	rspec rerankSpec
}

var _ invoke.RerankAdapter = (*jinaRerankAdapter)(nil)

// Capabilities unions the embedding and rerank shards.
func (a *jinaRerankAdapter) Capabilities() invoke.Capabilities {
	return invoke.Capabilities{
		Embedding: embeddingCapsFor(a.name),
		Rerank:    rerankCapsFor(a.name),
	}
}

// BuildRerankRequest dispatches to the vendor build.
func (a *jinaRerankAdapter) BuildRerankRequest(
	ep invoke.Endpoint, model string, opts *invoke.RerankOptions,
) (*invoke.Request, error) {
	return a.rspec.build(ep, model, opts)
}

// ParseRerankResponse dispatches to the vendor parse.
func (a *jinaRerankAdapter) ParseRerankResponse(
	status int, header http.Header, body []byte,
) (*invoke.RerankResponse, error) {
	return a.rspec.parse(status, header, body)
}
