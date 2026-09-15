package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	secutils "github.com/Tencent/WeKnora/internal/utils"
	"github.com/google/uuid"
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

	// OAuth wiring (only used when Service.AuthConfig.AuthType == oauth).
	// The token store is scoped to (TenantID, Principal, Service.ID) so each
	// identity connects with its own access/refresh token.
	TenantID  uint64
	Principal types.Principal
	// UserID is kept for compatibility with older call sites/tests. New code
	// should pass Principal.
	UserID    string
	OAuthRepo interfaces.MCPOAuthRepository
}

// mcpGoClient wraps mark3labs/mcp-go client to implement our MCPClient interface
type mcpGoClient struct {
	service      *types.MCPService
	client       *client.Client
	oauth        *oauthRuntime
	connected    atomic.Bool
	initialized  atomic.Bool
	metadataMu   sync.RWMutex
	instructions string
}

// applyAuthHeaders injects the auth header for the SELECTED strategy only —
// driven by AuthType so static API key / bearer are mutually exclusive (the old
// code emitted both whenever the fields happened to be set, which double-authed
// after a strategy switch). OAuth is handled separately by the caller.
// CustomHeaders are always layered on top regardless of strategy and may
// override the strategy header.
func applyAuthHeaders(headers map[string]string, ac *types.MCPAuthConfig) {
	if ac == nil {
		return
	}
	switch ac.AuthType {
	case types.MCPAuthAPIKey:
		if ac.APIKey != "" {
			name := ac.APIKeyHeader
			if name == "" {
				name = "X-API-Key"
			}
			headers[name] = ac.APIKey
		}
	case types.MCPAuthBearer:
		if ac.Token != "" {
			headers["Authorization"] = "Bearer " + ac.Token
		}
	case types.MCPAuthNone:
		// Backward compatibility for rows that predate AuthType: infer from
		// whichever static credential is set, preserving the historical
		// behavior so existing services keep authenticating after upgrade.
		if ac.APIKey != "" {
			headers["X-API-Key"] = ac.APIKey
		}
		if ac.Token != "" {
			headers["Authorization"] = "Bearer " + ac.Token
		}
	}
	for key, value := range ac.CustomHeaders {
		headers[key] = value
	}
}

// OAuthRequiredError signals that the target MCP server requires OAuth
// authorization — it answered the connect/initialize handshake with a 401 that
// advertised RFC 9728 protected-resource metadata — even though the service was
// NOT configured to use OAuth. Callers use this to guide the user to switch the
// auth strategy to OAuth instead of surfacing a generic "401" failure.
type OAuthRequiredError struct {
	// MetadataURL is the RFC 9728 protected-resource metadata URL advertised by
	// the server via the WWW-Authenticate header. Non-empty by construction
	// (asOAuthRequired only wraps when the server advertised it).
	MetadataURL string
	Err         error
}

func (e *OAuthRequiredError) Error() string {
	return fmt.Sprintf("the MCP server requires OAuth authorization: %v", e.Err)
}

func (e *OAuthRequiredError) Unwrap() error { return e.Err }

// asOAuthRequired inspects err for a transport-level authorization-required
// signal that carries RFC 9728 protected-resource metadata. It returns a
// non-nil *OAuthRequiredError ONLY when the server advertised a metadata URL —
// a bare 401 without metadata is treated as an ordinary auth failure (e.g. a
// wrong/missing API key) so we don't misdirect the user toward OAuth.
func asOAuthRequired(err error) *OAuthRequiredError {
	if err == nil {
		return nil
	}
	var authErr *transport.AuthorizationRequiredError
	if errors.As(err, &authErr) && authErr.ResourceMetadataURL != "" {
		return &OAuthRequiredError{MetadataURL: authErr.ResourceMetadataURL, Err: err}
	}
	return nil
}

