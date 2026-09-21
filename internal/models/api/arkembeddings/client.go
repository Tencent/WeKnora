// Package arkembeddings implements Volcengine Ark's multimodal embedding
// shape: an input array of typed parts answering a single `data` object.
//
// The response is one fused vector for the whole input, not one per element —
// the reference's own example returns
//
//	"data": { "embedding": [...], "object": "embedding" }
//
// for a request carrying a video, an image and a text. So a batch of N texts
// is N requests, which the vendor declares as a batch size of one rather than
// this package looping.
//
// It is the only embedding API Ark still documents. Its retired text
// endpoint (/api/v3/embeddings) was the plain OpenAI shape, which is why rows
// naming a text model resolve to that protocol instead.
//
// https://docs.volcengine.com/docs/ark/multimodal-vectorization-api
package arkembeddings

import (
	"context"
	"fmt"

	"github.com/Tencent/WeKnora/internal/models/api"
	"github.com/Tencent/WeKnora/internal/models/catalog"
)

// Config is everything the client needs, already resolved by the catalog.
type Config struct {
	Endpoint   api.Endpoint
	Settings   catalog.EmbeddingsSettings
	Dimensions int
	Retry      api.RetryPolicy
}

// Client talks Ark's multimodal embedding to one endpoint.
type Client struct {
	cfg Config
}

// New builds a client.
func New(cfg Config) *Client { return &Client{cfg: cfg} }

type response struct {
	Data struct {
		Embedding []float32 `json:"embedding"`
		Object    string    `json:"object"`
	} `json:"data"`
	Error *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// BuildRequestBody is the golden-test entry point.
func (c *Client) BuildRequestBody(texts []string, _ api.EmbedInputType) map[string]any {
	input := make([]any, 0, len(texts))
	for _, text := range texts {
		input = append(input, map[string]any{"type": "text", "text": text})
	}
	body := map[string]any{
		"model": c.cfg.Endpoint.Model,
		"input": input,
	}
	if c.cfg.Settings.SendEncodingFormat {
		body["encoding_format"] = "float"
	}
	if c.cfg.Settings.DimensionsField != "" && c.cfg.Dimensions > 0 {
		body[c.cfg.Settings.DimensionsField] = c.cfg.Dimensions
	}
	for k, v := range c.cfg.Settings.ExtraBody {
		if _, exists := body[k]; !exists {
			body[k] = v
		}
	}
	return body
}

// Embed vectorizes one input. The vendor fuses everything it is sent into a
// single vector, so the caller must hand over exactly one text; the shared
// batching layer guarantees that through the declared batch size.
func (c *Client) Embed(
	ctx context.Context, texts []string, kind api.EmbedInputType,
) ([][]float32, error) {
	if len(texts) != 1 {
		return nil, fmt.Errorf(
			"ark embedding fuses its input into one vector, so it takes one text per request, got %d",
			len(texts),
		)
	}
	var decoded response
	url := c.cfg.Endpoint.Resolve(c.cfg.Settings.Path)
	err := c.cfg.Endpoint.PostJSONWithRetry(
		ctx, url, c.BuildRequestBody(texts, kind), &decoded, c.cfg.Retry, "embedding",
	)
	if err != nil {
		return nil, err
	}
	if decoded.Error != nil && decoded.Error.Message != "" {
		return nil, fmt.Errorf("ark embedding error %s: %s", decoded.Error.Code, decoded.Error.Message)
	}
	if len(decoded.Data.Embedding) == 0 {
		return nil, fmt.Errorf("ark embedding returned no vector")
	}
	return [][]float32{decoded.Data.Embedding}, nil
}
