// Package dashscoperank implements Alibaba Model Studio's native text-rerank
// shape: the same fields as the Cohere dialect, wrapped in input/parameters
// and answered under output.
//
// https://help.aliyun.com/zh/model-studio/text-rerank-api
package dashscoperank

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

// Client talks DashScope's native rerank to one endpoint.
type Client struct {
	cfg Config
}

// New builds a client.
func New(cfg Config) *Client { return &Client{cfg: cfg} }

type request struct {
	Model      string     `json:"model"`
	Input      input      `json:"input"`
	Parameters parameters `json:"parameters"`
}

type input struct {
	Query     string   `json:"query"`
	Documents []string `json:"documents"`
}

type parameters struct {
	ReturnDocuments bool `json:"return_documents"`
	TopN            int  `json:"top_n"`
}

type response struct {
	Output struct {
		Results []struct {
			Index          int     `json:"index"`
			RelevanceScore float64 `json:"relevance_score"`
			Document       struct {
				Text string `json:"text"`
			} `json:"document"`
		} `json:"results"`
	} `json:"output"`
	// DashScope reports failures in the body with a 200 on some paths.
	Code    string `json:"code"`
	Message string `json:"message"`
}

// BuildRequestBody is the golden-test entry point.
func (c *Client) BuildRequestBody(query string, documents []string) (map[string]any, error) {
	body := request{
		Model: c.cfg.Endpoint.Model,
		Input: input{Query: query, Documents: documents},
		Parameters: parameters{
			ReturnDocuments: c.cfg.Settings.SendReturnDocs,
			// top_n is not optional in this shape: leaving it at zero would
			// ask for no documents at all, so the full count is always sent.
			TopN: len(documents),
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
	for k, v := range c.cfg.Settings.ExtraBody {
		if _, exists := out[k]; !exists {
			out[k] = v
		}
	}
	return out, nil
}

// Rerank scores documents against the query.
func (c *Client) Rerank(ctx context.Context, query string, documents []string) ([]api.RerankResult, error) {
	body, err := c.BuildRequestBody(query, documents)
	if err != nil {
		return nil, err
	}
	var decoded response
	url := c.cfg.Endpoint.Resolve(c.cfg.Settings.Path)
	if err := c.cfg.Endpoint.PostJSON(ctx, url, body, &decoded); err != nil {
		return nil, err
	}
	if decoded.Code != "" {
		return nil, fmt.Errorf("DashScope rerank error %s: %s", decoded.Code, decoded.Message)
	}
	out := make([]api.RerankResult, 0, len(decoded.Output.Results))
	for _, item := range decoded.Output.Results {
		if item.Index < 0 || item.Index >= len(documents) {
			return nil, fmt.Errorf("rerank index %d out of range for %d documents", item.Index, len(documents))
		}
		out = append(out, api.RerankResult{
			Index: item.Index,
			Score: item.RelevanceScore,
			Text:  item.Document.Text,
		})
	}
	return out, nil
}
