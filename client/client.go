// Package client provides the implementation for interacting with the WeKnora API
// This package encapsulates CRUD operations for server resources and provides a friendly interface for callers
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// Client is the client for interacting with the WeKnora service.
//
// Authentication uses one of two credential kinds:
//   - API key (long-lived, set via X-API-Key)        — see WithAPIKey
//   - Bearer JWT (short-lived, set via Authorization)— see WithBearerToken
//
// Both may be configured simultaneously; X-API-Key takes precedence at the
// HTTP layer. The legacy WithToken is kept as an alias for WithAPIKey for two
// minor versions of compatibility.
type Client struct {
	baseURL    string
	httpClient *http.Client
	// streamTimeout is zero by default so long-lived SSE responses are not
	// severed by the ordinary request timeout. An explicit WithTimeout option
	// sets both timeouts, preserving the public option's all-request semantics.
	streamTimeout time.Duration
	apiKey        string
	bearerToken   string
	tenantID      *uint64
}

// ClientOption defines client configuration options
type ClientOption func(*Client)

// WithTimeout sets the timeout for ordinary and streaming HTTP requests.
// Streaming requests have no timeout by default; callers that set this option
// explicitly are asking for the same upper bound to apply to SSE streams.
func WithTimeout(timeout time.Duration) ClientOption {
	return func(c *Client) {
		c.httpClient.Timeout = timeout
		c.streamTimeout = timeout
	}
}

// WithTransport overrides the underlying http.RoundTripper on the SDK's
// HTTP client. The default Timeout (and any WithTimeout override) is
// preserved. Intended for callers (the CLI's authretry layer; metrics or
// signing middleware) that want to wrap the transport without replacing
// the whole http.Client.
//
// Passing nil restores http.DefaultTransport.
func WithTransport(rt http.RoundTripper) ClientOption {
	return func(c *Client) {
		c.httpClient.Transport = rt
	}
}

// WithAPIKey sets the long-lived API key sent as the X-API-Key header.
func WithAPIKey(key string) ClientOption {
	return func(c *Client) {
		c.apiKey = key
	}
}

// WithBearerToken sets the JWT bearer token sent as the Authorization header.
// Used after a successful auth.login to call authenticated endpoints with the
// short-lived access token.
func WithBearerToken(token string) ClientOption {
	return func(c *Client) {
		c.bearerToken = token
	}
}

// WithToken is the v0.x compatibility alias for WithAPIKey. Prefer WithAPIKey
// (or WithBearerToken for JWT). Will be removed in the next major; the alias
// is preserved for two minor versions per ADR.
//
// Deprecated: use WithAPIKey for X-API-Key, WithBearerToken for JWT.
func WithToken(token string) ClientOption {
	return WithAPIKey(token)
}

// WithTenantID sets X-Tenant-ID on every request. Use only for explicit
// cross-tenant access by callers with CanAccessAllTenants — the server's
// auth middleware runs the cross-tenant gate whenever this header is
// present on a bearer request, even when the value matches the credential's
// own tenant, and 403s normal users. JWT bearer tokens and tenant-scoped
// API keys carry tenant identity intrinsically, so the header is redundant
// (and harmful) for default-tenant traffic. Per-request override via the
// "TenantID" context value still applies.
func WithTenantID(tenantID uint64) ClientOption {
	return func(c *Client) {
		c.tenantID = &tenantID
	}
}

// NewClient creates a new client instance
func NewClient(baseURL string, options ...ClientOption) *Client {
	client := &Client{
		baseURL: baseURL,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}

	for _, option := range options {
		option(client)
	}

	return client
}

