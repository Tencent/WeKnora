// Package cohererank implements the rerank shape Cohere defined and almost
// every other vendor copied: POST {base}/rerank with {model, query,
// documents} answering {results: [{index, relevance_score, document}]}.
// A bare array of the same result objects is accepted too: self-hosted
// runtimes such as SGLang return that shape instead of the envelope.
//
// Jina, Zhipu, SiliconFlow, Qianfan, GPUStack, WeKnora Cloud and any
// OpenAI-compatible gateway that serves rerank all speak it. Their
// differences — which optional fields are accepted, where the endpoint sits,
// what the documented ceilings are — arrive as api.RerankSettings; this
// package contains no vendor names.
package cohererank

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Tencent/WeKnora/internal/models/api"
)

// Config is everything the client needs, already resolved by the api.
type Config struct {
	Endpoint api.Endpoint
	Settings api.RerankSettings
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

// BuildImageRequestBody is the golden-test entry point for image documents.
// The shape has no standard image document; a vendor that extends it names
// the key of the object that carries one. Jina's reranker reference gives
// {"image": URL or base64} documents; it shows no base64 example, so the
// data URI its embeddings reference documents is used (unverified for
// rerank). The request carries images only, so text documents never change
// shape.
func (c *Client) BuildImageRequestBody(query string, images []api.EmbedImage) (map[string]any, error) {
	documents := make([]any, len(images))
	for i, img := range images {
		documents[i] = map[string]any{c.cfg.Settings.ImageField: img.DataURI()}
	}
	body, err := c.BuildRequestBody(query, nil)
	if err != nil {
		return nil, err
	}
	body["documents"] = documents
	if c.cfg.Settings.SendTopN {
		body["top_n"] = len(images)
	}
	// Results are matched by index; an echoed image document would carry its
	// whole base64 payload back for nothing.
	if _, asked := body["return_documents"]; asked {
		body["return_documents"] = false
	}
	return body, nil
}

// AcceptsImages reports whether the vendor names an image field; without one
// a Cohere-shaped endpoint takes text documents only.
func (c *Client) AcceptsImages() bool { return c.cfg.Settings.ImageField != "" }

// Rerank scores documents against the query, in the order they were given.
func (c *Client) Rerank(ctx context.Context, query string, documents []string) ([]api.RerankResult, error) {
	body, err := c.BuildRequestBody(query, documents)
	if err != nil {
		return nil, err
	}
	return c.post(ctx, body, len(documents))
}

// RerankImages scores image documents against the query; indices refer to
// images.
func (c *Client) RerankImages(ctx context.Context, query string, images []api.EmbedImage) ([]api.RerankResult, error) {
	if !c.AcceptsImages() {
		return nil, fmt.Errorf("this rerank endpoint declares no image documents")
	}
	body, err := c.BuildImageRequestBody(query, images)
	if err != nil {
		return nil, err
	}
	return c.post(ctx, body, len(images))
}

func (c *Client) post(ctx context.Context, body map[string]any, count int) ([]api.RerankResult, error) {
	// Decode into raw JSON first: some Cohere-shaped runtimes (SGLang, for
	// one) answer the same request with a bare array instead of the
	// {"results": [...]} envelope. Both shapes mean the same thing, so the
	// envelope is not part of the contract callers may rely on.
	var raw json.RawMessage
	if err := c.cfg.Endpoint.PostJSON(ctx, c.url(), body, &raw); err != nil {
		return nil, err
	}
	results, err := decodeResults(raw)
	if err != nil {
		return nil, err
	}
	out := make([]api.RerankResult, 0, len(results))
	for _, item := range results {
		if item.Index < 0 || item.Index >= count {
			return nil, fmt.Errorf("rerank index %d out of range for %d documents", item.Index, count)
		}
		out = append(out, api.RerankResult{
			Index: item.Index,
			Score: firstNonNil(item.RelevanceScore, item.Score),
			Text:  item.Document.Text,
		})
	}
	return out, nil
}

// decodeResults accepts both documented envelopes: the Cohere object
// {"results": [...]} and the bare array some self-hosted runtimes return.
func decodeResults(raw []byte) ([]result, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) > 0 && trimmed[0] == '[' {
		var results []result
		if err := json.Unmarshal(trimmed, &results); err != nil {
			return nil, fmt.Errorf("decode response: %w", err)
		}
		return results, nil
	}
	var decoded response
	if err := json.Unmarshal(trimmed, &decoded); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	return decoded.Results, nil
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
