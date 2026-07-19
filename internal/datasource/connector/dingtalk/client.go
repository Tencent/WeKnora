package dingtalk

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/Tencent/WeKnora/internal/datasource"
	"github.com/Tencent/WeKnora/internal/logger"
)

const (
	defaultTimeout = 30 * time.Second
	userAgent      = "WeKnora-DingTalk-Connector/1.0"

	// DingTalk API pagination limits
	maxWorkspacesPerPage = 30 // GET /v2.0/wiki/workspaces maxResults max is 30
	maxNodesPerPage      = 50 // GET /v2.0/wiki/nodes maxResults max is 50

	// rateLimitDelay is the minimum interval between consecutive API calls
	// to stay within DingTalk's 20 QPS limit for standard edition.
	rateLimitDelay = 100 * time.Millisecond
)

// Client wraps the DingTalk Open Platform API for knowledge base operations.
type Client struct {
	baseURL      string
	clientID     string // appKey
	clientSecret string // appSecret
	operatorID   string // unionId of the operating user

	httpClient *http.Client

	// Token cache (thread-safe)
	tokenMu    sync.RWMutex
	tokenCache string
	tokenExpAt time.Time

	// lastRequestTime tracks the last API request time for rate limiting
	lastReqMu   sync.Mutex
	lastReqTime time.Time
}

type nodeListFailure struct {
	Node node
	Err  error
}

type partialNodeListError struct {
	Failures []nodeListFailure
}

func (e *partialNodeListError) Error() string {
	if e == nil || len(e.Failures) == 0 {
		return "partial node listing failed"
	}
	parts := make([]string, 0, len(e.Failures))
	for _, failure := range e.Failures {
		parts = append(parts, failure.Err.Error())
	}
	return strings.Join(parts, "; ")
}

// NewClient creates a new DingTalk API client.
func NewClient(config *Config) *Client {
	return &Client{
		baseURL:      config.GetBaseURL(),
		clientID:     config.ClientID,
		clientSecret: config.ClientSecret,
		operatorID:   config.OperatorID,
		httpClient:   datasource.NewConnectorHTTPClient(defaultTimeout),
	}
}

// rateLimitWait ensures we don't exceed the QPS limit by sleeping if needed.
func (c *Client) rateLimitWait(ctx context.Context) error {
	c.lastReqMu.Lock()
	defer c.lastReqMu.Unlock()

	elapsed := time.Since(c.lastReqTime)
	if elapsed < rateLimitDelay {
		remaining := rateLimitDelay - elapsed
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(remaining):
		}
	}
	c.lastReqTime = time.Now()
	return nil
}

// getAccessToken retrieves (or returns cached) access token.
// DingTalk tokens expire in 2 hours; we cache with a 5-minute safety margin.
func (c *Client) getAccessToken(ctx context.Context) (string, error) {
	// Fast path: read lock
	c.tokenMu.RLock()
	if c.tokenCache != "" && time.Now().Before(c.tokenExpAt) {
		token := c.tokenCache
		c.tokenMu.RUnlock()
		return token, nil
	}
	c.tokenMu.RUnlock()

	// Slow path: write lock (double-checked locking)
	c.tokenMu.Lock()
	defer c.tokenMu.Unlock()

	// Re-check after acquiring write lock
	if c.tokenCache != "" && time.Now().Before(c.tokenExpAt) {
		return c.tokenCache, nil
	}

	payload, _ := json.Marshal(map[string]string{
		"appKey":    c.clientID,
		"appSecret": c.clientSecret,
	})

	url := c.baseURL + "/v1.0/oauth2/accessToken"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return "", fmt.Errorf("create token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("request token: %w", err)
	}
	defer resp.Body.Close()

	body, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		return "", fmt.Errorf("read token response body: %w", readErr)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("dingtalk auth error: status=%d body=%s", resp.StatusCode, truncate(string(body), 500))
	}

	var result tokenResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return "", fmt.Errorf("decode token response: %w", err)
	}
	if result.AccessToken == "" {
		return "", fmt.Errorf("empty access token from dingtalk")
	}

	c.tokenCache = result.AccessToken
	ttl := time.Duration(result.ExpireIn) * time.Second
	if ttl > 5*time.Minute {
		ttl -= 5 * time.Minute
	}
	c.tokenExpAt = time.Now().Add(ttl)

	logger.Infof(ctx, "[DingTalk] got accessToken: %s expireIn=%ds",
		redactToken(result.AccessToken), result.ExpireIn)

	return c.tokenCache, nil
}