// NewMCPClient creates a new MCP client based on the transport type
func NewMCPClient(config *ClientConfig) (MCPClient, error) {
	if config == nil || config.Service == nil {
		return nil, fmt.Errorf("MCP client config and service are required")
	}
	if err := ValidateServiceOutboundURLs(config.Service); err != nil {
		return nil, err
	}

	// Create HTTP client with timeout
	timeout := 30 * time.Second
	if config.Service.AdvancedConfig != nil && config.Service.AdvancedConfig.Timeout > 0 {
		timeout = time.Duration(config.Service.AdvancedConfig.Timeout) * time.Second
	}

	clientCfg := secutils.DefaultSSRFSafeHTTPClientConfig()
	clientCfg.Timeout = timeout
	httpClient := secutils.NewSSRFSafeHTTPClient(clientCfg)

	// Build headers
	headers := make(map[string]string)
	for key, value := range config.Service.Headers {
		headers[key] = value
	}
	applyAuthHeaders(headers, config.Service.AuthConfig)

	// Build OAuth config when this service uses the OAuth strategy. The
	// client_id comes from the dynamically-registered client persisted at
	// authorization time; the token store loads the invoking user's token
	// and transparently refreshes it.
	oauthConfig, useOAuth, err := buildOAuthConfig(config, httpClient)
	if err != nil {
		return nil, err
	}

	// Create client based on transport type
	var mcpClient *client.Client
	switch config.Service.TransportType {
	case types.MCPTransportSSE:
		if config.Service.URL == nil || *config.Service.URL == "" {
			return nil, fmt.Errorf("URL is required for SSE transport")
		}
		if useOAuth {
			mcpClient, err = client.NewOAuthSSEClient(*config.Service.URL, oauthConfig,
				transport.WithHTTPClient(httpClient),
				transport.WithHeaders(headers),
			)
		} else {
			mcpClient, err = client.NewSSEMCPClient(*config.Service.URL,
				client.WithHTTPClient(httpClient),
				client.WithHeaders(headers),
			)
		}
		if err != nil {
			return nil, fmt.Errorf("failed to create SSE client: %w", err)
		}
	case types.MCPTransportHTTPStreamable:
		if config.Service.URL == nil || *config.Service.URL == "" {
			return nil, fmt.Errorf("URL is required for HTTP Streamable transport")
		}
		if useOAuth {
			mcpClient, err = client.NewOAuthStreamableHttpClient(*config.Service.URL, oauthConfig,
				transport.WithHTTPBasicClient(httpClient),
				transport.WithHTTPHeaders(headers),
			)
		} else {
			// For HTTP streamable, we need to use transport options
			mcpClient, err = client.NewStreamableHttpClient(*config.Service.URL,
				transport.WithHTTPBasicClient(httpClient),
				transport.WithHTTPHeaders(headers),
			)
		}
		if err != nil {
			return nil, fmt.Errorf("failed to create HTTP streamable client: %w", err)
		}
	case types.MCPTransportStdio:
		// Stdio transport is disabled for security reasons (potential command injection vulnerabilities)
		return nil, fmt.Errorf("stdio transport is disabled for security reasons; please use SSE or HTTP Streamable transport instead")
	default:
		return nil, ErrUnsupportedTransport
	}

	instance := &mcpGoClient{
		service: config.Service,
		client:  mcpClient,
	}
	if useOAuth {
		instance.oauth = newOAuthRuntime(
			config.OAuthRepo,
			config.TenantID,
			config.Principal,
			config.Service.ID,
			*config.Service.URL,
			oauthConfig,
		)
	}
	mcpClient.OnConnectionLost(instance.onConnectionLost)
	return instance, nil
}

