// Package googleembeddings implements Gemini's batchEmbedContents: one POST
// carrying a request per text, answering embeddings[].values in order.
//
// Three facts shape it. The per-request `model` must be the fully qualified
// `models/{id}`, not the bare id the URL already carries. The per-request
// options — `taskType`, `outputDimensionality` — belong inside
// `embedContentConfig`; the reference marks the same names at the top level
// of the request "Deprecated: Please use EmbedContentConfig…". And
// `taskType` exists on gemini-embedding-001 but not on gemini-embedding-2,
// which takes its task instruction in the prompt instead — so the field is
// declared per model rather than for the vendor.
//
// Each text is its own request with a single part: gemini-embedding-2 fuses
// the parts of one Content into one vector.
//
// https://ai.google.dev/api/embeddings
package googleembeddings

import (
	"context"
	"fmt"
	"strings"

	"github.com/Tencent/WeKnora/internal/models/api"
)

// Config is everything the client needs, already resolved by the api.
type Config struct {
	Endpoint   api.Endpoint
	Settings   api.EmbeddingsSettings
	Dimensions int
	Retry      api.RetryPolicy
}

// Client talks batchEmbedContents to one endpoint.
type Client struct {
	cfg Config
}

// New builds a client.
func New(cfg Config) *Client { return &Client{cfg: cfg} }

const method = ":batchEmbedContents"

// configField holds the per-request options.
const configField = "embedContentConfig"

// qualifiedModel is the `models/{id}` form the per-request model field needs.
func (c *Client) qualifiedModel() string {
	return "models/" + strings.TrimPrefix(c.cfg.Endpoint.Model, "models/")
}

// openAIFacade is the sub-path of the same API version that serves the
// OpenAI-compatible surface. Rows that chat through it carry it in their base
// URL; the native method lives one level up.
const openAIFacade = "/openai"

// url is {base}/models/{id}:batchEmbedContents.
func (c *Client) url() string {
	if c.cfg.Endpoint.URL != "" {
		return c.cfg.Endpoint.Resolve("")
	}
	endpoint := c.cfg.Endpoint
	endpoint.BaseURL = strings.TrimSuffix(strings.TrimRight(endpoint.BaseURL, "/"), openAIFacade)
	return endpoint.Resolve("/" + c.qualifiedModel() + method)
}

type response struct {
	Embeddings []struct {
		Values []float32 `json:"values"`
	} `json:"embeddings"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// BuildRequestBody is the golden-test entry point.
func (c *Client) BuildRequestBody(texts []string, kind api.EmbedInputType) map[string]any {
	parts := make([]any, 0, len(texts))
	for _, text := range texts {
		parts = append(parts, map[string]any{"text": text})
	}
	return c.body(parts, kind)
}

// BuildImageRequestBody is the golden-test entry point for images: an image
// is an inlineData part carrying its MIME type and base64 bytes.
func (c *Client) BuildImageRequestBody(images []api.EmbedImage, kind api.EmbedInputType) map[string]any {
	parts := make([]any, 0, len(images))
	for _, img := range images {
		parts = append(parts, map[string]any{
			"inlineData": map[string]any{"mimeType": img.MIMEType, "data": img.Base64()},
		})
	}
	return c.body(parts, kind)
}

// body wraps every part in its own request, so each gets its own vector.
func (c *Client) body(parts []any, kind api.EmbedInputType) map[string]any {
	s := c.cfg.Settings
	requests := make([]any, 0, len(parts))
	for _, part := range parts {
		req := map[string]any{
			"model":   c.qualifiedModel(),
			"content": map[string]any{"parts": []any{part}},
		}
		config := map[string]any{}
		if field := s.InputTypeField; field != "" {
			config[field] = s.InputTypeValue(kind)
		}
		if s.DimensionsField != "" && c.cfg.Dimensions > 0 {
			config[s.DimensionsField] = c.cfg.Dimensions
		}
		if len(config) > 0 {
			req[configField] = config
		}
		requests = append(requests, req)
	}
	body := map[string]any{"requests": requests}
	for k, v := range s.ExtraBody {
		if _, exists := body[k]; !exists {
			body[k] = v
		}
	}
	return body
}

// Embed vectorizes one batch. The reply carries no index: embeddings come
// back positionally, one per request, so a short reply is an error rather
// than a partially filled result.
func (c *Client) Embed(
	ctx context.Context, texts []string, kind api.EmbedInputType,
) ([][]float32, error) {
	return c.post(ctx, c.BuildRequestBody(texts, kind), len(texts))
}

// AcceptsImages is always true: inlineData is part of the schema.
func (c *Client) AcceptsImages() bool { return true }

// EmbedImages vectorizes one batch of images, each its own vector.
func (c *Client) EmbedImages(
	ctx context.Context, images []api.EmbedImage, kind api.EmbedInputType,
) ([][]float32, error) {
	return c.post(ctx, c.BuildImageRequestBody(images, kind), len(images))
}

func (c *Client) post(ctx context.Context, body map[string]any, want int) ([][]float32, error) {
	var decoded response
	err := c.cfg.Endpoint.PostJSONWithRetry(ctx, c.url(), body, &decoded, c.cfg.Retry, "embedding")
	if err != nil {
		return nil, err
	}
	if decoded.Error != nil && decoded.Error.Message != "" {
		return nil, fmt.Errorf("gemini embedding error: %s", decoded.Error.Message)
	}
	if len(decoded.Embeddings) != want {
		return nil, fmt.Errorf(
			"gemini returned %d embeddings for %d inputs", len(decoded.Embeddings), want)
	}
	return api.PlaceEmbeddings(want, len(decoded.Embeddings), func(i int) (int, []float32) {
		return i, decoded.Embeddings[i].Values
	})
}