// doRequest executes an authenticated API request and decodes the JSON response.
// Includes retry logic for transient errors (429, 5xx).
func (c *Client) doRequest(ctx context.Context, method, path string, body interface{}, result interface{}) error {
	const (
		maxRetries    = 3
		max5xxRetries = 1
		retry5xxDelay = 2 * time.Second
	)
	var lastErr error
	backoff := []time.Duration{2 * time.Second, 4 * time.Second, 8 * time.Second}

	for attempt := 0; attempt <= maxRetries; attempt++ {
		// Rate limit before each request
		if err := c.rateLimitWait(ctx); err != nil {
			return err
		}

		token, err := c.getAccessToken(ctx)
		if err != nil {
			return err
		}

		var bodyReader io.Reader
		if body != nil {
			bodyBytes, err := json.Marshal(body)
			if err != nil {
				return fmt.Errorf("marshal request body: %w", err)
			}
			bodyReader = bytes.NewReader(bodyBytes)
		}

		reqURL := c.baseURL + path
		req, err := http.NewRequestWithContext(ctx, method, reqURL, bodyReader)
		if err != nil {
			return fmt.Errorf("create request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json; charset=utf-8")
		req.Header.Set("x-acs-dingtalk-access-token", token)
		req.Header.Set("User-Agent", userAgent)

		if attempt == 0 {
			logger.Infof(ctx, "[DingTalk] %s %s", method, path)
		} else {
			logger.Infof(ctx, "[DingTalk] %s %s (retry %d/%d)", method, path, attempt, maxRetries)
		}

		resp, err := c.httpClient.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("execute request: %w", err)
			if attempt < maxRetries {
				if sErr := sleepCtx(ctx, backoff[attempt]); sErr != nil {
					return sErr
				}
				continue
			}
			return lastErr
		}

		respBody, readErr := io.ReadAll(resp.Body)
		resp.Body.Close()
		if readErr != nil {
			lastErr = fmt.Errorf("read response body: %w", readErr)
			if attempt < maxRetries {
				if sErr := sleepCtx(ctx, backoff[attempt]); sErr != nil {
					return sErr
				}
				continue
			}
			return lastErr
		}

		bodyPreview := truncate(string(respBody), 500)
		logger.Infof(ctx, "[DingTalk] %s %s → status=%d bodyLen=%d body=%s",
			method, path, resp.StatusCode, len(respBody), bodyPreview)

		if resp.StatusCode == 429 {
			wait := parseRetryAfter(resp.Header.Get("Retry-After"), backoff[min(attempt, len(backoff)-1)])
			lastErr = fmt.Errorf("dingtalk rate limited: status=429 body=%s", bodyPreview)
			if attempt < maxRetries {
				if sErr := sleepCtx(ctx, wait); sErr != nil {
					return sErr
				}
				continue
			}
			return lastErr
		}

		if resp.StatusCode >= 500 && resp.StatusCode < 600 {
			lastErr = fmt.Errorf("dingtalk server error: status=%d body=%s", resp.StatusCode, bodyPreview)
			if attempt < max5xxRetries {
				if sErr := sleepCtx(ctx, retry5xxDelay); sErr != nil {
					return sErr
				}
				continue
			}
			return lastErr
		}

		// 401/403 → surface as ErrInvalidCredentials so DataSourceService can
		// distinguish bad-credentials from transient failures and auto-flag the source.
		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
			return fmt.Errorf("%w: status=%d body=%s", datasource.ErrInvalidCredentials, resp.StatusCode, bodyPreview)
		}

		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return fmt.Errorf("dingtalk api error: status=%d body=%s", resp.StatusCode, bodyPreview)
		}

		if result != nil {
			if err := json.Unmarshal(respBody, result); err != nil {
				return fmt.Errorf("decode response: %w", err)
			}
		}
		return nil
	}
	return lastErr
}

// Ping verifies the credentials by attempting to get an access token.
func (c *Client) Ping(ctx context.Context) error {
	_, err := c.getAccessToken(ctx)
	return err
}

