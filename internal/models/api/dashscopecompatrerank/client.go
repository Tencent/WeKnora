// Package dashscopecompatrerank implements the flat rerank shape Alibaba
// Model Studio serves qwen3-rerank on: POST {base}/compatible-api/v1/reranks
// with {model, query, documents, top_n} at the top level of the request and
// {results: [{index, relevance_score}]} at the top level of the reply.
//
// It is a separate package from dashscoperank because the two shapes share
// nothing but the vendor. The native dialect wraps the same fields in
// input/parameters and answers under output, and Alibaba documents them as
// incompatible ("两种接口的请求体结构和响应格式不同"); qwen3-rerank is only
// callable through this one, gte-rerank-v2 and the other text-rerank models
// only through that one.
//
// Two optional fields of the native shape are deliberately left out:
//
//   - return_documents is not documented for qwen3-rerank (the compatibility
//     route lists gte-rerank-v2 and qwen3-vl-rerank), and a reply to the
//     documented request carries index and relevance_score only — never the
//     document text. Nothing is therefore asked for, and the shared batching
//     layer fills each result's text in from the caller's own slice. The
//     route does happen to answer a return_documents request with the text
//     attached, but that is undocumented behaviour and this package does not
//     depend on it.
//   - instruct is accepted, but sending none already means the
//     question-answering retrieval instruction every other protocol here is
//     compared against.
//
// https://help.aliyun.com/zh/model-studio/text-rerank-api
package dashscopecompatrerank

import (
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

// Client talks the flat DashScope rerank shape to one endpoint.
type Client struct {
	cfg Config
}

// New builds a client.
func New(cfg Config) *Client { return &Client{cfg: cfg} }

const defaultPath = "/compatible-api/v1/reranks"

// path appends the compatibility route unless the base URL already names it.
// Vendors whose default base URL is the full endpoint — and operators who
// pasted one — must not have the path doubled.
func (c *Client) path() string {
	if c.cfg.Endpoint.URL != "" {
		return ""
	}
	path := c.cfg.Settings.Path
	if path == "" {
		path = defaultPath
	}
	if strings.HasSuffix(strings.TrimRight(c.cfg.Endpoint.BaseURL, "/"), path) {
		path = ""
	}
	return path
}

// request is the whole body: flat, with no input/parameters wrapper, over the
// field set — and in the order — of the vendor's own example.
type request struct {
	Model     string   `json:"model"`
	Query     string   `json:"query"`
	Documents []string `json:"documents"`
	// TopN names the count of the documents being sent. The field is optional
	// in this shape (the default is every document), but it is always sent:
	// asking for exactly what the caller sent leaves nothing to the default,
	// and top_n is what the vendor's request example carries. It is never
	// zero — the shared layer returns early on an empty document set rather
	// than calling a protocol with one.
	TopN int `json:"top_n"`
}

// response is the reply shape. Unlike the native dialect there is no output
// wrapper: results sit at the top level beside the model name, the request id
// and the usage counters this build does not read.
type response struct {
	Results []result `json:"results"`
	// DashScope reports failures in the body with a 200 on some paths.
	Code    string `json:"code"`
	Message string `json:"message"`
}

type result struct {
	Index          int     `json:"index"`
	RelevanceScore float64 `json:"relevance_score"`
}

// BuildRequestBody is the golden-test entry point: it returns the exact JSON
// object that would be sent.
func (c *Client) BuildRequestBody(query string, documents []string) (map[string]any, error) {
	body := request{
		Model:     c.cfg.Endpoint.Model,
		Query:     query,
		Documents: documents,
		TopN:      len(documents),
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

// Rerank scores documents against the query. Each result carries only an
// index and a score: this shape never echoes the documents, and the caller
// reads the text out of the slice it sent.
func (c *Client) Rerank(ctx context.Context, query string, documents []string) ([]api.RerankResult, error) {
	body, err := c.BuildRequestBody(query, documents)
	if err != nil {
		return nil, err
	}
	var decoded response
	url := c.cfg.Endpoint.Resolve(c.path())
	if err := c.cfg.Endpoint.PostJSON(ctx, url, body, &decoded); err != nil {
		return nil, err
	}
	if decoded.Code != "" {
		return nil, fmt.Errorf("DashScope rerank error %s: %s", decoded.Code, decoded.Message)
	}
	out := make([]api.RerankResult, 0, len(decoded.Results))
	for _, item := range decoded.Results {
		if item.Index < 0 || item.Index >= len(documents) {
			return nil, fmt.Errorf("rerank index %d out of range for %d documents", item.Index, len(documents))
		}
		out = append(out, api.RerankResult{
			Index: item.Index,
			Score: item.RelevanceScore,
		})
	}
	return out, nil
}
