// Package watsonxrank implements IBM watsonx.ai's native text rerank API.
// https://cloud.ibm.com/apidocs/watsonx-ai#text-rerank
// https://dataplatform.cloud.ibm.com/docs/content/wsj/analyze-data/fm-api-rerank.html?context=wx&audience=wdp
package watsonxrank

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Tencent/WeKnora/internal/models/api"
)

// Config holds the watsonx endpoint and rerank request settings.
type Config struct {
	Endpoint api.Endpoint
	Settings api.RerankSettings
}

// Client calls watsonx.ai's text rerank API.
type Client struct{ cfg Config }

// New creates a watsonx rerank client.
func New(cfg Config) *Client { return &Client{cfg: cfg} }

// apiVersion is the watsonx.ai API version sent on every request. The API
// requires a version query parameter of the form YYYY-MM-DD; 2024-05-31 is
// the version IBM's own Node.js SDK example uses for textRerank. An
// operator can override it through the endpoint query, which wins.
const apiVersion = "2024-05-31"

func (c *Client) url() string {
	// The watsonx API requires a version query parameter; add the default
	// unless the endpoint already carries one. The query map is copied so
	// the caller's endpoint is never mutated.
	ep := c.cfg.Endpoint
	query := make(map[string]string, len(ep.Query)+1)
	for k, v := range ep.Query {
		query[k] = v
	}
	if _, ok := query["version"]; !ok {
		query["version"] = apiVersion
	}
	ep.Query = query
	if ep.URL != "" {
		return ep.Resolve("")
	}
	path := c.cfg.Settings.Path
	if path == "" {
		path = "/ml/v1/text/rerank"
	}
	if strings.HasSuffix(strings.TrimRight(ep.BaseURL, "/"), path) {
		path = ""
	}
	return ep.Resolve(path)
}

type request struct {
	ModelID    string         `json:"model_id"`
	Inputs     []string       `json:"inputs"`
	Query      string         `json:"query"`
	Parameters map[string]any `json:"parameters,omitempty"`
}

// BuildRequestBody constructs the native watsonx body. Asking for every
// candidate back through return_options.top_n preserves WeKnora's index
// mapping. project_id / space_id are required by the API — one of them must
// come from Settings.ExtraBody, which is merged in but cannot replace
// model_id, inputs, query or parameters.
func (c *Client) BuildRequestBody(query string, documents []string) (map[string]any, error) {
	body := request{
		ModelID: c.cfg.Endpoint.Model,
		Inputs:  documents,
		Query:   query,
		Parameters: map[string]any{
			"return_options": map[string]any{"top_n": len(documents)},
		},
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
	Results *[]result `json:"results"`
}

type result struct {
	Index *int     `json:"index"`
	Score *float64 `json:"score"`
}

// Rerank returns scores for the supplied documents in watsonx's ranked order.
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
	if decoded.Results == nil {
		return nil, fmt.Errorf("watsonx rerank response has no results array")
	}
	if len(*decoded.Results) != len(documents) {
		return nil, fmt.Errorf(
			"watsonx rerank returned %d scores for %d documents", len(*decoded.Results), len(documents),
		)
	}
	out := make([]api.RerankResult, 0, len(*decoded.Results))
	seen := make([]bool, len(documents))
	for _, item := range *decoded.Results {
		if item.Index == nil || item.Score == nil {
			return nil, fmt.Errorf("watsonx rerank result is missing index or score")
		}
		if *item.Index < 0 || *item.Index >= len(documents) {
			return nil, fmt.Errorf("rerank index %d out of range for %d documents", *item.Index, len(documents))
		}
		if seen[*item.Index] {
			return nil, fmt.Errorf("watsonx rerank returned duplicate index %d", *item.Index)
		}
		seen[*item.Index] = true
		out = append(out, api.RerankResult{
			Index: *item.Index, Score: *item.Score, Text: documents[*item.Index],
		})
	}
	return out, nil
}