// ListWorkspaces returns all knowledge bases accessible to the operator.
// Uses nextToken-based pagination (max 30 per page).
// Protected against infinite pagination loops (max 1000 pages).
func (c *Client) ListWorkspaces(ctx context.Context) ([]workspace, error) {
	var allWorkspaces []workspace
	nextToken := ""

	const maxPages = 1000
	for page := 0; page < maxPages; page++ {
		path := "/v2.0/wiki/workspaces?" + url.Values{
			"operatorId": {c.operatorID},
			"maxResults": {fmt.Sprintf("%d", maxWorkspacesPerPage)},
		}.Encode()
		if nextToken != "" {
			path += "&nextToken=" + url.QueryEscape(nextToken)
		}

		var resp workspaceListResponse
		if err := c.doRequest(ctx, http.MethodGet, path, nil, &resp); err != nil {
			return nil, fmt.Errorf("list workspaces: %w", err)
		}

		logger.Infof(ctx, "[DingTalk] ListWorkspaces: got %d workspaces, nextToken=%q",
			len(resp.Workspaces), resp.NextToken)

		allWorkspaces = append(allWorkspaces, resp.Workspaces...)

		if resp.NextToken == "" {
			break
		}
		nextToken = resp.NextToken
	}
	if nextToken != "" {
		logger.Warnf(ctx, "[DingTalk] ListWorkspaces: hit maxPages=%d limit, some workspaces may be missing", maxPages)
	}

	logger.Infof(ctx, "[DingTalk] ListWorkspaces: total %d workspaces", len(allWorkspaces))
	return allWorkspaces, nil
}

// GetWorkspaceRootNodeID fetches the workspace and returns its root node ID.
// This is needed because ListResources at the workspace level needs to list
// the root node's children, and the root node ID comes from the workspace metadata.
func (c *Client) GetWorkspaceRootNodeID(ctx context.Context, workspaceID string) (string, error) {
	// Fetch the workspace details to get the rootNodeId
	path := "/v2.0/wiki/workspaces/" + url.PathEscape(workspaceID) + "?" + url.Values{
		"operatorId": {c.operatorID},
	}.Encode()

	// DingTalk API returns {"workspace": {...}} nested structure for single workspace detail.
	var resp struct {
		Workspace workspace `json:"workspace"`
	}
	if err := c.doRequest(ctx, http.MethodGet, path, nil, &resp); err != nil {
		return "", fmt.Errorf("get workspace %s: %w", workspaceID, err)
	}

	if resp.Workspace.RootNodeID == "" {
		return "", fmt.Errorf("workspace %s has no root node ID", workspaceID)
	}

	logger.Infof(ctx, "[DingTalk] GetWorkspaceRootNodeID: workspace=%s rootNodeID=%s", workspaceID, resp.Workspace.RootNodeID)
	return resp.Workspace.RootNodeID, nil
}

// ListNodes returns the direct children of a parent node.
// If parentNodeID is empty, lists the root nodes of the workspace (via rootNodeId).
// Uses nextToken-based pagination (max 50 per page).
// Protected against infinite pagination loops (max 1000 pages).
func (c *Client) ListNodes(ctx context.Context, parentNodeID string) ([]node, error) {
	var allNodes []node
	nextToken := ""

	const maxPages = 1000
	for page := 0; page < maxPages; page++ {
		path := "/v2.0/wiki/nodes?" + url.Values{
			"parentNodeId": {parentNodeID},
			"operatorId":   {c.operatorID},
			"maxResults":   {fmt.Sprintf("%d", maxNodesPerPage)},
		}.Encode()
		if nextToken != "" {
			path += "&nextToken=" + url.QueryEscape(nextToken)
		}

		var resp nodeListResponse
		if err := c.doRequest(ctx, http.MethodGet, path, nil, &resp); err != nil {
			return nil, fmt.Errorf("list nodes under %s: %w", parentNodeID, err)
		}

		allNodes = append(allNodes, resp.Nodes...)

		if resp.NextToken == "" {
			break
		}
		nextToken = resp.NextToken
	}
	if nextToken != "" {
		logger.Warnf(ctx, "[DingTalk] ListNodes(%s): hit maxPages=%d limit, some nodes may be missing", parentNodeID, maxPages)
	}

	return allNodes, nil
}

