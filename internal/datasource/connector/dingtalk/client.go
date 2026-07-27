package dingtalk

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/Tencent/WeKnora/internal/datasource"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/utils"
	"golang.org/x/sync/singleflight"
)

const (
	defaultTimeout  = 30 * time.Second
	defaultPageSize = 30
	userAgent       = "WeKnora-DingTalk-Connector/1.0"
)

// client wraps the DingTalk Open API.
type client struct {
	baseURL      string
	clientID     string
	clientSecret string
	accessToken  string
	tokenExpiry  time.Time
	httpClient   *http.Client
	mu           sync.Mutex

	logTokenOnce sync.Once
}

type cachedAccessToken struct {
	token  string
	expiry time.Time
}

var accessTokenCache = struct {
	sync.Mutex
	entries map[string]cachedAccessToken
}{
	entries: make(map[string]cachedAccessToken),
}

var accessTokenRefreshGroup singleflight.Group

// newClient constructs a client with configuration.
func newClient(cfg *Config) *client {
	httpCfg := utils.DefaultSSRFSafeHTTPClientConfig()
	httpCfg.Timeout = defaultTimeout
	return &client{
		baseURL:      cfg.GetBaseURL(),
		clientID:     cfg.ClientID,
		clientSecret: cfg.ClientSecret,
		httpClient:   utils.NewSSRFSafeHTTPClient(httpCfg),
	}
}

func (c *client) cacheKey() string {
	sum := sha256.Sum256([]byte(c.clientSecret))
	return fmt.Sprintf("%s\x00%s\x00%x", c.baseURL, c.clientID, sum[:8])
}

func tokenUsable(token string, expiry time.Time) bool {
	return token != "" && time.Until(expiry) > 5*time.Minute
}

func loadCachedAccessToken(key string) (string, time.Time, bool) {
	accessTokenCache.Lock()
	defer accessTokenCache.Unlock()
	entry, ok := accessTokenCache.entries[key]
	if !ok || !tokenUsable(entry.token, entry.expiry) {
		if ok {
			delete(accessTokenCache.entries, key)
		}
		return "", time.Time{}, false
	}
	return entry.token, entry.expiry, true
}

func storeCachedAccessToken(key, token string, expiry time.Time) {
	accessTokenCache.Lock()
	defer accessTokenCache.Unlock()
	accessTokenCache.entries[key] = cachedAccessToken{token: token, expiry: expiry}
}

// ensureAccessToken refreshes the access token if expired or not set.
func (c *client) ensureAccessToken(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Check if current token is still valid (with 5 minute buffer)
	if tokenUsable(c.accessToken, c.tokenExpiry) {
		return nil
	}

	c.logTokenOnce.Do(func() {
		logger.Infof(ctx, "[DingTalk] client configured clientId=%s base=%s", redactClientID(c.clientID), c.baseURL)
	})

	cacheKey := c.cacheKey()
	if token, expiry, ok := loadCachedAccessToken(cacheKey); ok {
		c.accessToken = token
		c.tokenExpiry = expiry
		return nil
	}

	value, err, _ := accessTokenRefreshGroup.Do(cacheKey, func() (interface{}, error) {
		if token, expiry, ok := loadCachedAccessToken(cacheKey); ok {
			return cachedAccessToken{token: token, expiry: expiry}, nil
		}
		return c.fetchAccessToken(ctx, cacheKey)
	})
	if err != nil {
		return err
	}
	token := value.(cachedAccessToken)
	c.accessToken = token.token
	c.tokenExpiry = token.expiry
	return nil
}

