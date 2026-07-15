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

	"github.com/Tencent/WeKnora/internal/logger"
)

// Client wraps the DingTalk Open Platform API for knowledge base operations.
type Client struct {
	baseURL       string
	legacyBaseURL string
	appKey        string
	appSecret     string

	httpClient *http.Client

	// Token / operator cache (thread-safe)
	mu         sync.Mutex
	tokenCache string
	tokenExpAt time.Time
	operatorID string
}

type nodeListFailure struct {
	Node wikiNode
	Err  error
}

type partialNodeListError struct {
	Failures []nodeListFailure
}

func (e *partialNodeListError) Error() string {
	if e == nil || len(e.Failures) == 0 {
		return "partial wiki node listing failed"
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
		baseURL:       config.GetBaseURL(),
		legacyBaseURL: config.GetLegacyBaseURL(),
		appKey:        config.AppKey,
		appSecret:     config.AppSecret,
		operatorID:    config.OperatorID,
		httpClient:    &http.Client{Timeout: 30 * time.Second},
	}
}

// getAccessToken retrieves (or returns cached) app access token.
// DingTalk tokens expire in 2 hours; we cache with a 5-minute safety margin.
func (c *Client) getAccessToken(ctx context.Context) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

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
	req.Header.Set("Content-Type", "application/json; charset=utf-8")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("request token: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read token response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("dingtalk auth error: %s", apiErrorString(resp.StatusCode, respBody))
	}

	var result tokenResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return "", fmt.Errorf("decode token response: %w", err)
	}
	if result.AccessToken == "" {
		return "", fmt.Errorf("dingtalk auth error: empty accessToken in response")
	}

	c.tokenCache = result.AccessToken
	ttl := time.Duration(result.ExpireIn) * time.Second
	if ttl > 5*time.Minute {
		ttl -= 5 * time.Minute
	}
	c.tokenExpAt = time.Now().Add(ttl)

	logger.Infof(ctx, "[DingTalk] got accessToken: %s expire=%ds", maskToken(result.AccessToken), result.ExpireIn)
	return c.tokenCache, nil
}

// GetOperatorID returns the operator unionId used for wiki API permission
// checks. When not configured explicitly, it is resolved once from the org's
// admin list via the legacy contact API (requires scope qyapi_get_member) and
// cached for the lifetime of the client.
func (c *Client) GetOperatorID(ctx context.Context) (string, error) {
	c.mu.Lock()
	if c.operatorID != "" {
		defer c.mu.Unlock()
		return c.operatorID, nil
	}
	c.mu.Unlock()

	unionID, err := c.resolveAdminUnionID(ctx)
	if err != nil {
		return "", err
	}

	c.mu.Lock()
	c.operatorID = unionID
	c.mu.Unlock()
	return unionID, nil
}

// resolveAdminUnionID resolves the unionId of the org's first admin via the
// legacy contact API: gettoken → listadmin → user/get.
func (c *Client) resolveAdminUnionID(ctx context.Context) (string, error) {
	// Legacy token (same credentials, legacy endpoint)
	tokenURL := fmt.Sprintf("%s/gettoken?appkey=%s&appsecret=%s",
		c.legacyBaseURL, url.QueryEscape(c.appKey), url.QueryEscape(c.appSecret))
	var tokenResp legacyTokenResponse
	if err := c.doLegacyRequest(ctx, http.MethodGet, tokenURL, nil, &tokenResp); err != nil {
		return "", fmt.Errorf("get legacy token: %w", err)
	}
	if tokenResp.ErrCode != 0 {
		return "", fmt.Errorf("get legacy token: errcode=%d errmsg=%s", tokenResp.ErrCode, tokenResp.ErrMsg)
	}

	adminURL := fmt.Sprintf("%s/topapi/user/listadmin?access_token=%s",
		c.legacyBaseURL, url.QueryEscape(tokenResp.AccessToken))
	var adminResp adminListResponse
	if err := c.doLegacyRequest(ctx, http.MethodPost, adminURL, nil, &adminResp); err != nil {
		return "", fmt.Errorf("list admins: %w", err)
	}
	if adminResp.ErrCode != 0 || len(adminResp.Result) == 0 {
		return "", fmt.Errorf(
			"resolve operator failed (errcode=%d errmsg=%s): configure operator_id (unionId) explicitly, "+
				"or grant the qyapi_get_member scope to the app",
			adminResp.ErrCode, adminResp.ErrMsg)
	}

	userURL := fmt.Sprintf("%s/topapi/v2/user/get?access_token=%s",
		c.legacyBaseURL, url.QueryEscape(tokenResp.AccessToken))
	var userResp userGetResponse
	if err := c.doLegacyRequest(ctx, http.MethodPost, userURL,
		map[string]string{"userid": adminResp.Result[0].UserID}, &userResp); err != nil {
		return "", fmt.Errorf("get admin user: %w", err)
	}
	if userResp.ErrCode != 0 || userResp.Result.UnionID == "" {
		return "", fmt.Errorf("get admin user: errcode=%d errmsg=%s", userResp.ErrCode, userResp.ErrMsg)
	}

	logger.Infof(ctx, "[DingTalk] resolved operator unionId from admin %q", userResp.Result.Name)
	return userResp.Result.UnionID, nil
}

