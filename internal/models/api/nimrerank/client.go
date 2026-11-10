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

// imagePassage is a passage that is an image. The VLM reranking reference:
// images "must be base64 data URLs, such as data:image/jpeg;base64,...",
// and "For image-only passages, omit text. Do not set text to an empty
// string." Text passages keep their own type, so their shape never changes.
type imagePassage struct {
	Image string `json:"image"`
}

// BuildImageRequestBody is the golden-test entry point for image passages.
func (c *Client) BuildImageRequestBody(query string, images []api.EmbedImage) (map[string]any, error) {
	passages := make([]imagePassage, len(images))
	for i, img := range images {
		passages[i] = imagePassage{Image: img.DataURI()}
	}
	body, err := c.BuildRequestBody(query, nil)
	if err != nil {
		return nil, err
	}
	body["passages"] = passages
	return roundTrip(body)
}

// AcceptsImages is true: image passages are part of the NIM ranking schema.
// Only the VL models score them, which their catalog input says.
func (c *Client) AcceptsImages() bool { return true }

// Rerank scores documents against the query. The returned scores are logits;
// see the package comment.
func (c *Client) Rerank(ctx context.Context, query string, documents []string) ([]api.RerankResult, error) {
	body, err := c.BuildRequestBody(query, documents)
	if err != nil {
		return nil, err
	}
	out, err := c.post(ctx, body, len(documents))
	for i := range out {
		out[i].Text = documents[out[i].Index]
	}
	return out, err
}

// RerankImages scores image passages against the query; indices refer to
// images.
func (c *Client) RerankImages(ctx context.Context, query string, images []api.EmbedImage) ([]api.RerankResult, error) {
	body, err := c.BuildImageRequestBody(query, images)
	if err != nil {
		return nil, err
	}
	return c.post(ctx, body, len(images))
}

func (c *Client) post(ctx context.Context, body map[string]any, count int) ([]api.RerankResult, error) {
	var decoded response
	url := c.cfg.Endpoint.Resolve(c.cfg.Settings.Path)
	if err := c.cfg.Endpoint.PostJSON(ctx, url, body, &decoded); err != nil {
		return nil, err
	}
	out := make([]api.RerankResult, 0, len(decoded.Rankings))
	for _, item := range decoded.Rankings {
		if item.Index < 0 || item.Index >= count {
			return nil, fmt.Errorf("rerank index %d out of range for %d documents", item.Index, count)
		}
		out = append(out, api.RerankResult{Index: item.Index, Score: item.Logit})
	}
	return out, nil
}

// roundTrip renders a body through JSON, so golden tests compare plain maps.
func roundTrip(body map[string]any) (map[string]any, error) {
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	return out, nil
}
