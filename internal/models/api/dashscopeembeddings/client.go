// Package dashscopeembeddings implements Alibaba Model Studio's native
// multimodal embedding shape: input.contents, parameters.dimension, and
// results under output.embeddings.
//
// Only the multimodal models use it. DashScope's text embeddings are served
// on the OpenAI-compatible endpoint, which is why the vendor's default
// protocol is the OpenAI one and the multimodal entries override it.
//
// https://help.aliyun.com/zh/model-studio/multimodal-embedding-api-reference
package dashscopeembeddings

import (
	"context"
	"fmt"

	"github.com/Tencent/WeKnora/internal/models/api"
)

// Config is everything the client needs, already resolved by the api.
type Config struct {
	Endpoint   api.Endpoint
	Settings   api.EmbeddingsSettings
	Dimensions int
	Retry      api.RetryPolicy
}

// Client talks DashScope's native multimodal embedding to one endpoint.
type Client struct {
	cfg Config
}

// New builds a client.
func New(cfg Config) *Client { return &Client{cfg: cfg} }

type response struct {
	Output struct {
		Embeddings []struct {
			// The position field is `index`. DashScope's *text* embedding API
			// one page over calls it `text_index`, and decoding that name
			// here yields 0 for every element, collapsing a whole batch onto
			// slot 0 (Tencent/WeKnora#3484).
			Index     int       `json:"index"`
			Embedding []float32 `json:"embedding"`
			Type      string    `json:"type"`
		} `json:"embeddings"`
	} `json:"output"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

// BuildRequestBody is the golden-test entry point.
func (c *Client) BuildRequestBody(texts []string, _ api.EmbedInputType) map[string]any {
	contents := make([]any, 0, len(texts))
	for _, text := range texts {
		contents = append(contents, map[string]any{"text": text})
	}
	body := map[string]any{
		"model": c.cfg.Endpoint.Model,
		"input": map[string]any{"contents": contents},
	}
	// The width lives under parameters, not at the top level, and the field
	// is singular: `dimension`.
	if c.cfg.Settings.DimensionsField != "" && c.cfg.Dimensions > 0 {
		body["parameters"] = map[string]any{c.cfg.Settings.DimensionsField: c.cfg.Dimensions}
	}
	for k, v := range c.cfg.Settings.ExtraBody {
		if _, exists := body[k]; !exists {
			body[k] = v
		}
	}
	return body
}

// Embed vectorizes one batch.
func (c *Client) Embed(
	ctx context.Context, texts []string, kind api.EmbedInputType,
) ([][]float32, error) {
	var decoded response
	url := c.cfg.Endpoint.Resolve(c.cfg.Settings.Path)
	err := c.cfg.Endpoint.PostJSONWithRetry(
		ctx, url, c.BuildRequestBody(texts, kind), &decoded, c.cfg.Retry, "embedding",
	)
	if err != nil {
		return nil, err
	}
	// DashScope reports some failures in the body with a 200.
	if decoded.Code != "" {
		return nil, fmt.Errorf("DashScope embedding error %s: %s", decoded.Code, decoded.Message)
	}
	return api.PlaceEmbeddings(len(texts), len(decoded.Output.Embeddings), func(i int) (int, []float32) {
		return decoded.Output.Embeddings[i].Index, decoded.Output.Embeddings[i].Embedding
	})
}