// buildOAuthConfig returns the OAuth configuration for an OAuth-enabled MCP
// service, or (_, false, nil) when the service does not use OAuth. It loads
// the dynamically-registered client_id and wires a per-user token store so
// the transport injects the invoking user's bearer token and refreshes it.
func buildOAuthConfig(config *ClientConfig, httpClient *http.Client) (transport.OAuthConfig, bool, error) {
	svc := config.Service
	if !svc.AuthConfig.IsOAuth() {
		return transport.OAuthConfig{}, false, nil
	}
	if config.OAuthRepo == nil {
		return transport.OAuthConfig{}, false, fmt.Errorf("OAuth repository is required for OAuth MCP services")
	}
	principal := config.Principal.Normalize()
	if !principal.Valid() && config.UserID != "" {
		principal = types.Principal{Type: types.PrincipalWebUser, ID: config.UserID}.Normalize()
	}
	if !principal.Valid() {
		return transport.OAuthConfig{}, false, fmt.Errorf("principal context is required to connect to an OAuth MCP service")
	}
	config.Principal = principal

	oauthCfg := transport.OAuthConfig{
		Scopes:                svc.AuthConfig.Scopes,
		TokenStore:            newManagedTokenStore(config.OAuthRepo, config.TenantID, principal, svc.ID),
		PKCEEnabled:           true,
		AuthServerMetadataURL: svc.AuthConfig.AuthServerMetadataURL,
		HTTPClient:            httpClient,
	}
	if regClient, err := config.OAuthRepo.GetClient(context.Background(), config.TenantID, svc.ID); err == nil && regClient != nil {
		oauthCfg.ClientID = regClient.ClientID
		oauthCfg.ClientSecret = regClient.ClientSecret
		oauthCfg.RedirectURI = regClient.RedirectURI
	}
	return oauthCfg, true, nil
}

// onConnectionLost callback when the connection is lost
func (c *mcpGoClient) onConnectionLost(err error) {
	_ = c.Disconnect()
	logger.Warnf(context.Background(), "MCP server connection has been lost, URL:%s, error:%v", *c.service.URL, err)
}

// checkErrorAndDisconnectIfNeeded checks for transport errors that indicate the
// session is no longer valid and proactively disconnects the client so that
// subsequent GetOrCreateClient calls will establish a fresh connection.
// Both SSE and HTTP Streamable transports use server-assigned sessions
// (via Mcp-Session-Id header) that can expire or be invalidated.
func (c *mcpGoClient) checkErrorAndDisconnectIfNeeded(err error) {
	var transportErr *transport.Error
	if !errors.As(err, &transportErr) || transportErr.Err == nil {
		return
	}
	errMsg := transportErr.Err.Error()
	// Known session invalidation errors from MCP servers:
	//   - "Invalid session ID"  — server recognises the header but rejects the value
	//   - "No active connection" — server has no record of the session at all
	if strings.Contains(errMsg, "Invalid session ID") ||
		strings.Contains(errMsg, "No active connection") {
		_ = c.Disconnect()
	}
}

// oauthCall runs one MCP operation with WeKnora-owned token lifecycle checks.
// A resource-server 401 forces exactly one refresh and one retry. Other errors
// are never retried, which avoids duplicating tool side effects after ambiguous
// network failures.
func oauthCall[T any](ctx context.Context, c *mcpGoClient, operation func() (T, error)) (T, error) {
	var zero T
	if c.oauth != nil {
		if err := c.oauth.ensureFresh(ctx, false, nil); err != nil {
			return zero, err
		}
	}
	result, err := operation()
	if err == nil || c.oauth == nil || !isOAuthAuthorizationFailure(err) {
		return result, err
	}
	if refreshErr := c.oauth.ensureFresh(ctx, true, client.GetOAuthHandler(err)); refreshErr != nil {
		return zero, refreshErr
	}
	return operation()
}

// Connect establishes connection to the MCP service
func (c *mcpGoClient) Connect(ctx context.Context) error {
	if c.connected.Load() {
		return ErrAlreadyConnected
	}

	_, err := oauthCall(ctx, c, func() (struct{}, error) {
		return struct{}{}, c.client.Start(ctx)
	})
	if err != nil {
		if oerr := asOAuthRequired(err); oerr != nil {
			return oerr
		}
		return fmt.Errorf("failed to start client: %w", err)
	}
	c.connected.Store(true)
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
	if !c.connected.CompareAndSwap(true, false) {
		return nil
	}
	c.initialized.Store(false)

	// Close the client
	if c.client != nil {
		c.client.Close()
	}
	return nil
}