// doLegacyRequest executes a legacy (oapi.dingtalk.com) API request.
// Legacy APIs signal errors via the errcode field, decoded by the caller.
func (c *Client) doLegacyRequest(ctx context.Context, method, fullURL string, body interface{}, result interface{}) error {
	var bodyReader io.Reader
	if body != nil {
		bodyBytes, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshal request body: %w", err)
		}
		bodyReader = bytes.NewReader(bodyBytes)
	}

	req, err := http.NewRequestWithContext(ctx, method, fullURL, bodyReader)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("execute request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read response body: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("legacy api error: status=%d body=%s", resp.StatusCode, truncate(string(respBody), 500))
	}
	if err := json.Unmarshal(respBody, result); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}

// doRequest executes an authenticated new-style API request and decodes the
// JSON response. path must include any query parameters.
func (c *Client) doRequest(ctx context.Context, method, path string, body interface{}, result interface{}) error {
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

	logger.Infof(ctx, "[DingTalk] %s %s", method, redactQuery(path))

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("execute request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read response body: %w", err)
	}

	logger.Infof(ctx, "[DingTalk] %s %s → status=%d bodyLen=%d",
		method, redactQuery(path), resp.StatusCode, len(respBody))

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("dingtalk api error: %s", apiErrorString(resp.StatusCode, respBody))
	}

	if result != nil {
		if err := json.Unmarshal(respBody, result); err != nil {
			return fmt.Errorf("decode response: %w", err)
		}
	}
	return nil
}

// apiErrorString renders a non-2xx api.dingtalk.com response for error messages.
func apiErrorString(status int, body []byte) string {
	var apiErr apiError
	if err := json.Unmarshal(body, &apiErr); err == nil && apiErr.Code != "" {
		return fmt.Sprintf("status=%d code=%s message=%s requestid=%s",
			status, apiErr.Code, apiErr.Message, apiErr.RequestID)
	}
	return fmt.Sprintf("status=%d body=%s", status, truncate(string(body), 500))
}

// Ping verifies the credentials by obtaining an access token.
func (c *Client) Ping(ctx context.Context) error {
	_, err := c.getAccessToken(ctx)
	return err
}

// maxPages bounds pagination loops as a safety net against a server that
// keeps returning the same nextToken.
const maxPages = 200

// ListWorkspaces returns all knowledge bases (知识库) accessible to the operator.
func (c *Client) ListWorkspaces(ctx context.Context) ([]workspace, error) {
	operatorID, err := c.GetOperatorID(ctx)
	if err != nil {
		return nil, err
	}

	var all []workspace
	nextToken := ""
	for page := 0; page < maxPages; page++ {
		path := "/v2.0/wiki/workspaces?operatorId=" + url.QueryEscape(operatorID)
		if nextToken != "" {
			path += "&nextToken=" + url.QueryEscape(nextToken)
		}

		var resp workspaceListResponse
		if err := c.doRequest(ctx, http.MethodGet, path, nil, &resp); err != nil {
			return nil, fmt.Errorf("list workspaces: %w", err)
		}
		all = append(all, resp.Workspaces...)

		if resp.NextToken == "" || resp.NextToken == nextToken {
			break
		}
		nextToken = resp.NextToken
	}

	logger.Infof(ctx, "[DingTalk] ListWorkspaces: total %d workspaces", len(all))
	return all, nil
}

