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
	"github.com/Tencent/WeKnora/internal/models/catalog"
)

// Config is everything the client needs, already resolved by the catalog.
type Config struct {
	Endpoint   api.Endpoint
	Settings   catalog.EmbeddingsSettings
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
	s := c.cfg.Settings
	requests := make([]any, 0, len(texts))
	for _, text := range texts {
		req := map[string]any{
			"model":   c.qualifiedModel(),
			"content": map[string]any{"parts": []any{map[string]any{"text": text}}},
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
	var decoded response
	err := c.cfg.Endpoint.PostJSONWithRetry(
		ctx, c.url(), c.BuildRequestBody(texts, kind), &decoded, c.cfg.Retry, "embedding",
	)
	if err != nil {
		return nil, err
	}
	if decoded.Error != nil && decoded.Error.Message != "" {
		return nil, fmt.Errorf("gemini embedding error: %s", decoded.Error.Message)
	}
	if len(decoded.Embeddings) != len(texts) {
		return nil, fmt.Errorf(
			"gemini returned %d embeddings for %d inputs", len(decoded.Embeddings), len(texts))
	}
	return api.PlaceEmbeddings(len(texts), len(decoded.Embeddings), func(i int) (int, []float32) {
		return i, decoded.Embeddings[i].Values
	})
}