func (c *client) fetchAccessToken(ctx context.Context, cacheKey string) (cachedAccessToken, error) {
	body := accessTokenRequest{
		AppKey:    c.clientID,
		AppSecret: c.clientSecret,
	}
	bodyBytes, _ := json.Marshal(body)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1.0/oauth2/accessToken", bytes.NewReader(bodyBytes))
	if err != nil {
		return cachedAccessToken{}, fmt.Errorf("create token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	req.Header.Set("User-Agent", userAgent)

	logger.Infof(ctx, "[DingTalk] refreshing access token for clientId=%s", redactClientID(c.clientID))

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return cachedAccessToken{}, fmt.Errorf("execute token request: %w", err)
	}
	defer resp.Body.Close()

	respBody, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		return cachedAccessToken{}, fmt.Errorf("read token response: %w", readErr)
	}

	if resp.StatusCode != http.StatusOK {
		var apiErr dingtalkErrorResponse
		_ = json.Unmarshal(respBody, &apiErr)
		if msg := apiErr.errorMessage(); msg != "" {
			return cachedAccessToken{}, fmt.Errorf("dingtalk token error: status=%d code=%s msg=%s", resp.StatusCode, apiErr.errorCode(), msg)
		}
		return cachedAccessToken{}, fmt.Errorf("dingtalk token error: status=%d", resp.StatusCode)
	}

	var tokenResp accessTokenResponse
	if err := json.Unmarshal(respBody, &tokenResp); err != nil {
		return cachedAccessToken{}, fmt.Errorf("decode token response: %w", err)
	}

	if tokenResp.AccessToken == "" {
		return cachedAccessToken{}, fmt.Errorf("%w: empty access token received", datasource.ErrInvalidCredentials)
	}

	token := cachedAccessToken{
		token:  tokenResp.AccessToken,
		expiry: time.Now().Add(time.Duration(tokenResp.ExpireIn) * time.Second),
	}
	storeCachedAccessToken(cacheKey, token.token, token.expiry)

	logger.Infof(ctx, "[DingTalk] access token refreshed, expires in %d seconds", tokenResp.ExpireIn)
	return token, nil
}

// doRequest executes an authenticated request with automatic token refresh.
func (c *client) doRequest(ctx context.Context, method, path string, queryParams map[string]string, body interface{}, result interface{}) error {
	// Ensure we have a valid access token
	if err := c.ensureAccessToken(ctx); err != nil {
		return err
	}

	const (
		maxRetries    = 3
		max5xxRetries = 1
		retry5xxDelay = 2 * time.Second
	)
	var lastErr error
	backoff := []time.Duration{1 * time.Second, 2 * time.Second, 4 * time.Second}

	for attempt := 0; attempt <= maxRetries; attempt++ {
		// Build URL with query params
		reqURL := c.baseURL + path
		if len(queryParams) > 0 {
			q := url.Values{}
			for k, v := range queryParams {
				if v != "" {
					q.Set(k, v)
				}
			}
			if len(q) > 0 {
				reqURL += "?" + q.Encode()
			}
		}

		var bodyReader io.Reader
		if body != nil {
			bodyBytes, _ := json.Marshal(body)
			bodyReader = bytes.NewReader(bodyBytes)
		}

		req, err := http.NewRequestWithContext(ctx, method, reqURL, bodyReader)
		if err != nil {
			return fmt.Errorf("create request: %w", err)
		}

		req.Header.Set("x-acs-dingtalk-access-token", c.accessToken)
		req.Header.Set("Content-Type", "application/json; charset=utf-8")
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

		bodyBytes, readErr := io.ReadAll(resp.Body)
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

		apiErr := parseDingTalkError(bodyBytes)
		logger.Infof(ctx, "[DingTalk] %s %s → status=%d bodyLen=%d",
			method, path, resp.StatusCode, len(bodyBytes))

		// Handle rate limiting
		if isDingTalkRateLimit(resp.StatusCode, apiErr) {
			wait := parseRetryAfter(resp.Header.Get("Retry-After"), backoff[min(attempt, len(backoff)-1)])
			lastErr = fmt.Errorf("dingtalk rate limited: status=%d code=%s", resp.StatusCode, apiErr.errorCode())
			if attempt < maxRetries {
				if sErr := sleepCtx(ctx, wait); sErr != nil {
					return sErr
				}
				continue
			}
			return lastErr
		}

		if resp.StatusCode >= 500 && resp.StatusCode < 600 {
			lastErr = fmt.Errorf("dingtalk server error: status=%d code=%s", resp.StatusCode, apiErr.errorCode())
			if attempt < max5xxRetries {
				if sErr := sleepCtx(ctx, retry5xxDelay); sErr != nil {
					return sErr
				}
				continue
			}
			return lastErr
		}

		if resp.StatusCode == http.StatusUnauthorized ||
			(resp.StatusCode == http.StatusForbidden && isCredentialError(apiErr)) {
			return fmt.Errorf("%w: status=%d code=%s msg=%s",
				datasource.ErrInvalidCredentials, resp.StatusCode, apiErr.errorCode(), apiErr.errorMessage())
		}

		if resp.StatusCode == http.StatusForbidden {
			if msg := apiErr.errorMessage(); msg != "" {
				return &dingtalkAPIError{Code: apiErr.errorCode(), Msg: msg}
			}
			return fmt.Errorf("dingtalk api forbidden: status=%d", resp.StatusCode)
		}

		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			if msg := apiErr.errorMessage(); msg != "" {
				return &dingtalkAPIError{Code: apiErr.errorCode(), Msg: msg}
			}
			return fmt.Errorf("dingtalk api error: status=%d code=%s", resp.StatusCode, apiErr.errorCode())
		}

		if result != nil && len(bodyBytes) > 0 {
			if err := json.Unmarshal(bodyBytes, result); err != nil {
				return fmt.Errorf("decode response: %w", err)
			}
		}
		return nil
	}
	return lastErr
}

