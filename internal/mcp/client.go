package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	secutils "github.com/Tencent/WeKnora/internal/utils"
	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"
)

// MCPClient defines the interface for MCP client implementations
type MCPClient interface {
	// Connect establishes connection to the MCP service
	Connect(ctx context.Context) error

	// Disconnect closes the connection to the MCP service
	Disconnect() error

	// Initialize performs the MCP initialize handshake
	Initialize(ctx context.Context) (*InitializeResult, error)

	// ListTools retrieves the list of available tools from the MCP service
	ListTools(ctx context.Context) ([]*types.MCPTool, error)

	// ListResources retrieves the list of available resources from the MCP service
	ListResources(ctx context.Context) ([]*types.MCPResource, error)

	// CallTool calls a tool on the MCP service
	CallTool(ctx context.Context, name string, args map[string]interface{}) (*CallToolResult, error)

	// ReadResource reads a resource from the MCP service
	ReadResource(ctx context.Context, uri string) (*ReadResourceResult, error)

	// IsConnected returns true if the client is connected
	IsConnected() bool

	// GetServiceID returns the service ID this client is connected to
	GetServiceID() string
}

// ClientConfig represents configuration for creating an MCP client
type ClientConfig struct {
	Service *types.MCPService
}

// mcpGoClient wraps mark3labs/mcp-go client to implement our MCPClient interface
type mcpGoClient struct {
	service     *types.MCPService
	client      *client.Client
	connected   bool
	initialized bool
}

// NewMCPClient creates a new MCP client based on the transport type
func NewMCPClient(config *ClientConfig) (MCPClient, error) {
	// Create HTTP client with timeout
	timeout := 30 * time.Second
	if config.Service.AdvancedConfig != nil && config.Service.AdvancedConfig.Timeout > 0 {
		timeout = time.Duration(config.Service.AdvancedConfig.Timeout) * time.Second
	}

	httpClient := &http.Client{
		Timeout: timeout,
	}

	// Build headers
	headers := make(map[string]string)
	for key, value := range config.Service.Headers {
		headers[key] = value
	}

	// Add auth headers
	if config.Service.AuthConfig != nil {
		if config.Service.AuthConfig.APIKey != "" {
			headers["X-API-Key"] = config.Service.AuthConfig.APIKey
		}
		if config.Service.AuthConfig.Token != "" {
			headers["Authorization"] = "Bearer " + config.Service.AuthConfig.Token
		}
		if config.Service.AuthConfig.CustomHeaders != nil {
			for key, value := range config.Service.AuthConfig.CustomHeaders {
				headers[key] = value
			}
		}
	}

	// Create client based on transport type
	var mcpClient *client.Client
	var err error
	switch config.Service.TransportType {
	case types.MCPTransportSSE:
		if config.Service.URL == nil || *config.Service.URL == "" {
			return nil, fmt.Errorf("URL is required for SSE transport")
		}
		mcpClient, err = client.NewSSEMCPClient(*config.Service.URL,
			client.WithHTTPClient(httpClient),
			client.WithHeaders(headers),
		)
		if err != nil {
			return nil, fmt.Errorf("failed to create SSE client: %w", err)
		}
	case types.MCPTransportHTTPStreamable:
		if config.Service.URL == nil || *config.Service.URL == "" {
			return nil, fmt.Errorf("URL is required for HTTP Streamable transport")
		}
		// For HTTP streamable, we need to use transport options
		mcpClient, err = client.NewStreamableHttpClient(*config.Service.URL,
			transport.WithHTTPBasicClient(httpClient),
			transport.WithHTTPHeaders(headers),
		)
		if err != nil {
			return nil, fmt.Errorf("failed to create HTTP streamable client: %w", err)
		}
	case types.MCPTransportStdio:
		if config.Service.StdioConfig == nil {
			return nil, fmt.Errorf("stdio_config is required for stdio transport")
		}

		// Security validation: validate command, args, and env vars before execution
		// This prevents command injection attacks (CWE-78)
		if err := secutils.ValidateStdioConfig(
			config.Service.StdioConfig.Command,
			config.Service.StdioConfig.Args,
			config.Service.EnvVars,
		); err != nil {
			return nil, fmt.Errorf("stdio configuration validation failed: %w", err)
		}

		// Convert env vars map to []string format (KEY=value)
		envVars := make([]string, 0, len(config.Service.EnvVars))
		for key, value := range config.Service.EnvVars {
			envVars = append(envVars, fmt.Sprintf("%s=%s", key, value))
		}

		// Create stdio client with options
		// NewStdioMCPClientWithOptions(command string, env []string, args []string, opts ...transport.StdioOption)
		mcpClient, err = client.NewStdioMCPClientWithOptions(
			config.Service.StdioConfig.Command,
			envVars,
			config.Service.StdioConfig.Args,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to create stdio client: %w", err)
		}
	default:
		return nil, ErrUnsupportedTransport
	}

	return &mcpGoClient{
		service: config.Service,
		client:  mcpClient,
	}, nil
}

