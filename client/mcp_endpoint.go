package client

import (
	"context"
	"fmt"
	"net/http"
)

// MCPEndpoint is one workspace-published MCP server endpoint that external
// MCP clients connect to over Streamable HTTP at Path.
type MCPEndpoint struct {
	ID                 string   `json:"id"`
	TenantID           uint64   `json:"tenant_id"`
	Name               string   `json:"name"`
	Description        string   `json:"description"`
	Enabled            bool     `json:"enabled"`
	TokenHint          string   `json:"token_hint"`
	KnowledgeBaseIDs   []string `json:"knowledge_base_ids"`
	Tools              []string `json:"tools"`
	DefaultAgentID     string   `json:"default_agent_id"`
	RateLimitPerMinute int      `json:"rate_limit_per_minute"`
	// Path is the public MCP path relative to the server origin (/mcp/<id>).
	Path       string  `json:"path"`
	LastUsedAt *string `json:"last_used_at,omitempty"`
	CreatedAt  string  `json:"created_at"`
	UpdatedAt  string  `json:"updated_at"`
	// Token is only present on create and rotate responses and is never
	// returned again.
	Token string `json:"token,omitempty"`
}

// MCPEndpointToolDefinition describes one tool an endpoint may expose.
type MCPEndpointToolDefinition struct {
	Name        string `json:"name"`
	Group       string `json:"group"`
	Destructive bool   `json:"destructive"`
}

// MCPEndpointToolCatalog lists the tools an endpoint can expose.
type MCPEndpointToolCatalog struct {
	Groups       []string                    `json:"groups"`
	Tools        []MCPEndpointToolDefinition `json:"tools"`
	DefaultTools []string                    `json:"default_tools"`
}

// MCPEndpointRequest is the create/update payload. Nil pointers are omitted
// so an update only touches the fields you set.
type MCPEndpointRequest struct {
	Name               *string   `json:"name,omitempty"`
	Description        *string   `json:"description,omitempty"`
	Enabled            *bool     `json:"enabled,omitempty"`
	KnowledgeBaseIDs   *[]string `json:"knowledge_base_ids,omitempty"`
	Tools              *[]string `json:"tools,omitempty"`
	DefaultAgentID     *string   `json:"default_agent_id,omitempty"`
	RateLimitPerMinute *int      `json:"rate_limit_per_minute,omitempty"`
}

// ListMCPEndpoints lists the workspace's MCP endpoints (tokens are never included).
func (c *Client) ListMCPEndpoints(ctx context.Context) ([]*MCPEndpoint, error) {
	resp, err := c.doRequest(ctx, http.MethodGet, "/api/v1/mcp-endpoints", nil, nil)
	if err != nil {
		return nil, err
	}
	var result struct {
		Success bool           `json:"success"`
		Data    []*MCPEndpoint `json:"data"`
	}
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}
	return result.Data, nil
}

// GetMCPEndpointToolCatalog returns the tools an endpoint may expose.
func (c *Client) GetMCPEndpointToolCatalog(ctx context.Context) (*MCPEndpointToolCatalog, error) {
	resp, err := c.doRequest(ctx, http.MethodGet, "/api/v1/mcp-endpoints/tools", nil, nil)
	if err != nil {
		return nil, err
	}
	var result struct {
		Success bool                    `json:"success"`
		Data    *MCPEndpointToolCatalog `json:"data"`
	}
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}
	return result.Data, nil
}

// CreateMCPEndpoint publishes a new endpoint. The returned Token is shown once.
func (c *Client) CreateMCPEndpoint(ctx context.Context, req *MCPEndpointRequest) (*MCPEndpoint, error) {
	resp, err := c.doRequest(ctx, http.MethodPost, "/api/v1/mcp-endpoints", req, nil)
	if err != nil {
		return nil, err
	}
	var result struct {
		Success bool         `json:"success"`
		Data    *MCPEndpoint `json:"data"`
	}
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}
	return result.Data, nil
}

// GetMCPEndpoint returns one endpoint.
func (c *Client) GetMCPEndpoint(ctx context.Context, endpointID string) (*MCPEndpoint, error) {
	resp, err := c.doRequest(ctx, http.MethodGet, fmt.Sprintf("/api/v1/mcp-endpoints/%s", endpointID), nil, nil)
	if err != nil {
		return nil, err
	}
	var result struct {
		Success bool         `json:"success"`
		Data    *MCPEndpoint `json:"data"`
	}
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}
	return result.Data, nil
}

// UpdateMCPEndpoint applies a partial update; omitted fields are kept.
func (c *Client) UpdateMCPEndpoint(
	ctx context.Context, endpointID string, req *MCPEndpointRequest,
) (*MCPEndpoint, error) {
	resp, err := c.doRequest(ctx, http.MethodPut, fmt.Sprintf("/api/v1/mcp-endpoints/%s", endpointID), req, nil)
	if err != nil {
		return nil, err
	}
	var result struct {
		Success bool         `json:"success"`
		Data    *MCPEndpoint `json:"data"`
	}
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}
	return result.Data, nil
}

// DeleteMCPEndpoint deletes an endpoint; connected clients lose access immediately.
func (c *Client) DeleteMCPEndpoint(ctx context.Context, endpointID string) error {
	resp, err := c.doRequest(ctx, http.MethodDelete, fmt.Sprintf("/api/v1/mcp-endpoints/%s", endpointID), nil, nil)
	if err != nil {
		return err
	}
	return parseResponse(resp, nil)
}

// RotateMCPEndpointToken replaces the bearer token. The new Token is shown once.
func (c *Client) RotateMCPEndpointToken(ctx context.Context, endpointID string) (*MCPEndpoint, error) {
	resp, err := c.doRequest(ctx, http.MethodPost,
		fmt.Sprintf("/api/v1/mcp-endpoints/%s/rotate-token", endpointID), nil, nil)
	if err != nil {
		return nil, err
	}
	var result struct {
		Success bool         `json:"success"`
		Data    *MCPEndpoint `json:"data"`
	}
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}
	return result.Data, nil
}