// Initialize performs the MCP initialize handshake
func (c *mcpGoClient) Initialize(ctx context.Context) (*InitializeResult, error) {
	if !c.connected.Load() {
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

	result, err := oauthCall(ctx, c, func() (*mcp.InitializeResult, error) {
		return c.client.Initialize(ctx, req)
	})
	if err != nil {
		c.checkErrorAndDisconnectIfNeeded(err)
		if oerr := asOAuthRequired(err); oerr != nil {
			return nil, oerr
		}
		return nil, fmt.Errorf("failed to initialize: %w", err)
	}

	c.initialized.Store(true)
	c.metadataMu.Lock()
	c.instructions = result.Instructions
	c.metadataMu.Unlock()
	serviceName := secutils.SanitizeForLog(c.service.Name)
	logger.Debugf(
		ctx,
		"MCP initialize handshake service=%s protocol=%s name=%s version=%s title=%s",
		serviceName,
		result.ProtocolVersion,
		secutils.SanitizeForLog(result.ServerInfo.Name),
		secutils.SanitizeForLog(result.ServerInfo.Version),
		secutils.SanitizeForLog(result.ServerInfo.Title),
	)
	if result.Instructions == "" && result.ServerInfo.Description == "" {
		logger.Debugf(
			ctx,
			"MCP initialize optional docs absent service=%s description_len=0 instructions_len=0",
			serviceName,
		)
	} else {
		logger.Debugf(
			ctx,
			"MCP initialize docs service=%s description_len=%d instructions_len=%d instructions_preview=%q",
			serviceName,
			len(result.ServerInfo.Description),
			len(result.Instructions),
			mcpTextPreview(result.Instructions, 240),
		)
	}

	return &InitializeResult{
		ProtocolVersion: result.ProtocolVersion,
		Instructions:    result.Instructions,
		ServerInfo: ServerInfo{
			Name:        result.ServerInfo.Name,
			Version:     result.ServerInfo.Version,
			Title:       result.ServerInfo.Title,
			Description: result.ServerInfo.Description,
		},
	}, nil
}

// ServerInstructions retains server-wide MCP documentation from initialize.
// It is separate from credentials and can accompany model-facing tools.
func (c *mcpGoClient) ServerInstructions() string {
	c.metadataMu.RLock()
	defer c.metadataMu.RUnlock()
	return c.instructions
}

// ListTools retrieves the list of available tools
func (c *mcpGoClient) ListTools(ctx context.Context) ([]*types.MCPTool, error) {
	if !c.initialized.Load() {
		return nil, ErrNotConnected
	}

	tools, err := oauthCall(ctx, c, func() ([]*types.MCPTool, error) {
		return c.listRawTools(ctx)
	})
	if err != nil {
		c.checkErrorAndDisconnectIfNeeded(err)
		return nil, fmt.Errorf("failed to list tools: %w", err)
	}

	return tools, nil
}

// A tenant-supplied MCP endpoint is untrusted, and the whole directory is held
// in memory and schema-compiled afterwards. Bound protocol pagination so a
// hostile or looping server cannot grow it without limit under the list
// timeout; an over-limit directory is rejected rather than published in part.
const (
	maxToolListPages   = 100
	maxToolsPerService = 2000
	maxToolSchemaBytes = 256 * 1024
)

// The SDK's typed ToolInputSchema discards unknown root keywords (e.g. oneOf)
// and rewrites definitions to $defs without rewriting references. Read raw
// schemas through the same authenticated transport instead. String request IDs
// cannot collide with the SDK client's numeric IDs.
func (c *mcpGoClient) listRawTools(ctx context.Context) ([]*types.MCPTool, error) {
	var tools []*types.MCPTool
	cursor := ""
	seen := make(map[string]bool)
	for pages := 0; ; pages++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if pages >= maxToolListPages {
			return nil, fmt.Errorf("tools/list exceeded %d pages", maxToolListPages)
		}
		response, err := c.client.GetTransport().SendRequest(ctx, transport.JSONRPCRequest{
			JSONRPC: mcp.JSONRPC_VERSION,
			ID:      mcp.NewRequestId("weknora-tools-" + uuid.NewString()),
			Method:  "tools/list",
			Params: struct {
				Cursor string `json:"cursor,omitempty"`
			}{cursor},
		})
		if err != nil {
			return nil, transport.NewError(err)
		}
		if response == nil {
			return nil, fmt.Errorf("empty tools/list response")
		}
		if response.Error != nil {
			return nil, response.Error.AsError()
		}
		var page struct {
			Tools []struct {
				Name        string          `json:"name"`
				Description string          `json:"description"`
				InputSchema json.RawMessage `json:"inputSchema"`
			} `json:"tools"`
			NextCursor string `json:"nextCursor"`
		}
		if err := json.Unmarshal(response.Result, &page); err != nil {
			return nil, fmt.Errorf("invalid tools/list response: %w", err)
		}
		for _, tool := range page.Tools {
			if len(tool.InputSchema) > maxToolSchemaBytes {
				return nil, fmt.Errorf(
					"tool %q input schema exceeds %d bytes", tool.Name, maxToolSchemaBytes,
				)
			}
			tools = append(
				tools,
				&types.MCPTool{Name: tool.Name, Description: tool.Description, InputSchema: tool.InputSchema},
			)
		}
		if len(tools) > maxToolsPerService {
			return nil, fmt.Errorf("tools/list exceeded %d tools", maxToolsPerService)
		}
		if page.NextCursor == "" {
			return tools, nil
		}
		if seen[page.NextCursor] {
			return nil, fmt.Errorf("tools/list returned a repeated cursor")
		}
		seen[page.NextCursor] = true
		cursor = page.NextCursor
	}
}