func parseDingTalkError(body []byte) dingtalkErrorResponse {
	var apiErr dingtalkErrorResponse
	_ = json.Unmarshal(body, &apiErr)
	return apiErr
}

func isDingTalkRateLimit(status int, apiErr dingtalkErrorResponse) bool {
	if status == http.StatusTooManyRequests {
		return true
	}
	text := strings.ToLower(apiErr.errorCode() + " " + apiErr.errorMessage())
	return strings.Contains(text, "qpslimit") ||
		strings.Contains(text, "rate limit") ||
		strings.Contains(text, "ratelimit") ||
		strings.Contains(text, "too many requests")
}

func isCredentialError(apiErr dingtalkErrorResponse) bool {
	text := strings.ToLower(apiErr.errorCode() + " " + apiErr.errorMessage())
	return strings.Contains(text, "invalidcredential") ||
		strings.Contains(text, "invalid credentials") ||
		strings.Contains(text, "invalidaccesstoken") ||
		strings.Contains(text, "invalid access token") ||
		strings.Contains(text, "invalid token") ||
		strings.Contains(text, "unauthorized")
}

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

// truncate returns s truncated to maxLen with "..." appended if longer.
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	if maxLen <= 0 {
		return "..."
	}
	result := s[:maxLen]
	for len(result) > 0 {
		r, size := utf8.DecodeLastRuneInString(result)
		if r != utf8.RuneError || size != 1 {
			break
		}
		result = result[:len(result)-1]
	}
	return result + "..."
}

// Ping verifies the credentials by calling the workspaces endpoint.
func (c *client) Ping(ctx context.Context, operatorID string) error {
	var resp wikiWorkspacesResponse
	return c.doRequest(ctx, http.MethodGet, "/v2.0/wiki/workspaces", map[string]string{
		"operatorId": operatorID,
		"maxResults": fmt.Sprintf("%d", defaultPageSize),
	}, nil, &resp)
}

