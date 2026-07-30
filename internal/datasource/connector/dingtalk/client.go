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
	defaultPageSize = 50
	userAgent       = "WeKnora-DingTalk-Connector/1.0"
)

// client wraps the DingTalk OpenAPI for wiki / document operations.
type client struct {
	baseURL    string
	appKey     string
	appSecret  string
	operatorID string
	httpClient *http.Client

	tokenMu    sync.Mutex
	tokenCache string
	tokenExpAt time.Time
}

// newClient constructs a client with a normalized base URL.
func newClient(cfg *Config) *client {
	return &client{
		baseURL:    cfg.GetBaseURL(),
		appKey:     cfg.AppKey,
		appSecret:  cfg.AppSecret,
		operatorID: cfg.OperatorID,
		httpClient: datasource.NewConnectorHTTPClient(defaultTimeout),
	}
}

// getAccessToken retrieves (or returns cached) application access token.
// Tokens expire in ~2 hours; we cache with a 5-minute safety margin.
func (c *client) getAccessToken(ctx context.Context) (string, error) {
	c.tokenMu.Lock()
	defer c.tokenMu.Unlock()

	if c.tokenCache != "" && time.Now().Before(c.tokenExpAt) {
		return c.tokenCache, nil
	}

	payload, _ := json.Marshal(map[string]string{
		"appKey":    c.appKey,
		"appSecret": c.appSecret,
	})

	reqURL := c.baseURL + "/v1.0/oauth2/accessToken"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, bytes.NewReader(payload))
	if err != nil {
		return "", fmt.Errorf("create token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", userAgent)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("request token: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read token response: %w", err)
	}

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return "", fmt.Errorf("%w: status=%d body=%s",
			datasource.ErrInvalidCredentials, resp.StatusCode, truncate(string(body), 300))
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("dingtalk accessToken returned %d: %s",
			resp.StatusCode, truncate(string(body), 300))
	}

	var result accessTokenResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return "", fmt.Errorf("decode token response: %w", err)
	}
	if result.AccessToken == "" {
		// Some error responses return 200 with code/message.
		if result.Code != "" || result.Message != "" {
			return "", fmt.Errorf("%w: code=%s msg=%s",
				datasource.ErrInvalidCredentials, result.Code, result.Message)
		}
		return "", fmt.Errorf("%w: empty access token", datasource.ErrInvalidCredentials)
	}

	c.tokenCache = result.AccessToken
	ttl := time.Duration(result.ExpireIn) * time.Second
	if ttl > 5*time.Minute {
		ttl -= 5 * time.Minute
	} else if ttl <= 0 {
		ttl = 90 * time.Minute
	}
	c.tokenExpAt = time.Now().Add(ttl)

	logger.Infof(ctx, "[DingTalk] got accessToken appKey=%s expireIn=%ds",
		redactSecret(c.appKey), result.ExpireIn)
	return c.tokenCache, nil
}

