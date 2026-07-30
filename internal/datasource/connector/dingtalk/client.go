package dingtalk

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Tencent/WeKnora/internal/datasource"
	"github.com/Tencent/WeKnora/internal/logger"
)

const (
	defaultTimeout    = 30 * time.Second
	defaultMaxResults = 50
	userAgent         = "WeKnora-DingTalk-Connector/1.0"
)

// Retry policy: 429 honours Retry-After, 5xx retries once, transport errors back off.
const (
	dingtalkMaxRetries    = 3
	dingtalkMax5xxRetries = 1
	dingtalkRetry5xxDelay = 2 * time.Second
)

var dingtalkRetryBackoff = []time.Duration{2 * time.Second, 4 * time.Second, 8 * time.Second}

// Client wraps the DingTalk Open Platform API for document/knowledge-base operations.
type Client struct {
	baseURL   string
	appKey    string
	appSecret string

	httpClient *http.Client

	// Token cache (thread-safe)
	tokenMu    sync.Mutex
	tokenCache string
	tokenExpAt time.Time
}

// NewClient creates a new DingTalk API client.
func NewClient(config *Config) *Client {
	return &Client{
		baseURL:    config.GetBaseURL(),
		appKey:     config.AppKey,
		appSecret:  config.AppSecret,
		httpClient: datasource.NewConnectorHTTPClient(defaultTimeout),
	}
}

