// Package pineconerank implements Pinecone Inference's standalone rerank API.
// https://docs.pinecone.io/guides/search/rerank-results
package pineconerank

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Tencent/WeKnora/internal/models/api"
)

// Config holds the Pinecone endpoint and rerank request settings.
type Config struct {
	Endpoint api.Endpoint
	Settings api.RerankSettings
}

// Client calls Pinecone's standalone rerank API.
type Client struct{ cfg Config }

// New creates a Pinecone rerank client.
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

type document struct {
	Text string `json:"text"`
}

type request struct {
	Model           string         `json:"model"`
	Query           string         `json:"query"`
	Documents       []document     `json:"documents"`
	TopN            int            `json:"top_n"`
	RankFields      []string       `json:"rank_fields"`
	ReturnDocuments bool           `json:"return_documents"`
	Parameters      map[string]any `json:"parameters,omitempty"`
}

// BuildRequestBody constructs the native Pinecone body. Asking for every
// candidate and suppressing echoed documents preserves WeKnora's index mapping
// while avoiding unnecessary response bytes.
func (c *Client) BuildRequestBody(query string, documents []string) (map[string]any, error) {
	docs := make([]document, len(documents))
	for i, text := range documents {
		docs[i] = document{Text: text}
	}
	body := request{
		Model: c.cfg.Endpoint.Model, Query: query, Documents: docs,
		TopN: len(documents), RankFields: []string{"text"},
	}
	if c.cfg.Settings.Truncate != "" {
		body.Parameters = map[string]any{"truncate": c.cfg.Settings.Truncate}
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	for key, value := range c.cfg.Settings.ExtraBody {
		if _, exists := out[key]; !exists {
			out[key] = value
		}
	}
	return out, nil
}

type response struct {
	Data *[]result `json:"data"`
}

type result struct {
	Index *int     `json:"index"`
	Score *float64 `json:"score"`
}

// Rerank returns scores for the supplied documents in Pinecone's ranked order.
func (c *Client) Rerank(
	ctx context.Context, query string, documents []string,
) ([]api.RerankResult, error) {
	body, err := c.BuildRequestBody(query, documents)
	if err != nil {
		return nil, err
	}
	var decoded response
	if err := c.cfg.Endpoint.PostJSON(ctx, c.url(), body, &decoded); err != nil {
		return nil, err
	}
	if decoded.Data == nil {
		return nil, fmt.Errorf("pinecone rerank response has no data array")
	}
	if len(*decoded.Data) != len(documents) {
		return nil, fmt.Errorf(
			"pinecone rerank returned %d scores for %d documents", len(*decoded.Data), len(documents),
		)
	}
	out := make([]api.RerankResult, 0, len(*decoded.Data))
	seen := make([]bool, len(documents))
	for _, item := range *decoded.Data {
		if item.Index == nil || item.Score == nil {
			return nil, fmt.Errorf("pinecone rerank result is missing index or score")
		}
		if *item.Index < 0 || *item.Index >= len(documents) {
			return nil, fmt.Errorf("rerank index %d out of range for %d documents", *item.Index, len(documents))
		}
		if seen[*item.Index] {
			return nil, fmt.Errorf("pinecone rerank returned duplicate index %d", *item.Index)
		}
		seen[*item.Index] = true
		out = append(out, api.RerankResult{
			Index: *item.Index, Score: *item.Score, Text: documents[*item.Index],
		})
	}
	return out, nil
}
