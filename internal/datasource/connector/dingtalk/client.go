package dingtalk

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/Tencent/WeKnora/internal/datasource"
	"github.com/Tencent/WeKnora/internal/logger"
)

const (
	defaultTimeout  = 30 * time.Second
	defaultPageSize = 30
	userAgent       = "WeKnora-DingTalk-Connector/1.0"
)

// client wraps the DingTalk Open API.
type client struct {
	baseURL     string
	clientID    string
	clientSecret string
	accessToken string
	tokenExpiry time.Time
	httpClient  *http.Client
	mu          sync.Mutex

	logTokenOnce sync.Once
}

// newClient constructs a client with configuration.
func newClient(cfg *Config) *client {
	return &client{
		baseURL:      cfg.GetBaseURL(),
		clientID:     cfg.ClientID,
		clientSecret: cfg.ClientSecret,
		httpClient:   &http.Client{Timeout: defaultTimeout},
	}
}

// ensureAccessToken refreshes the access token if expired or not set.
func (c *client) ensureAccessToken(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Check if current token is still valid (with 5 minute buffer)
	if c.accessToken != "" && time.Until(c.tokenExpiry) > 5*time.Minute {
		return nil
	}

	c.logTokenOnce.Do(func() {
		logger.Infof(ctx, "[DingTalk] client configured clientId=%s base=%s", redactClientID(c.clientID), c.baseURL)
	})

	// Fetch new token
	body := accessTokenRequest{
		AppKey:    c.clientID,
		AppSecret: c.clientSecret,
	}
	bodyBytes, _ := json.Marshal(body)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1.0/oauth2/accessToken", bytes.NewReader(bodyBytes))
	if err != nil {
		return fmt.Errorf("create token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	req.Header.Set("User-Agent", userAgent)

	logger.Infof(ctx, "[DingTalk] refreshing access token for clientId=%s", redactClientID(c.clientID))

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("execute token request: %w", err)
	}
	defer resp.Body.Close()

	respBody, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		return fmt.Errorf("read token response: %w", readErr)
	}

	if resp.StatusCode != http.StatusOK {
		var apiErr dingtalkErrorResponse
		_ = json.Unmarshal(respBody, &apiErr)
		if apiErr.ErrMsg != "" {
			return fmt.Errorf("dingtalk token error: status=%d msg=%s", resp.StatusCode, apiErr.ErrMsg)
		}
		return fmt.Errorf("dingtalk token error: status=%d body=%s", resp.StatusCode, truncate(string(respBody), 200))
	}

	var tokenResp accessTokenResponse
	if err := json.Unmarshal(respBody, &tokenResp); err != nil {
		return fmt.Errorf("decode token response: %w", err)
	}

	if tokenResp.AccessToken == "" {
		return fmt.Errorf("%w: empty access token received", datasource.ErrInvalidCredentials)
	}

	c.accessToken = tokenResp.AccessToken
	c.tokenExpiry = time.Now().Add(time.Duration(tokenResp.ExpireIn) * time.Second)

	logger.Infof(ctx, "[DingTalk] access token refreshed, expires in %d seconds", tokenResp.ExpireIn)
	return nil
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

		bodyPreview := truncate(string(bodyBytes), 500)
		logger.Infof(ctx, "[DingTalk] %s %s → status=%d bodyLen=%d body=%s",
			method, path, resp.StatusCode, len(bodyBytes), bodyPreview)

		// Handle rate limiting
		if resp.StatusCode == http.StatusTooManyRequests {
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

		// 401/403 → surface as ErrInvalidCredentials
		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
			return fmt.Errorf("%w: status=%d body=%s", datasource.ErrInvalidCredentials, resp.StatusCode, bodyPreview)
		}

		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			var apiErr dingtalkErrorResponse
			_ = json.Unmarshal(bodyBytes, &apiErr)
			if apiErr.ErrMsg != "" {
				return &dingtalkAPIError{Code: apiErr.ErrCode, Msg: apiErr.ErrMsg}
			}
			return fmt.Errorf("dingtalk api error: status=%d body=%s", resp.StatusCode, bodyPreview)
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
	return s[:maxLen] + "..."
}

// Ping verifies the credentials by calling workspaces endpoint.
func (c *client) Ping(ctx context.Context) error {
	var resp wikiWorkspacesResponse
	return c.doRequest(ctx, http.MethodGet, "/v2.0/wiki/workspaces", nil, nil, &resp)
}

// ListWorkspaces returns all accessible knowledge bases.
func (c *client) ListWorkspaces(ctx context.Context, operatorID string) ([]WikiWorkspace, error) {
	var all []WikiWorkspace
	nextToken := ""
	for {
		query := map[string]string{
			"operatorId": operatorID,
		}
		if nextToken != "" {
			query["nextToken"] = nextToken
			query["maxResults"] = fmt.Sprintf("%d", defaultPageSize)
		}

		var resp wikiWorkspacesResponse
		if err := c.doRequest(ctx, http.MethodGet, "/v2.0/wiki/workspaces", query, nil, &resp); err != nil {
			return nil, err
		}
		all = append(all, resp.Workspaces...)
		// Note: DingTalk's workspaces API doesn't have pagination token in response
		// based on current API spec, but we handle it generically
		break
	}
	return all, nil
}

// ListNodes returns nodes (files and folders) in a workspace or folder.
func (c *client) ListNodes(ctx context.Context, parentNodeID, operatorID string) ([]WikiNode, string, error) {
	query := map[string]string{
		"parentNodeId": parentNodeID,
		"operatorId":   operatorID,
		"maxResults":   fmt.Sprintf("%d", defaultPageSize),
	}

	var resp wikiNodesResponse
	if err := c.doRequest(ctx, http.MethodGet, "/v2.0/wiki/nodes", query, nil, &resp); err != nil {
		return nil, "", err
	}
	return resp.Nodes, resp.NextToken, nil
}

// ListAllNodes returns all nodes recursively (with pagination).
func (c *client) ListAllNodes(ctx context.Context, parentNodeID, operatorID string) ([]WikiNode, error) {
	var all []WikiNode
	nextToken := ""

	for {
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
			return nil, err
		}

		all = append(all, resp.Nodes...)

		if resp.NextToken == "" {
			break
		}
		nextToken = resp.NextToken

		// Rate limit protection
		if err := sleepCtx(ctx, 200*time.Millisecond); err != nil {
			return nil, err
		}
	}

	return all, nil
}