// getAccessToken retrieves (or returns cached) DingTalk access token.
// DingTalk tokens expire in ~2 hours; we cache with a 5-minute safety margin.
func (c *Client) getAccessToken(ctx context.Context) (string, error) {
	c.tokenMu.Lock()
	defer c.tokenMu.Unlock()

	if c.tokenCache != "" && time.Now().Before(c.tokenExpAt) {
		return c.tokenCache, nil
	}

	payload, _ := json.Marshal(map[string]string{
		"appKey":    c.appKey,
		"appSecret": c.appSecret,
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

	respBody, _ := io.ReadAll(resp.Body)

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return "", fmt.Errorf("%w: status=%d body=%s", datasource.ErrInvalidCredentials, resp.StatusCode, truncate(string(respBody), 500))
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("dingtalk auth error: status=%d body=%s", resp.StatusCode, truncate(string(respBody), 500))
	}

	var result tokenResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
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

	logger.Infof(ctx, "[DingTalk] got accessToken: %s...%s expireIn=%ds",
		result.AccessToken[:min(8, len(result.AccessToken))],
		result.AccessToken[len(result.AccessToken)-min(4, len(result.AccessToken)):],
		result.ExpireIn)

	return c.tokenCache, nil
}

// doRequest executes an authenticated API request and decodes the JSON response,
// retrying transient failures (transport errors, HTTP 429, 5xx).
func (c *Client) doRequest(ctx context.Context, method, path string, body interface{}, result interface{}) error {
	token, err := c.getAccessToken(ctx)
	if err != nil {
		return err
	}

	var bodyBytes []byte
	if body != nil {
		bodyBytes, err = json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshal request body: %w", err)
		}
	}

	url := c.baseURL + path
	var lastErr error

	for attempt := 0; attempt <= dingtalkMaxRetries; attempt++ {
		var bodyReader io.Reader
		if bodyBytes != nil {
			bodyReader = bytes.NewReader(bodyBytes)
		}
		req, err := http.NewRequestWithContext(ctx, method, url, bodyReader)
		if err != nil {
			return fmt.Errorf("create request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json; charset=utf-8")
		req.Header.Set("x-acs-dingtalk-access-token", token)
		req.Header.Set("User-Agent", userAgent)

		if attempt == 0 {
			logger.Infof(ctx, "[DingTalk] %s %s", method, path)
		} else {
			logger.Infof(ctx, "[DingTalk] %s %s (retry %d/%d)", method, path, attempt, dingtalkMaxRetries)
		}

		resp, err := c.httpClient.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("execute request: %w", err)
			if attempt < dingtalkMaxRetries {
				if sErr := sleepCtx(ctx, dingtalkRetryBackoff[attempt]); sErr != nil {
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
			if attempt < dingtalkMaxRetries {
				if sErr := sleepCtx(ctx, dingtalkRetryBackoff[attempt]); sErr != nil {
					return sErr
				}
				continue
			}
			return lastErr
		}

		logger.Infof(ctx, "[DingTalk] %s %s → status=%d bodyLen=%d body=%s",
			method, path, resp.StatusCode, len(respBody), truncate(string(respBody), 1000))

		// 401/403 → surface as ErrInvalidCredentials so DataSourceService can
		// distinguish bad-token from transient failures and auto-flag the source.
		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
			return fmt.Errorf("%w: status=%d body=%s", datasource.ErrInvalidCredentials, resp.StatusCode, truncate(string(respBody), 500))
		}

		if resp.StatusCode == http.StatusTooManyRequests {
			wait := parseRetryAfter(resp.Header.Get("Retry-After"), dingtalkRetryBackoff[min(attempt, len(dingtalkRetryBackoff)-1)])
			lastErr = fmt.Errorf("dingtalk rate limited: status=429 body=%s", truncate(string(respBody), 500))
			if attempt < dingtalkMaxRetries {
				if sErr := sleepCtx(ctx, wait); sErr != nil {
					return sErr
				}
				continue
			}
			return lastErr
		}

		if resp.StatusCode >= 500 && resp.StatusCode < 600 {
			lastErr = fmt.Errorf("dingtalk server error: status=%d body=%s", resp.StatusCode, truncate(string(respBody), 500))
			if attempt < dingtalkMax5xxRetries {
				if sErr := sleepCtx(ctx, dingtalkRetry5xxDelay); sErr != nil {
					return sErr
				}
				continue
			}
			return lastErr
		}

		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return fmt.Errorf("dingtalk api error: status=%d body=%s", resp.StatusCode, truncate(string(respBody), 500))
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

// ListSpaces returns all knowledge base spaces accessible to the app.
func (c *Client) ListSpaces(ctx context.Context) ([]docSpace, error) {
	var allSpaces []docSpace
	nextToken := ""

	for {
		path := fmt.Sprintf("/v1.0/doc/spaces?maxResults=%d", defaultMaxResults)
		if nextToken != "" {
			path += "&nextToken=" + nextToken
		}

		var resp spaceListResponse
		if err := c.doRequest(ctx, http.MethodGet, path, nil, &resp); err != nil {
			return nil, fmt.Errorf("list dingtalk spaces: %w", err)
		}

		logger.Infof(ctx, "[DingTalk] ListSpaces: got %d spaces, nextToken=%s", len(resp.Result.Spaces), truncate(resp.Result.NextToken, 20))

		allSpaces = append(allSpaces, resp.Result.Spaces...)

		if resp.Result.NextToken == "" {
			break
		}
		nextToken = resp.Result.NextToken
	}

	logger.Infof(ctx, "[DingTalk] ListSpaces: total %d spaces", len(allSpaces))
	return allSpaces, nil
}

// ListSpaceNodes returns nodes under a space, optionally filtered by parentID.
// If parentID is empty, returns top-level nodes.
func (c *Client) ListSpaceNodes(ctx context.Context, spaceID string, parentID string) ([]docNode, error) {
	var allNodes []docNode
	nextToken := ""

	for {
		path := fmt.Sprintf("/v1.0/doc/spaces/%s/nodes?maxResults=%d", spaceID, defaultMaxResults)
		if parentID != "" {
			path += "&parentId=" + parentID
		}
		if nextToken != "" {
			path += "&nextToken=" + nextToken
		}

		var resp nodeListResponse
		if err := c.doRequest(ctx, http.MethodGet, path, nil, &resp); err != nil {
			return nil, fmt.Errorf("list dingtalk space nodes: %w", err)
		}

		for _, node := range resp.Result.Nodes {
			if node.SpaceID == "" {
				node.SpaceID = spaceID
			}
			if node.ParentID == "" && parentID != "" {
				node.ParentID = parentID
			}
			allNodes = append(allNodes, node)
		}

		if resp.Result.NextToken == "" {
			break
		}
		nextToken = resp.Result.NextToken
	}

	return allNodes, nil
}

// GetNodeDetail returns detailed information for a single node, including content.
func (c *Client) GetNodeDetail(ctx context.Context, nodeID string) (docNodeDetail, error) {
	path := fmt.Sprintf("/v1.0/doc/nodes/%s", nodeID)

	var resp nodeDetailResponse
	if err := c.doRequest(ctx, http.MethodGet, path, nil, &resp); err != nil {
		return docNodeDetail{}, fmt.Errorf("get node detail: %w", err)
	}

	return resp.Result, nil
}

// nodeListFailure records a single node whose children could not be listed.
type nodeListFailure struct {
	Node docNode
	Err  error
}

// partialNodeListError indicates that the tree walk encountered errors on some
// branches but still collected all successfully listed nodes. Callers should
// process Nodes and surface Failures to the user as a partial sync.
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

// ListAllNodesRecursive recursively lists all nodes under a wiki space.
// It walks the tree depth-first to discover all nested documents.
// If some branches fail to list (e.g. transient API error), the successfully
// collected nodes are returned along with a *partialNodeListError so the
// caller can process partial results and surface failures.
func (c *Client) ListAllNodesRecursive(ctx context.Context, spaceID string) ([]docNode, error) {
	topNodes, err := c.ListSpaceNodes(ctx, spaceID, "")
	if err != nil {
		return nil, err
	}

	var allNodes []docNode
	var failures []nodeListFailure
	var walk func(nodes []docNode)

	walk = func(nodes []docNode) {
		for _, node := range nodes {
			allNodes = append(allNodes, node)

			// Recurse into folder nodes to discover children.
			// In DingTalk's knowledge base, only folder-type nodes can have
			// child nodes; documents, sheets, etc. are leaf nodes.
			if node.Type == "folder" {
				children, err := c.ListSpaceNodes(ctx, spaceID, node.NodeID)
				if err != nil {
					wrappedErr := fmt.Errorf("list children of %s: %w", node.NodeID, err)
					failures = append(failures, nodeListFailure{
						Node: node,
						Err:  wrappedErr,
					})
					logger.Warnf(ctx, "[DingTalk] partial node listing failure: space=%s node=%s err=%v",
						spaceID, node.NodeID, err)
					continue
				}
				walk(children)
			}
		}
	}

	walk(topNodes)
	if len(failures) > 0 {
		return allNodes, &partialNodeListError{Failures: failures}
	}
	return allNodes, nil
}

// --- Helper functions ---

// parseRetryAfter interprets a Retry-After header value (seconds) into a wait duration.
func parseRetryAfter(header string, fallback time.Duration) time.Duration {
	if header == "" {
		return fallback
	}
	secs, err := strconv.ParseFloat(strings.TrimSpace(header), 64)
	if err != nil {
		return fallback
	}
	if secs <= 0 {
		return 100 * time.Millisecond
	}
	return time.Duration(secs * float64(time.Second))
}

// sleepCtx waits for d or until ctx is cancelled.
func sleepCtx(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// truncate truncates a string to maxLen and appends "..." if truncated.
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
