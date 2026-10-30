// Package pairwiserank implements reranking with parallel queries/documents
// arrays and a positional scores array. The model is selected by the request
// URL, rather than a field in the JSON body.
package pairwiserank

import (
	"context"
	"fmt"
	"math"
	"net/url"
	"strings"

	"github.com/Tencent/WeKnora/internal/models/api"
)

// Config supplies the endpoint and wire-level rerank settings.
type Config struct {
	Endpoint api.Endpoint
	Settings api.RerankSettings
}

// Client scores one query/document pair per input position.
type Client struct{ cfg Config }

// New creates a pairwise rerank client.
func New(cfg Config) *Client { return &Client{cfg: cfg} }

// url appends the model to the inference base URL unless the endpoint URL or
// request path is explicitly configured. A pasted full endpoint is not doubled.
func (c *Client) url() string {
	if c.cfg.Endpoint.URL != "" {
		return c.cfg.Endpoint.Resolve("")
	}
	path := c.cfg.Settings.Path
	if path == "" {
		parts := strings.Split(strings.Trim(c.cfg.Endpoint.Model, "/"), "/")
		for i, part := range parts {
			parts[i] = url.PathEscape(part)
		}
		path = "/" + strings.Join(parts, "/")
	}
	if strings.HasSuffix(strings.TrimRight(c.cfg.Endpoint.BaseURL, "/"), path) {
		path = ""
	}
	return c.cfg.Endpoint.Resolve(path)
}

// BuildRequestBody repeats the query for every document. The two arrays have
// equal length, so score i always belongs to document i. ExtraBody may add
// explicit extensions but cannot replace either array. Model, top_n and
// truncation settings from other rerank dialects are not added to this body.
func (c *Client) BuildRequestBody(query string, documents []string) map[string]any {
	queries := make([]string, len(documents))
	docs := make([]string, len(documents))
	for i, text := range documents {
		queries[i] = query
		docs[i] = text
	}
	body := map[string]any{"queries": queries, "documents": docs}
	for key, value := range c.cfg.Settings.ExtraBody {
		if _, exists := body[key]; !exists {
			body[key] = value
		}
	}
	return body
}

type response struct {
	Scores *[]*float64 `json:"scores"`
}

// Rerank returns every score in input order, with the original document text.
// Scores are preserved as received; interpreting their scale belongs to the
// caller's resolved settings. Empty inputs require no inference request.
func (c *Client) Rerank(ctx context.Context, query string, documents []string) ([]api.RerankResult, error) {
	if len(documents) == 0 {
		return []api.RerankResult{}, nil
	}
	var decoded response
	if err := c.cfg.Endpoint.PostJSON(ctx, c.url(), c.BuildRequestBody(query, documents), &decoded); err != nil {
		return nil, err
	}
	return decoded.results(documents)
}

func (r response) results(documents []string) ([]api.RerankResult, error) {
	if r.Scores == nil {
		return nil, fmt.Errorf("pairwise rerank response has no scores array")
	}
	if len(*r.Scores) != len(documents) {
		return nil, fmt.Errorf("pairwise rerank returned %d scores for %d documents", len(*r.Scores), len(documents))
	}
	out := make([]api.RerankResult, len(documents))
	for i, score := range *r.Scores {
		if score == nil {
			return nil, fmt.Errorf("pairwise rerank score is missing for index %d", i)
		}
		if math.IsNaN(*score) || math.IsInf(*score, 0) {
			return nil, fmt.Errorf("pairwise rerank score is non-finite for index %d", i)
		}
		out[i] = api.RerankResult{Index: i, Score: *score, Text: documents[i]}
	}
	return out, nil
}