// GetWorkspace returns metadata (including the root node ID) for one knowledge base.
func (c *Client) GetWorkspace(ctx context.Context, workspaceID string) (workspace, error) {
	operatorID, err := c.GetOperatorID(ctx)
	if err != nil {
		return workspace{}, err
	}

	path := fmt.Sprintf("/v2.0/wiki/workspaces/%s?operatorId=%s",
		url.PathEscape(workspaceID), url.QueryEscape(operatorID))

	var resp workspaceDetailResponse
	if err := c.doRequest(ctx, http.MethodGet, path, nil, &resp); err != nil {
		return workspace{}, fmt.Errorf("get workspace %s: %w", workspaceID, err)
	}
	if resp.Workspace.WorkspaceID == "" {
		resp.Workspace.WorkspaceID = workspaceID
	}
	return resp.Workspace, nil
}

// ListNodes returns the direct children of a wiki node.
func (c *Client) ListNodes(ctx context.Context, parentNodeID string) ([]wikiNode, error) {
	operatorID, err := c.GetOperatorID(ctx)
	if err != nil {
		return nil, err
	}

	var all []wikiNode
	nextToken := ""
	for page := 0; page < maxPages; page++ {
		path := fmt.Sprintf("/v2.0/wiki/nodes?parentNodeId=%s&operatorId=%s",
			url.QueryEscape(parentNodeID), url.QueryEscape(operatorID))
		if nextToken != "" {
			path += "&nextToken=" + url.QueryEscape(nextToken)
		}

		var resp nodeListResponse
		if err := c.doRequest(ctx, http.MethodGet, path, nil, &resp); err != nil {
			return nil, fmt.Errorf("list nodes under %s: %w", parentNodeID, err)
		}
		all = append(all, resp.Nodes...)

		if resp.NextToken == "" || resp.NextToken == nextToken {
			break
		}
		nextToken = resp.NextToken
	}
	return all, nil
}

// GetNode returns metadata for a single wiki node.
func (c *Client) GetNode(ctx context.Context, nodeID string) (wikiNode, error) {
	operatorID, err := c.GetOperatorID(ctx)
	if err != nil {
		return wikiNode{}, err
	}

	path := fmt.Sprintf("/v2.0/wiki/nodes/%s?operatorId=%s",
		url.PathEscape(nodeID), url.QueryEscape(operatorID))

	var resp nodeDetailResponse
	if err := c.doRequest(ctx, http.MethodGet, path, nil, &resp); err != nil {
		return wikiNode{}, fmt.Errorf("get node %s: %w", nodeID, err)
	}
	return resp.Node, nil
}

// ListNodesRecursive returns all descendant nodes below the given parent,
// walking the tree depth-first. On per-node listing failures it keeps going
// and returns the collected nodes together with a *partialNodeListError.
func (c *Client) ListNodesRecursive(ctx context.Context, parentNodeID string) ([]wikiNode, error) {
	var all []wikiNode
	var failures []nodeListFailure
	var walk func(nodes []wikiNode)

	walk = func(nodes []wikiNode) {
		for _, node := range nodes {
			all = append(all, node)
			if !node.HasChildren {
				continue
			}
			children, err := c.ListNodes(ctx, node.NodeID)
			if err != nil {
				failures = append(failures, nodeListFailure{
					Node: node,
					Err:  fmt.Errorf("list children of %s: %w", node.NodeID, err),
				})
				logger.Warnf(ctx, "[DingTalk] partial node listing failure: node=%s err=%v", node.NodeID, err)
				continue
			}
			walk(children)
		}
	}

	top, err := c.ListNodes(ctx, parentNodeID)
	if err != nil {
		return nil, err
	}
	walk(top)

	if len(failures) > 0 {
		return all, &partialNodeListError{Failures: failures}
	}
	return all, nil
}

// ──────────────────────────────────────────────────────────────────────
// Document export: convert an online document (ALIDOC) to markdown
//
// Flow:
//  1. POST /v2.0/doc/me/export/submit     → create export task, get taskId
//  2. GET  /v2.0/doc/me/export/task/query → poll until downloadUrl is ready
//  3. GET  downloadUrl                    → download markdown bytes (presigned
//     OSS URL, no auth header needed)
// ──────────────────────────────────────────────────────────────────────