// buildRequest constructs the authenticated *http.Request shared by doRequest
// and doRequestStream: serializes the JSON body, composes URL + query, and
// applies auth headers.
func (c *Client) buildRequest(ctx context.Context,
	method, path string, body interface{}, query url.Values,
) (*http.Request, error) {
	var reqBody io.Reader
	if body != nil {
		jsonData, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("failed to serialize request body: %w", err)
		}
		reqBody = bytes.NewBuffer(jsonData)
	}

	u := fmt.Sprintf("%s%s", c.baseURL, path)
	if len(query) > 0 {
		u = fmt.Sprintf("%s?%s", u, query.Encode())
	}

	req, err := http.NewRequestWithContext(ctx, method, u, reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	c.applyAuthHeaders(ctx, req)
	return req, nil
}

// doRequest executes an HTTP request subject to the client's blanket Timeout
// (default 30s). Use for ordinary request/response calls.
func (c *Client) doRequest(ctx context.Context,
	method, path string, body interface{}, query url.Values,
) (*http.Response, error) {
	req, err := c.buildRequest(ctx, method, path, body, query)
	if err != nil {
		return nil, err
	}
	return c.httpClient.Do(req)
}

// doRequestStream executes a streaming (SSE) request without the client's
// default 30-second blanket Timeout. http.Client.Timeout covers reading the
// response body, so applying that default would sever long-running chat /
// session-ask / continue-stream responses. Stream lifetime is governed by ctx
// unless the caller explicitly supplied WithTimeout, in which case that upper
// bound is preserved. The Transport is shared with c.httpClient so connection
// pooling is unaffected.
func (c *Client) doRequestStream(ctx context.Context,
	method, path string, body interface{}, query url.Values,
) (*http.Response, error) {
	req, err := c.buildRequest(ctx, method, path, body, query)
	if err != nil {
		return nil, err
	}
	sc := *c.httpClient // shallow copy: shares Transport, override Timeout
	sc.Timeout = c.streamTimeout
	return sc.Do(req)
}

// applyAuthHeaders sets X-API-Key, Authorization, X-Request-ID, and X-Tenant-ID
// on req based on client config and ctx values. Used by doRequest and any
// caller that builds its own *http.Request (currently CreateKnowledgeFromFile,
// which uses multipart and can't go through doRequest).
func (c *Client) applyAuthHeaders(ctx context.Context, req *http.Request) {
	if c.apiKey != "" {
		req.Header.Set("X-API-Key", c.apiKey)
	}
	if c.bearerToken != "" {
		req.Header.Set("Authorization", "Bearer "+c.bearerToken)
	}
	if requestID := ctx.Value("RequestID"); requestID != nil {
		if s, ok := requestID.(string); ok {
			req.Header.Set("X-Request-ID", s)
		}
	}

	tenantID := c.tenantID
	if ctxTenant := ctx.Value("TenantID"); ctxTenant != nil {
		switch v := ctxTenant.(type) {
		case *uint64:
			if v != nil {
				tenantID = v
			}
		case uint64:
			tenantID = &v
		case string:
			if parsed, err := strconv.ParseUint(v, 10, 64); err == nil {
				tenantID = &parsed
			}
		}
	}
	if tenantID != nil {
		req.Header.Set("X-Tenant-ID", strconv.FormatUint(*tenantID, 10))
	}
}

// Raw performs a raw HTTP request against the WeKnora API with the client's
// auth headers, X-Request-ID, and X-Tenant-ID injection applied.
//
// Experimental: this method is intended for one-off integrations and the
// `weknora api` CLI passthrough. The signature, return type, and behavior may
// change in any minor version. Prefer typed methods (ListKnowledgeBases,
// GetKnowledgeBase, etc.) when they exist.
func (c *Client) Raw(ctx context.Context, method, path string, body interface{}) (*http.Response, error) {
	return c.doRequest(ctx, method, path, body, nil)
}

// parseResponse parses an HTTP response
func parseResponse(resp *http.Response, target interface{}) error {
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("HTTP error %d: %s", resp.StatusCode, string(body))
	}

	if target == nil {
		return nil
	}

	return json.NewDecoder(resp.Body).Decode(target)
}
