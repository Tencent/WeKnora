// Package openaiembeddings implements the embedding shape OpenAI defined and
// every compatible gateway copied: POST {base}/embeddings with {model, input}
// answering {data: [{embedding, index}]}.
//
// The vendors that speak it disagree about everything optional, so nothing
// optional is sent unless api.EmbeddingsSettings says the vendor
// documents it:
//
//   - `dimensions` does not exist on NVIDIA NIM or Volcengine's text endpoint;
//   - `encoding_format` is absent from Zhipu's reference;
//   - the parameter that separates a search query from an indexed document is
//     `input_type` on NIM, `task` on Jina and `taskType` on Gemini, and most
//     vendors have none.
//
// This package contains no vendor names.
package openaiembeddings

import (
	"context"
	"fmt"
	"strings"

	"github.com/Tencent/WeKnora/internal/models/api"
)

// Config is everything the client needs, already resolved by the api.
type Config struct {
	Endpoint api.Endpoint
	Settings api.EmbeddingsSettings
	// Dimensions is the width the row asked for; it is sent only when the
	// vendor names a field for it.
	Dimensions int
	Retry      api.RetryPolicy
}

// Client talks the OpenAI embedding shape to one endpoint.
type Client struct {
	cfg Config
}

// New builds a client.
func New(cfg Config) *Client { return &Client{cfg: cfg} }

const defaultPath = "/embeddings"

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

type response struct {
	Data []struct {
		Embedding []float32 `json:"embedding"`
		// Index is a pointer so that an absent field reads as absent rather
		// than as 0. OpenAI, vLLM and TEI send it; some minimal compatible
		// servers answer in order and leave it out, and the pre-catalog
		// client read those positionally. Decoded as a plain int, every
		// element of such a reply would claim slot 0.
		Index *int `json:"index"`
	} `json:"data"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// BuildRequestBody is the golden-test entry point: it returns the exact JSON
// object that would be sent.
func (c *Client) BuildRequestBody(texts []string, kind api.EmbedInputType) map[string]any {
	s := c.cfg.Settings
	body := map[string]any{
		"model": c.cfg.Endpoint.Model,
		"input": texts,
	}
	if s.SendEncodingFormat {
		body["encoding_format"] = "float"
	}
	if s.DimensionsField != "" && c.cfg.Dimensions > 0 {
		body[s.DimensionsField] = c.cfg.Dimensions
	}
	if field := s.InputTypeField; field != "" {
		body[field] = s.InputTypeValue(kind)
	}
	if s.TruncateField != "" {
		if s.TruncateValue == "true" || s.TruncateValue == "false" {
			body[s.TruncateField] = s.TruncateValue == "true"
		} else {
			body[s.TruncateField] = s.TruncateValue
		}
	}
	if s.TruncatePromptTokens > 0 && s.AcceptsTruncatePromptTokens {
		body["truncate_prompt_tokens"] = s.TruncatePromptTokens
	}
	for k, v := range s.ExtraBody {
		if _, exists := body[k]; !exists {
			body[k] = v
		}
	}
	return body
}

// Embed vectorizes one batch, in the order it was given.
func (c *Client) Embed(
	ctx context.Context, texts []string, kind api.EmbedInputType,
) ([][]float32, error) {
	var decoded response
	err := c.cfg.Endpoint.PostJSONWithRetry(
		ctx, c.url(), c.BuildRequestBody(texts, kind), &decoded, c.cfg.Retry, "embedding",
	)
	if err != nil {
		return nil, err
	}
	if decoded.Error != nil && decoded.Error.Message != "" {
		return nil, fmt.Errorf("embedding API error: %s", decoded.Error.Message)
	}
	return api.PlaceEmbeddings(len(texts), len(decoded.Data), func(i int) (int, []float32) {
		if decoded.Data[i].Index == nil {
			return i, decoded.Data[i].Embedding
		}
		return *decoded.Data[i].Index, decoded.Data[i].Embedding
	})
}
