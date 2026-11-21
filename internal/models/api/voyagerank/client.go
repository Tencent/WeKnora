// Package voyagerank implements Voyage AI's rerank API: POST {base}/rerank
// with {model, query, documents, top_k, return_documents} answering
// {data: [{index, relevance_score, document}], usage}.
//
// https://docs.voyageai.com/reference/reranker-api
//
// It differs from the Cohere shape in three places that make the Cohere
// client unusable as is: the count is `top_k`, not `top_n`; the results live
// under `data`, not `results`; and failures are {"detail": "..."}. There is
// no official Go SDK for Voyage, so this is a REST client over the shared
// endpoint helpers.
package voyagerank

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Tencent/WeKnora/internal/models/api"
	"github.com/Tencent/WeKnora/internal/models/api/voyageapi"
)

// Config is everything the client needs, already resolved by the api.
type Config struct {
	Endpoint api.Endpoint
	Settings api.RerankSettings
}

// Client talks Voyage's rerank API to one endpoint.
type Client struct{ cfg Config }

// New builds a client.
func New(cfg Config) *Client { return &Client{cfg: cfg} }

const defaultPath = "/rerank"

func (c *Client) url() string {
	if c.cfg.Endpoint.URL != "" {
		return c.cfg.Endpoint.Resolve("")
	}
	path := c.cfg.Settings.Path
	if path == "" {
		path = defaultPath
	}
	if strings.HasSuffix(strings.TrimRight(c.cfg.Endpoint.BaseURL, "/"), path) {
		path = ""
	}
	return c.cfg.Endpoint.Resolve(path)
}

// BuildRequestBody is the golden-test entry point: it returns the exact JSON
// object that would be sent. top_k is the number of documents, which asks for
// every one of them back and is sent only when the model entry or the row
// opted in; omitting it already means "all". return_documents echoes the text
// when the entry asks for it.
func (c *Client) BuildRequestBody(query string, documents []string) map[string]any {
	body := map[string]any{
		"model":     c.cfg.Endpoint.Model,
		"query":     query,
		"documents": documents,
	}
	if c.cfg.Settings.SendTopN {
		body["top_k"] = len(documents)
	}
	if c.cfg.Settings.SendReturnDocs {
		body["return_documents"] = true
	}
	for k, v := range c.cfg.Settings.ExtraBody {
		if _, exists := body[k]; !exists {
			body[k] = v
		}
	}
	return body
}

type response struct {
	Data *[]struct {
		Index          *int     `json:"index"`
		RelevanceScore *float64 `json:"relevance_score"`
		Document       *string  `json:"document"`
	} `json:"data"`
}

// Rerank scores documents against the query. Indices refer to documents; the
// order is Voyage's: descending relevance.
func (c *Client) Rerank(ctx context.Context, query string, documents []string) ([]api.RerankResult, error) {
	var raw json.RawMessage
	err := c.cfg.Endpoint.PostJSON(ctx, c.url(), c.BuildRequestBody(query, documents), &raw)
	if err != nil {
		return nil, voyageapi.WrapError(err)
	}
	var decoded response
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	// A missing array can be an error document returned with a 2xx status;
	// only an explicit array is a ranking.
	if decoded.Data == nil {
		return nil, fmt.Errorf("voyage rerank response has no data array")
	}
	if len(*decoded.Data) == 0 {
		return nil, fmt.Errorf("voyage rerank returned no results for %d documents", len(documents))
	}
	out := make([]api.RerankResult, 0, len(*decoded.Data))
	seen := make([]bool, len(documents))
	for _, item := range *decoded.Data {
		if item.Index == nil || item.RelevanceScore == nil {
			return nil, fmt.Errorf("voyage rerank result is missing index or relevance_score")
		}
		if *item.Index < 0 || *item.Index >= len(documents) {
			return nil, fmt.Errorf("rerank index %d out of range for %d documents", *item.Index, len(documents))
		}
		if seen[*item.Index] {
			return nil, fmt.Errorf("voyage rerank returned index %d twice", *item.Index)
		}
		seen[*item.Index] = true
		result := api.RerankResult{Index: *item.Index, Score: *item.RelevanceScore}
		if item.Document != nil {
			result.Text = *item.Document
		}
		out = append(out, result)
	}
	return out, nil
}
