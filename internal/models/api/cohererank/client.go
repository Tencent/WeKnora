// Package cohererank implements the rerank shape Cohere defined and almost
// every other vendor copied: POST {base}/rerank with {model, query,
// documents} answering {results: [{index, relevance_score, document}]}.
//
// Jina, Zhipu, SiliconFlow, Qianfan, GPUStack, WeKnora Cloud and any
// OpenAI-compatible gateway that serves rerank all speak it. Their
// differences — which optional fields are accepted, where the endpoint sits,
// what the documented ceilings are — arrive as catalog.RerankSettings; this
// package contains no vendor names.
package cohererank

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Tencent/WeKnora/internal/models/api"
	"github.com/Tencent/WeKnora/internal/models/catalog"
)

// Config is everything the client needs, already resolved by the catalog.
type Config struct {
	Endpoint api.Endpoint
	Settings catalog.RerankSettings
}

// Client talks the Cohere rerank shape to one endpoint.
type Client struct {
	cfg Config
}

// New builds a client.
func New(cfg Config) *Client { return &Client{cfg: cfg} }

const defaultPath = "/rerank"

// url resolves the endpoint. A vendor whose default base URL already names
// the full rerank endpoint — and an operator who pasted one — must not have
// the path appended twice.
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

type request struct {
	Model     string   `json:"model"`
	Query     string   `json:"query"`
	Documents []string `json:"documents"`
	TopN      int      `json:"top_n,omitempty"`
	// TruncatePromptTokens is a vLLM extension, not part of any vendor's
	// documented schema. It is sent only when the operator opted in, because
	// a gateway that does not implement it rejects the unknown field.
	TruncatePromptTokens int   `json:"truncate_prompt_tokens,omitempty"`
	ReturnDocuments      *bool `json:"return_documents,omitempty"`
}

type response struct {
	Results []result `json:"results"`
}

type result struct {
	Index          int      `json:"index"`
	RelevanceScore *float64 `json:"relevance_score"`
	Score          *float64 `json:"score"`
	Document       document `json:"document"`
}

// document is the echoed text, which vendors spell either as a bare string
// or as an object with a text field.
type document struct {
	Text string
}

func (d *document) UnmarshalJSON(data []byte) error {
	var text string
	if err := json.Unmarshal(data, &text); err == nil {
		d.Text = text
		return nil
	}
	var obj struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(data, &obj); err != nil {
		return fmt.Errorf("decode document: %w", err)
	}
	d.Text = obj.Text
	return nil
}

// BuildRequestBody is the golden-test entry point: it returns the exact JSON
// object that would be sent.
func (c *Client) BuildRequestBody(query string, documents []string) (map[string]any, error) {
	body := request{
		Model:     c.cfg.Endpoint.Model,
		Query:     query,
		Documents: documents,
	}
	if c.cfg.Settings.AcceptsTruncatePromptTokens {
		body.TruncatePromptTokens = c.cfg.Settings.TruncatePromptTokens
	}
	if c.cfg.Settings.SendTopN {
		body.TopN = len(documents)
	}
	if c.cfg.Settings.SendReturnDocs {
		enabled := true
		body.ReturnDocuments = &enabled
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

// Rerank scores documents against the query, in the order they were given.
func (c *Client) Rerank(ctx context.Context, query string, documents []string) ([]api.RerankResult, error) {
	body, err := c.BuildRequestBody(query, documents)
	if err != nil {
		return nil, err
	}
	var decoded response
	if err := c.cfg.Endpoint.PostJSON(ctx, c.url(), body, &decoded); err != nil {
		return nil, err
	}
	out := make([]api.RerankResult, 0, len(decoded.Results))
	for _, item := range decoded.Results {
		if item.Index < 0 || item.Index >= len(documents) {
			return nil, fmt.Errorf("rerank index %d out of range for %d documents", item.Index, len(documents))
		}
		out = append(out, api.RerankResult{
			Index: item.Index,
			Score: firstNonNil(item.RelevanceScore, item.Score),
			Text:  item.Document.Text,
		})
	}
	return out, nil
}

// firstNonNil picks relevance_score, falling back to score: the field is
// spelled both ways across vendors that otherwise share this shape.
func firstNonNil(values ...*float64) float64 {
	for _, v := range values {
		if v != nil {
			return *v
		}
	}
	return 0
}