// exportPollInterval and exportTimeout bound the export polling loop. Large
// documents can take a while to convert server-side. exportPollInterval is a
// variable so tests can shorten it.
var exportPollInterval = 2 * time.Second

const exportTimeout = 2 * time.Minute

// SubmitExportTask submits an async export task for a document node.
// targetFormat is typically "markdown". Returns (taskId, downloadUrl); the
// downloadUrl may already be set when the export completes synchronously.
func (c *Client) SubmitExportTask(ctx context.Context, dentryUUID, targetFormat string) (string, string, error) {
	operatorID, err := c.GetOperatorID(ctx)
	if err != nil {
		return "", "", err
	}

	body := map[string]string{
		"dentryUuid":   dentryUUID,
		"operatorId":   operatorID,
		"targetFormat": targetFormat,
	}

	var resp exportSubmitResponse
	if err := c.doRequest(ctx, http.MethodPost, "/v2.0/doc/me/export/submit", body, &resp); err != nil {
		return "", "", fmt.Errorf("submit export task: %w", err)
	}
	if resp.TaskID == "" && resp.DownloadURL == "" {
		return "", "", fmt.Errorf("submit export task: empty taskId in response")
	}
	return resp.TaskID, resp.DownloadURL, nil
}

// QueryExportTask polls an export task. Returns the downloadUrl when ready
// (empty while the task is still running).
func (c *Client) QueryExportTask(ctx context.Context, taskID string) (string, error) {
	operatorID, err := c.GetOperatorID(ctx)
	if err != nil {
		return "", err
	}

	path := fmt.Sprintf("/v2.0/doc/me/export/task/query?taskId=%s&operatorId=%s",
		url.QueryEscape(taskID), url.QueryEscape(operatorID))

	var resp exportQueryResponse
	if err := c.doRequest(ctx, http.MethodGet, path, nil, &resp); err != nil {
		return "", fmt.Errorf("query export task: %w", err)
	}
	if resp.DownloadURL != "" {
		return resp.DownloadURL, nil
	}

	switch strings.ToUpper(resp.Status) {
	case "", "RUNNING", "PROCESSING", "INIT", "PENDING":
		return "", nil // not ready yet
	case "SUCCESS", "FINISHED":
		return "", nil // succeeded but URL missing; keep polling defensively
	default:
		return "", fmt.Errorf("export task failed: status=%s", resp.Status)
	}
}

// ExportMarkdown is a high-level helper that exports a document node to
// markdown and downloads the result. Returns the markdown bytes.
func (c *Client) ExportMarkdown(ctx context.Context, dentryUUID string) ([]byte, error) {
	taskID, downloadURL, err := c.SubmitExportTask(ctx, dentryUUID, "markdown")
	if err != nil {
		return nil, err
	}

	if downloadURL == "" {
		deadline := time.Now().Add(exportTimeout)
		for time.Now().Before(deadline) {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(exportPollInterval):
			}

			downloadURL, err = c.QueryExportTask(ctx, taskID)
			if err != nil {
				return nil, err
			}
			if downloadURL != "" {
				break
			}
		}
		if downloadURL == "" {
			return nil, fmt.Errorf("export task timed out after %s (taskId=%s)", exportTimeout, taskID)
		}
	}

	return c.downloadURL(ctx, downloadURL)
}

// downloadURL fetches a presigned download URL (no auth header required).
func (c *Client) downloadURL(ctx context.Context, fullURL string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fullURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create download request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("download request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("download failed: status=%d body=%s", resp.StatusCode, truncate(string(body), 500))
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read download body: %w", err)
	}
	logger.Infof(ctx, "[DingTalk] downloaded export file: %d bytes", len(data))
	return data, nil
}

// --- Helpers ---

// truncate truncates a string to maxLen and appends "..." if truncated.
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// maskToken renders a token safe for logs.
func maskToken(token string) string {
	if len(token) <= 8 {
		return "***"
	}
	return token[:4] + "..." + token[len(token)-4:]
}

// redactQuery strips query values that may carry identifiers from log lines.
func redactQuery(path string) string {
	if i := strings.Index(path, "operatorId="); i >= 0 {
		end := strings.IndexByte(path[i:], '&')
		if end < 0 {
			return path[:i] + "operatorId=***"
		}
		return path[:i] + "operatorId=***" + path[i+end:]
	}
	return path
}
