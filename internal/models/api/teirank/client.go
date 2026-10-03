// Package teirank implements the native Text Embeddings Inference rerank
// protocol. It sends {query, texts} to /rerank and reads a bare array of
// {index, score}; the deployed model is selected by the TEI server, not by
// a model field in each request.
package teirank

import (
	"context"
	"fmt"
	"strings"

	"github.com/Tencent/WeKnora/internal/models/api"
)

// Config supplies the endpoint and wire-level settings for a TEI reranker.
type Config struct {
	Endpoint api.Endpoint
	Settings api.RerankSettings
}

// Client sends rerank requests to a Text Embeddings Inference server.
type Client struct{ cfg Config }

// New creates a TEI rerank client for the supplied endpoint.
func New(cfg Config) *Client { return &Client{cfg: cfg} }

func (c *Client) url() string {
	if c.cfg.Endpoint.URL != "" {
		return c.cfg.Endpoint.Resolve("")
	}
	path := c.cfg.Settings.Path
	if path == "" {
		path = "/rerank"
	}
	if strings.HasSuffix(strings.TrimRight(c.cfg.Endpoint.BaseURL, "/"), path) {
		path = ""
	}
	return c.cfg.Endpoint.Resolve(path)
}

type rank struct {
	Index *int     `json:"index"`
	Score *float64 `json:"score"`
	Text  string   `json:"text"`
}

// BuildRequestBody returns the native TEI request. raw_scores is explicit:
// false makes a single-class reranker's logits comparable with WeKnora's
// 0..1 relevance threshold. The model name is configured when TEI starts.
func (c *Client) BuildRequestBody(query string, documents []string) map[string]any {
	return map[string]any{
		"query":      query,
		"texts":      documents,
		"raw_scores": false,
	}
}

// Rerank scores documents with TEI and preserves their original indexes.
func (c *Client) Rerank(ctx context.Context, query string, documents []string) ([]api.RerankResult, error) {
	var decoded []rank
	if err := c.cfg.Endpoint.PostJSON(ctx, c.url(), c.BuildRequestBody(query, documents), &decoded); err != nil {
		return nil, err
	}
	if len(decoded) != len(documents) {
		return nil, fmt.Errorf("TEI returned %d ranks for %d documents", len(decoded), len(documents))
	}
	out := make([]api.RerankResult, 0, len(decoded))
	seen := make([]bool, len(documents))
	for _, item := range decoded {
		if item.Index == nil || *item.Index < 0 || *item.Index >= len(documents) {
			return nil, fmt.Errorf("TEI rerank index is missing or out of range for %d documents", len(documents))
		}
		if seen[*item.Index] {
			return nil, fmt.Errorf("TEI returned duplicate rerank index %d", *item.Index)
		}
		if item.Score == nil {
			return nil, fmt.Errorf("TEI rerank score is missing for index %d", *item.Index)
		}
		seen[*item.Index] = true
		out = append(out, api.RerankResult{Index: *item.Index, Score: *item.Score, Text: item.Text})
	}
	return out, nil
}