// GetNode returns metadata for a single node.
func (c *Client) GetNode(ctx context.Context, nodeID string) (node, error) {
	path := "/v2.0/wiki/nodes/" + url.PathEscape(nodeID) + "?" + url.Values{
		"operatorId": {c.operatorID},
	}.Encode()

	var resp nodeDetailResponse
	if err := c.doRequest(ctx, http.MethodGet, path, nil, &resp); err != nil {
		return node{}, fmt.Errorf("get node %s: %w", nodeID, err)
	}

	return resp.Node, nil
}

// GetDocumentBlocks returns the block elements of a DingTalk document.
// Only works for documents with category "ALIDOC".
func (c *Client) GetDocumentBlocks(ctx context.Context, docKey string) ([]block, error) {
	path := "/v1.0/doc/suites/documents/" + url.PathEscape(docKey) + "/blocks?" + url.Values{
		"operatorId": {c.operatorID},
	}.Encode()

	var resp blocksResponse
	if err := c.doRequest(ctx, http.MethodGet, path, nil, &resp); err != nil {
		return nil, fmt.Errorf("get document blocks for %s: %w", docKey, err)
	}

	if !resp.Success {
		return nil, fmt.Errorf("get document blocks for %s: success=false", docKey)
	}

	return resp.Result.Data, nil
}

// ListAllNodesRecursive recursively lists all nodes under a parent node.
// It walks the tree depth-first to discover all nested documents.
// Protected against excessively deep trees (max 50 levels).
func (c *Client) ListAllNodesRecursive(ctx context.Context, rootNodeID string) ([]node, error) {
	const maxDepth = 50

	var allNodes []node
	var failures []nodeListFailure

	var walk func(nodes []node, depth int)
	walk = func(nodes []node, depth int) {
		for _, n := range nodes {
			allNodes = append(allNodes, n)

			if n.Type == "FOLDER" && n.HasChildren {
				if depth >= maxDepth {
					logger.Warnf(ctx, "[DingTalk] ListAllNodesRecursive: skipping children of %s: hit maxDepth=%d limit", n.NodeID, maxDepth)
					continue
				}
				children, err := c.ListNodes(ctx, n.NodeID)
				if err != nil {
					wrappedErr := fmt.Errorf("list children of %s: %w", n.NodeID, err)
					failures = append(failures, nodeListFailure{
						Node: n,
						Err:  wrappedErr,
					})
					logger.Warnf(ctx, "[DingTalk] partial node listing failure: parent=%s err=%v", n.NodeID, err)
					continue
				}
				walk(children, depth+1)
			}
		}
	}

	topNodes, err := c.ListNodes(ctx, rootNodeID)
	if err != nil {
		return nil, err
	}

	walk(topNodes, 1)
	if len(failures) > 0 {
		return allNodes, &partialNodeListError{Failures: failures}
	}

	return allNodes, nil
}

// --- Helper functions ---

// parseRetryAfter returns the Retry-After duration from the header, or fallback if unparseable.
func parseRetryAfter(header string, fallback time.Duration) time.Duration {
	if header == "" {
		return fallback
	}
	if secs, err := time.ParseDuration(header + "s"); err == nil {
		if secs <= 0 {
			return 100 * time.Millisecond
		}
		return secs
	}
	return fallback
}

// sleepCtx pauses for d, returning early if ctx is cancelled.
func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// truncate returns s truncated to maxLen bytes with "..." appended if longer.
// Truncates at a UTF-8 rune boundary to avoid producing invalid UTF-8,
// which is important when logging Chinese API error messages.
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	// Peel trailing bytes until we're at a valid rune boundary.
	end := maxLen
	for end > 0 && !isRuneStart(s[end]) {
		end--
	}
	if end == 0 {
		// The first maxLen bytes are all continuation bytes; return as-is
		// since the full string was already valid UTF-8.
		return s[:maxLen] + "..."
	}
	return s[:end] + "..."
}

// isRuneStart reports whether the byte at position i could be the start of a
// UTF-8 encoded rune (i.e. not a continuation byte 10xxxxxx).
func isRuneStart(b byte) bool {
	return b&0xC0 != 0x80
}
