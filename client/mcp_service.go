package client

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

// MCPTransportType represents the transport type for MCP service
type MCPTransportType string

const (
	MCPTransportSSE            MCPTransportType = "sse"
	MCPTransportHTTPStreamable MCPTransportType = "http-streamable"
	MCPTransportStdio          MCPTransportType = "stdio"
)

// MCPService represents an MCP service configuration
type MCPService struct {
	ID             string             `json:"id"`
	TenantID       uint64             `json:"tenant_id"`
	Name           string             `json:"name"`
	Description    string             `json:"description"`
	Enabled        bool               `json:"enabled"`
	TransportType  MCPTransportType   `json:"transport_type"`
	URL            *string            `json:"url,omitempty"`
	Headers        map[string]string  `json:"headers"`
	AuthConfig     *MCPAuthConfig     `json:"auth_config"`
	AdvancedConfig *MCPAdvancedConfig `json:"advanced_config"`
	StdioConfig    *MCPStdioConfig    `json:"stdio_config,omitempty"`
	EnvVars        map[string]string  `json:"env_vars,omitempty"`
	IsBuiltin      bool               `json:"is_builtin"`
	CreatedAt      string             `json:"created_at"`
	UpdatedAt      string             `json:"updated_at"`
}

// MCPAuthConfig represents authentication configuration for MCP service
type MCPAuthConfig struct {
	APIKey        string            `json:"api_key,omitempty"`
	Token         string            `json:"token,omitempty"`
	CustomHeaders map[string]string `json:"custom_headers,omitempty"`
}

// MCPAdvancedConfig represents advanced configuration for MCP service
type MCPAdvancedConfig struct {
	Timeout    int `json:"timeout"`
	RetryCount int `json:"retry_count"`
	RetryDelay int `json:"retry_delay"`
}

// MCPStdioConfig represents stdio transport configuration
type MCPStdioConfig struct {
	Command string   `json:"command"`
	Args    []string `json:"args"`
}

// MCPTool represents a tool exposed by an MCP service
type MCPTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
}

// MCPResource represents a resource exposed by an MCP service
type MCPResource struct {
	URI         string `json:"uri"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	MimeType    string `json:"mimeType,omitempty"`
}

// MCPTestResult represents the result of testing an MCP service connection
type MCPTestResult struct {
	Success   bool           `json:"success"`
	Message   string         `json:"message,omitempty"`
	Tools     []*MCPTool     `json:"tools,omitempty"`
	Resources []*MCPResource `json:"resources,omitempty"`
}

// CreateMCPService creates a new MCP service
func (c *Client) CreateMCPService(ctx context.Context, service *MCPService) (*MCPService, error) {
	resp, err := c.doRequest(ctx, http.MethodPost, "/api/v1/mcp-services", service, nil)
	if err != nil {
		return nil, err
	}

	var result struct {
		Success bool        `json:"success"`
		Data    *MCPService `json:"data"`
	}
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}
	return result.Data, nil
}

// ListMCPServices lists all MCP services for the current tenant
func (c *Client) ListMCPServices(ctx context.Context) ([]*MCPService, error) {
	resp, err := c.doRequest(ctx, http.MethodGet, "/api/v1/mcp-services", nil, nil)
	if err != nil {
		return nil, err
	}

	var result struct {
		Success bool          `json:"success"`
		Data    []*MCPService `json:"data"`
	}
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}
	return result.Data, nil
}

// GetMCPService gets an MCP service by ID
func (c *Client) GetMCPService(ctx context.Context, serviceID string) (*MCPService, error) {
	resp, err := c.doRequest(ctx, http.MethodGet, fmt.Sprintf("/api/v1/mcp-services/%s", serviceID), nil, nil)
	if err != nil {
		return nil, err
	}

	var result struct {
		Success bool        `json:"success"`
		Data    *MCPService `json:"data"`
	}
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}
	return result.Data, nil
}

// UpdateMCPService updates an MCP service
func (c *Client) UpdateMCPService(ctx context.Context, serviceID string, updates map[string]interface{}) (*MCPService, error) {
	resp, err := c.doRequest(ctx, http.MethodPut, fmt.Sprintf("/api/v1/mcp-services/%s", serviceID), updates, nil)
	if err != nil {
		return nil, err
	}

	var result struct {
		Success bool        `json:"success"`
		Data    *MCPService `json:"data"`
	}
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}
	return result.Data, nil
}

// DeleteMCPService deletes an MCP service
func (c *Client) DeleteMCPService(ctx context.Context, serviceID string) error {
	resp, err := c.doRequest(ctx, http.MethodDelete, fmt.Sprintf("/api/v1/mcp-services/%s", serviceID), nil, nil)
	if err != nil {
		return err
	}
	return parseResponse(resp, nil)
}

// TestMCPService tests an MCP service connection
func (c *Client) TestMCPService(ctx context.Context, serviceID string) (*MCPTestResult, error) {
	resp, err := c.doRequest(ctx, http.MethodPost, fmt.Sprintf("/api/v1/mcp-services/%s/test", serviceID), nil, nil)
	if err != nil {
		return nil, err
	}

	var result struct {
		Success bool           `json:"success"`
		Data    *MCPTestResult `json:"data"`
	}
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}
	return result.Data, nil
}

// GetMCPServiceTools gets the tools provided by an MCP service
func (c *Client) GetMCPServiceTools(ctx context.Context, serviceID string) ([]*MCPTool, error) {
	resp, err := c.doRequest(ctx, http.MethodGet, fmt.Sprintf("/api/v1/mcp-services/%s/tools", serviceID), nil, nil)
	if err != nil {
		return nil, err
	}

	var result struct {
		Success bool       `json:"success"`
		Data    []*MCPTool `json:"data"`
	}
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}
	return result.Data, nil
}

// GetMCPServiceResources gets the resources provided by an MCP service
func (c *Client) GetMCPServiceResources(ctx context.Context, serviceID string) ([]*MCPResource, error) {
	resp, err := c.doRequest(ctx, http.MethodGet, fmt.Sprintf("/api/v1/mcp-services/%s/resources", serviceID), nil, nil)
	if err != nil {
		return nil, err
	}

	var result struct {
		Success bool           `json:"success"`
		Data    []*MCPResource `json:"data"`
	}
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}
	return result.Data, nil
}