// doRequest executes an authenticated request and decodes JSON, with retry for
// transient errors (429, 5xx, transport failures).
func (c *client) doRequest(ctx context.Context, method, path string, body interface{}, result interface{}) error {
	const (
		maxRetries    = 3
		max5xxRetries = 1
		retry5xxDelay = 2 * time.Second
	)
	var lastErr error
	backoff := []time.Duration{2 * time.Second, 4 * time.Second, 8 * time.Second}

	for attempt := 0; attempt <= maxRetries; attempt++ {
		token, err := c.getAccessToken(ctx)
		if err != nil {
			return err
		}

		var bodyReader io.Reader
		if body != nil {
			bodyBytes, mErr := json.Marshal(body)
			if mErr != nil {
				return fmt.Errorf("marshal request body: %w", mErr)
			}
			bodyReader = bytes.NewReader(bodyBytes)
		}

		reqURL := c.baseURL + path
		req, err := http.NewRequestWithContext(ctx, method, reqURL, bodyReader)
		if err != nil {
			return fmt.Errorf("create request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("User-Agent", userAgent)
		req.Header.Set("x-acs-dingtalk-access-token", token)

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
			wait := backoff[min(attempt, len(backoff)-1)]
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

		// 401: token may have expired mid-flight — clear cache and retry once path.
		if resp.StatusCode == http.StatusUnauthorized {
			c.tokenMu.Lock()
			c.tokenCache = ""
			c.tokenExpAt = time.Time{}
			c.tokenMu.Unlock()
			lastErr = fmt.Errorf("%w: status=401 body=%s", datasource.ErrInvalidCredentials, bodyPreview)
			if attempt < maxRetries {
				if sErr := sleepCtx(ctx, backoff[attempt]); sErr != nil {
					return sErr
				}
				continue
			}
			return lastErr
		}

		if resp.StatusCode == http.StatusForbidden {
			return fmt.Errorf("%w: status=403 body=%s", datasource.ErrInvalidCredentials, bodyPreview)
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

// buildQuery encodes query parameters, omitting empty values.
func buildQuery(params map[string]string) string {
	values := url.Values{}
	for k, v := range params {
		if v != "" {
			values.Set(k, v)
		}
	}
	if len(values) == 0 {
		return ""
	}
	return "?" + values.Encode()
}

// Ping verifies credentials by obtaining an access token and listing one page
// of workspaces (validates operator_id + permissions in one shot).
func (c *client) Ping(ctx context.Context) error {
	if _, err := c.getAccessToken(ctx); err != nil {
		return err
	}
	// A single page of workspaces confirms operator_id is accepted.
	path := "/v2.0/wiki/workspaces" + buildQuery(map[string]string{
		"operatorId": c.operatorID,
		"maxResults": "1",
	})
	var resp workspaceListResponse
	if err := c.doRequest(ctx, http.MethodGet, path, nil, &resp); err != nil {
		return err
	}
	return nil
}

// ListWorkspaces returns all knowledge-base workspaces accessible to the operator.
func (c *client) ListWorkspaces(ctx context.Context) ([]workspace, error) {
	var all []workspace
	nextToken := ""
	for {
		path := "/v2.0/wiki/workspaces" + buildQuery(map[string]string{
			"operatorId": c.operatorID,
			"maxResults": fmt.Sprintf("%d", defaultPageSize),
			"nextToken":  nextToken,
		})
		var resp workspaceListResponse
		if err := c.doRequest(ctx, http.MethodGet, path, nil, &resp); err != nil {
			return nil, err
		}
		items, nt := resp.items()
		all = append(all, items...)
		if nt == "" {
			break
		}
		nextToken = nt
	}
	return all, nil
}

// ListNodes returns direct children under parentNodeID.
func (c *client) ListNodes(ctx context.Context, parentNodeID string) ([]wikiNode, error) {
	var all []wikiNode
	nextToken := ""
	for {
		path := "/v2.0/wiki/nodes" + buildQuery(map[string]string{
			"parentNodeId": parentNodeID,
			"operatorId":   c.operatorID,
			"maxResults":   fmt.Sprintf("%d", defaultPageSize),
			"nextToken":    nextToken,
		})
		var resp nodeListResponse
		if err := c.doRequest(ctx, http.MethodGet, path, nil, &resp); err != nil {
			return nil, err
		}
		items, nt := resp.items()
		for _, n := range items {
			if n.ParentNodeID == "" {
				n.ParentNodeID = parentNodeID
			}
			all = append(all, n)
		}
		if nt == "" {
			break
		}
		nextToken = nt
	}
	return all, nil
}

// GetNode returns metadata for a single wiki node.
func (c *client) GetNode(ctx context.Context, nodeID string) (wikiNode, error) {
	path := fmt.Sprintf("/v2.0/wiki/nodes/%s", url.PathEscape(nodeID)) + buildQuery(map[string]string{
		"operatorId": c.operatorID,
	})
	var resp nodeInfoResponse
	if err := c.doRequest(ctx, http.MethodGet, path, nil, &resp); err != nil {
		return wikiNode{}, err
	}
	return resp.item(), nil
}

// GetDocumentBlocks fetches the block list for a document (docKey ≈ nodeId for wiki docs).
func (c *client) GetDocumentBlocks(ctx context.Context, docKey string) ([]docBlock, error) {
	path := fmt.Sprintf("/v1.0/doc/suites/documents/%s/blocks", url.PathEscape(docKey)) + buildQuery(map[string]string{
		"operatorId": c.operatorID,
	})
	var resp blocksResponse
	if err := c.doRequest(ctx, http.MethodGet, path, nil, &resp); err != nil {
		return nil, err
	}
	return resp.items(), nil
}

// GetWorkspace returns a single workspace (used to resolve rootNodeId).
func (c *client) GetWorkspace(ctx context.Context, workspaceID string) (workspace, error) {
	path := fmt.Sprintf("/v2.0/wiki/workspaces/%s", url.PathEscape(workspaceID)) + buildQuery(map[string]string{
		"operatorId": c.operatorID,
	})
	// Response may be bare workspace or wrapped.
	var raw json.RawMessage
	if err := c.doRequest(ctx, http.MethodGet, path, nil, &raw); err != nil {
		return workspace{}, err
	}
	var direct workspace
	if err := json.Unmarshal(raw, &direct); err == nil && direct.WorkspaceID != "" {
		return direct, nil
	}
	var wrapped struct {
		Result    workspace `json:"result"`
		Workspace workspace `json:"workspace"`
	}
	if err := json.Unmarshal(raw, &wrapped); err != nil {
		return workspace{}, fmt.Errorf("decode workspace: %w", err)
	}
	if wrapped.Result.WorkspaceID != "" {
		return wrapped.Result, nil
	}
	if wrapped.Workspace.WorkspaceID != "" {
		return wrapped.Workspace, nil
	}
	// Fallback: list and find
	all, err := c.ListWorkspaces(ctx)
	if err != nil {
		return workspace{}, err
	}
	for _, w := range all {
		if w.WorkspaceID == workspaceID {
			return w, nil
		}
	}
	return workspace{}, fmt.Errorf("workspace %s not found", workspaceID)
}

// ListAllNodesRecursive walks a workspace (or subtree) depth-first and returns
// every node. partial failures on individual branches are logged and skipped so
// a single bad folder does not abort the whole sync.
func (c *client) ListAllNodesRecursive(ctx context.Context, rootNodeID string) ([]wikiNode, error) {
	var all []wikiNode
	var walk func(parentID string) error
	walk = func(parentID string) error {
		children, err := c.ListNodes(ctx, parentID)
		if err != nil {
			return err
		}
		for _, n := range children {
			all = append(all, n)
			if isFolder(n) || n.HasChildren {
				if err := walk(n.NodeID); err != nil {
					logger.Warnf(ctx, "[DingTalk] skip children of node %s: %v", n.NodeID, err)
					continue
				}
			}
		}
		return nil
	}
	if err := walk(rootNodeID); err != nil {
		return all, err
	}
	return all, nil
}