// Connect establishes connection to the MCP service
func (c *mcpGoClient) Connect(ctx context.Context) error {
	if c.connected {
		return ErrAlreadyConnected
	}

	// Start the client
	if err := c.client.Start(ctx); err != nil {
		return fmt.Errorf("failed to start client: %w", err)
	}

	c.connected = true
	if c.service.TransportType == types.MCPTransportStdio {
		logger.GetLogger(ctx).Infof("MCP stdio client connected: %s %v",
			c.service.StdioConfig.Command, c.service.StdioConfig.Args)
	} else {
		logger.GetLogger(ctx).Infof("MCP client connected to %s", *c.service.URL)
	}
	return nil
}

// Disconnect closes the connection
func (c *mcpGoClient) Disconnect() error {
	if !c.connected {
		return nil
	}

	// Close the client
	if c.client != nil {
		c.client.Close()
	}

	c.connected = false
	c.initialized = false
	return nil
}

// Initialize performs the MCP initialize handshake
func (c *mcpGoClient) Initialize(ctx context.Context) (*InitializeResult, error) {
	if !c.connected {
		return nil, ErrNotConnected
	}

	// Initialize the client
	req := mcp.InitializeRequest{
		Params: mcp.InitializeParams{
			ProtocolVersion: mcp.LATEST_PROTOCOL_VERSION,
			Capabilities:    mcp.ClientCapabilities{},
			ClientInfo: mcp.Implementation{
				Name:    "WeKnora",
				Version: "1.0.0",
			},
		},
	}

	result, err := c.client.Initialize(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize: %w", err)
	}

	c.initialized = true

	return &InitializeResult{
		ProtocolVersion: result.ProtocolVersion,
		ServerInfo: ServerInfo{
			Name:    result.ServerInfo.Name,
			Version: result.ServerInfo.Version,
		},
	}, nil
}

// ListTools retrieves the list of available tools
func (c *mcpGoClient) ListTools(ctx context.Context) ([]*types.MCPTool, error) {
	if !c.initialized {
		return nil, ErrNotConnected
	}

	req := mcp.ListToolsRequest{}
	result, err := c.client.ListTools(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to list tools: %w", err)
	}

	// Convert to our types
	tools := make([]*types.MCPTool, len(result.Tools))
	for i, tool := range result.Tools {
		data, _ := json.Marshal(tool.InputSchema)
		tools[i] = &types.MCPTool{
			Name:        tool.Name,
			Description: tool.Description,
			InputSchema: data,
		}
	}

	return tools, nil
}

// ListResources retrieves the list of available resources
func (c *mcpGoClient) ListResources(ctx context.Context) ([]*types.MCPResource, error) {
	if !c.initialized {
		return nil, ErrNotConnected
	}

	req := mcp.ListResourcesRequest{}
	result, err := c.client.ListResources(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to list resources: %w", err)
	}

	// Convert to our types
	resources := make([]*types.MCPResource, len(result.Resources))
	for i, resource := range result.Resources {
		resources[i] = &types.MCPResource{
			URI:         resource.URI,
			Name:        resource.Name,
			Description: resource.Description,
			MimeType:    resource.MIMEType,
		}
	}

	return resources, nil
}

// CallTool calls a tool on the MCP service
func (c *mcpGoClient) CallTool(ctx context.Context, name string, args map[string]interface{}) (*CallToolResult, error) {
	if !c.initialized {
		return nil, ErrNotConnected
	}

	req := mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name:      name,
			Arguments: args,
		},
	}

	result, err := c.client.CallTool(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to call tool: %w", err)
	}

	// Convert to our types
	content := make([]ContentItem, 0, len(result.Content))
	for _, item := range result.Content {
		if textContent, ok := mcp.AsTextContent(item); ok {
			content = append(content, ContentItem{
				Type: "text",
				Text: textContent.Text,
			})
		} else if imageContent, ok := mcp.AsImageContent(item); ok {
			content = append(content, ContentItem{
				Type:     "image",
				Data:     imageContent.Data,
				MimeType: imageContent.MIMEType,
			})
		}
	}

	return &CallToolResult{
		IsError: result.IsError,
		Content: content,
	}, nil
}

// ReadResource reads a resource from the MCP service
func (c *mcpGoClient) ReadResource(ctx context.Context, uri string) (*ReadResourceResult, error) {
	if !c.initialized {
		return nil, ErrNotConnected
	}

	req := mcp.ReadResourceRequest{
		Params: mcp.ReadResourceParams{
			URI: uri,
		},
	}

	result, err := c.client.ReadResource(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to read resource: %w", err)
	}

	// Convert to our types
	contents := make([]ResourceContent, 0, len(result.Contents))
	for _, item := range result.Contents {
		if textContent, ok := mcp.AsTextResourceContents(item); ok {
			contents = append(contents, ResourceContent{
				URI:      textContent.URI,
				MimeType: textContent.MIMEType,
				Text:     textContent.Text,
			})
		} else if blobContent, ok := mcp.AsBlobResourceContents(item); ok {
			contents = append(contents, ResourceContent{
				URI:      blobContent.URI,
				MimeType: blobContent.MIMEType,
				Blob:     blobContent.Blob,
			})
		}
	}

	return &ReadResourceResult{
		Contents: contents,
	}, nil
}

// IsConnected returns true if the client is connected
func (c *mcpGoClient) IsConnected() bool {
	return c.connected
}

// GetServiceID returns the service ID
func (c *mcpGoClient) GetServiceID() string {
	return c.service.ID
}