// ListResources retrieves the list of available resources
func (c *mcpGoClient) ListResources(ctx context.Context) ([]*types.MCPResource, error) {
	if !c.initialized.Load() {
		return nil, ErrNotConnected
	}

	req := mcp.ListResourcesRequest{}
	result, err := oauthCall(ctx, c, func() (*mcp.ListResourcesResult, error) {
		return c.client.ListResources(ctx, req)
	})
	if err != nil {
		c.checkErrorAndDisconnectIfNeeded(err)
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
	if !c.initialized.Load() {
		return nil, ErrNotConnected
	}

	req := mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name:      name,
			Arguments: args,
		},
	}

	result, err := oauthCall(ctx, c, func() (*mcp.CallToolResult, error) {
		return c.client.CallTool(ctx, req)
	})
	if err != nil {
		c.checkErrorAndDisconnectIfNeeded(err)
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
	if !c.initialized.Load() {
		return nil, ErrNotConnected
	}

	req := mcp.ReadResourceRequest{
		Params: mcp.ReadResourceParams{
			URI: uri,
		},
	}

	result, err := oauthCall(ctx, c, func() (*mcp.ReadResourceResult, error) {
		return c.client.ReadResource(ctx, req)
	})
	if err != nil {
		c.checkErrorAndDisconnectIfNeeded(err)
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
	return c.connected.Load()
}

// GetServiceID returns the service ID
func (c *mcpGoClient) GetServiceID() string {
	return c.service.ID
}

func mcpTextPreview(s string, maxRunes int) string {
	s = secutils.SanitizeForLog(s)
	if maxRunes <= 0 {
		return ""
	}
	runes := []rune(s)
	if len(runes) <= maxRunes {
		return s
	}
	return string(runes[:maxRunes]) + "..."
}