// ListWorkspaces returns all accessible knowledge bases.
func (c *client) ListWorkspaces(ctx context.Context, operatorID string) ([]WikiWorkspace, error) {
	var all []WikiWorkspace
	nextToken := ""
	for {
		query := map[string]string{
			"operatorId": operatorID,
			"maxResults": fmt.Sprintf("%d", defaultPageSize),
		}
		if nextToken != "" {
			query["nextToken"] = nextToken
		}

		var resp wikiWorkspacesResponse
		if err := c.doRequest(ctx, http.MethodGet, "/v2.0/wiki/workspaces", query, nil, &resp); err != nil {
			return nil, err
		}
		all = append(all, resp.Workspaces...)
		if resp.NextToken == "" {
			break
		}
		nextToken = resp.NextToken
	}
	return all, nil
}

// ListNodes returns nodes (files and folders) in a workspace or folder.
func (c *client) ListNodes(ctx context.Context, parentNodeID, operatorID string) ([]WikiNode, string, error) {
	return c.listNodePage(ctx, parentNodeID, operatorID, "")
}

func (c *client) listNodePage(ctx context.Context, parentNodeID, operatorID, nextToken string) ([]WikiNode, string, error) {
	query := map[string]string{
		"parentNodeId": parentNodeID,
		"operatorId":   operatorID,
		"maxResults":   fmt.Sprintf("%d", defaultPageSize),
	}
	if nextToken != "" {
		query["nextToken"] = nextToken
	}

	var resp wikiNodesResponse
	if err := c.doRequest(ctx, http.MethodGet, "/v2.0/wiki/nodes", query, nil, &resp); err != nil {
		return nil, "", err
	}
	return resp.Nodes, resp.NextToken, nil
}

// ListAllNodes returns all nodes recursively (with pagination).
func (c *client) ListAllNodes(ctx context.Context, parentNodeID, operatorID string) ([]WikiNode, error) {
	visited := make(map[string]bool)
	all, err := c.listAllNodesFrom(ctx, parentNodeID, operatorID, visited)
	if err != nil {
		return nil, err
	}
	return all, nil
}

func (c *client) listAllNodesFrom(
	ctx context.Context,
	parentNodeID, operatorID string,
	visited map[string]bool,
) ([]WikiNode, error) {
	if parentNodeID == "" {
		return nil, nil
	}
	if visited[parentNodeID] {
		return nil, nil
	}
	visited[parentNodeID] = true

	var all []WikiNode
	nextToken := ""
	for {
		nodes, token, err := c.listNodePage(ctx, parentNodeID, operatorID, nextToken)
		if err != nil {
			return nil, err
		}
		all = append(all, nodes...)

		for _, node := range nodes {
			if !node.hasChildNodes() {
				continue
			}
			children, err := c.listAllNodesFrom(ctx, node.NodeID, operatorID, visited)
			if err != nil {
				return nil, fmt.Errorf("list children of node %s: %w", node.NodeID, err)
			}
			all = append(all, children...)
		}

		if token == "" {
			break
		}
		nextToken = token

		if err := sleepCtx(ctx, 200*time.Millisecond); err != nil {
			return nil, err
		}
	}
	return all, nil
}

func (n WikiNode) hasChildNodes() bool {
	return n.HasChildren || strings.EqualFold(n.NodeType, "FOLDER")
}

func (n WikiNode) contentKey() string {
	if strings.TrimSpace(n.DocKey) != "" {
		return strings.TrimSpace(n.DocKey)
	}
	return n.NodeID
}

// GetDocumentBlocks retrieves a DingTalk document's block tree. The caller can
// render the returned blocks to Markdown or surface a per-document failure.
func (c *client) GetDocumentBlocks(ctx context.Context, nodeID, operatorID string) ([]docBlock, error) {
	query := map[string]string{
		"operatorId": operatorID,
	}
	path := fmt.Sprintf("/v1.0/doc/suites/documents/%s/blocks", url.PathEscape(nodeID))

	var resp docBlocksResponse
	if err := c.doRequest(ctx, http.MethodGet, path, query, nil, &resp); err != nil {
		return nil, err
	}
	if err := resp.validate(); err != nil {
		return nil, err
	}
	return resp.allBlocks(), nil
}
