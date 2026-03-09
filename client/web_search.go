package client

import (
	"context"
	"encoding/json"
	"net/http"
)

// WebSearchProvider represents a web search provider
type WebSearchProvider struct {
	Name        string `json:"name"`
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
	Enabled     bool   `json:"enabled"`
}

// GetWebSearchProviders returns the list of available web search providers
func (c *Client) GetWebSearchProviders(ctx context.Context) ([]json.RawMessage, error) {
	resp, err := c.doRequest(ctx, http.MethodGet, "/api/v1/web-search/providers", nil, nil)
	if err != nil {
		return nil, err
	}

	var result struct {
		Success bool               `json:"success"`
		Data    []json.RawMessage  `json:"data"`
	}
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}
	return result.Data, nil
}
