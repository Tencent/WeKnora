// Package nimrerank implements NVIDIA NIM's retrieval reranking shape: a
// query object, a passages array, and rankings carrying a logit.
//
// Two things separate it from the Cohere dialect and both matter to callers.
// The score is an unbounded log-odds value that is routinely negative, not a
// 0..1 relevance — api.RerankSettings.ScoreScale says so. And `truncate`
// defaults to "NONE", which means an over-long passage fails the request
// instead of being cut, so the vendor sets it explicitly.
//
// https://docs.api.nvidia.com/nim/reference/nvidia-llama-3_2-nv-rerankqa-1b-v2-infer
package nimrerank

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/Tencent/WeKnora/internal/models/api"
)

// Config is everything the client needs, already resolved by the api.
type Config struct {
	Endpoint api.Endpoint
	Settings api.RerankSettings
}

// Client talks NIM reranking to one endpoint.
type Client struct {
	cfg Config
}

// New builds a client.
func New(cfg Config) *Client { return &Client{cfg: cfg} }

type text struct {
	Text string `json:"text"`
}

type request struct {
	Model    string `json:"model"`
	Query    text   `json:"query"`
	Passages []text `json:"passages"`
	Truncate string `json:"truncate,omitempty"`
}

type response struct {
	Rankings []struct {
		Index int     `json:"index"`
		Logit float64 `json:"logit"`
	} `json:"rankings"`
}

// BuildRequestBody is the golden-test entry point.
func (c *Client) BuildRequestBody(query string, documents []string) (map[string]any, error) {
	passages := make([]text, len(documents))
	for i := range documents {
		passages[i] = text{Text: documents[i]}
	}
	body := request{
		Model:    c.cfg.Endpoint.Model,
		Query:    text{Text: query},
		Passages: passages,
		Truncate: c.cfg.Settings.Truncate,
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	for k, v := range c.cfg.Settings.ExtraBody {
		if _, exists := out[k]; !exists {
			out[k] = v
		}
	}
	return out, nil
}

// Rerank scores documents against the query. The returned scores are logits;
// see the package comment.
func (c *Client) Rerank(ctx context.Context, query string, documents []string) ([]api.RerankResult, error) {
	body, err := c.BuildRequestBody(query, documents)
	if err != nil {
		return nil, err
	}
	var decoded response
	url := c.cfg.Endpoint.Resolve(c.cfg.Settings.Path)
	if err := c.cfg.Endpoint.PostJSON(ctx, url, body, &decoded); err != nil {
		return nil, err
	}
	out := make([]api.RerankResult, 0, len(decoded.Rankings))
	for _, item := range decoded.Rankings {
		if item.Index < 0 || item.Index >= len(documents) {
			return nil, fmt.Errorf("rerank index %d out of range for %d documents", item.Index, len(documents))
		}
		out = append(out, api.RerankResult{
			Index: item.Index,
			Score: item.Logit,
			Text:  documents[item.Index],
		})
	}
	return out, nil
}
