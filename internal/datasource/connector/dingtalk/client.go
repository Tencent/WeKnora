package dingtalk

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"

	"github.com/Tencent/WeKnora/internal/datasource"
	"github.com/Tencent/WeKnora/internal/logger"
)

const (
	defaultTimeout      = 30 * time.Second
	defaultPageSize     = 50
	workspacePageSize   = 30
	logBodyPreviewBytes = 500
)

// client wraps the DingTalk OpenAPI endpoints used by the connector.
type client struct {
	baseURL        string
	oapiBaseURL    string
	appKey         string
	appSecret      string
	operatorUserID string

	httpClient *http.Client

	// Token cache (thread-safe).
	tokenMu    sync.Mutex
	tokenCache string
	tokenExpAt time.Time

	// Operator unionid cache (thread-safe).
	operatorMu      sync.Mutex
	operatorUnionID string
}

// newClient constructs a client with normalized base URLs.
func newClient(cfg *Config) *client {
	return &client{
		baseURL:         cfg.GetBaseURL(),
		oapiBaseURL:     cfg.GetOAPIBaseURL(),
		appKey:          cfg.AppKey,
		appSecret:       cfg.AppSecret,
		operatorUserID:  cfg.OperatorUserID,
		operatorUnionID: cfg.OperatorUnionID,
		httpClient:      &http.Client{Timeout: defaultTimeout},
	}
}

// getAccessToken retrieves or returns a cached DingTalk access token.
func (c *client) getAccessToken(ctx context.Context) (string, error) {
	c.tokenMu.Lock()
	defer c.tokenMu.Unlock()

	if c.tokenCache != "" && time.Now().Before(c.tokenExpAt) {
		return c.tokenCache, nil
	}

	body, _ := json.Marshal(map[string]string{
		"appKey":    c.appKey,
		"appSecret": c.appSecret,
	})
	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		c.baseURL+"/v1.0/oauth2/accessToken",
		bytes.NewReader(body),
	)
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
	if isAuthStatus(resp.StatusCode) {
		return "", fmt.Errorf("%w: dingtalk auth status=%d", datasource.ErrInvalidCredentials, resp.StatusCode)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("dingtalk auth error: status=%d body=%s",
			resp.StatusCode, truncate(string(respBody), logBodyPreviewBytes))
	}

	var result accessTokenResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return "", fmt.Errorf("decode token response: %w", err)
	}
	if result.AccessToken == "" {
		var apiErr apiErrorBody
		_ = json.Unmarshal(respBody, &apiErr)
		if apiErr.Message != "" {
			return "", fmt.Errorf("%w: %s", datasource.ErrInvalidCredentials, apiErr.Message)
		}
		return "", fmt.Errorf("%w: empty access token", datasource.ErrInvalidCredentials)
	}

	ttl := time.Duration(result.ExpireIn) * time.Second
	if ttl <= 0 {
		ttl = 2 * time.Hour
	}
	if ttl > 5*time.Minute {
		ttl -= 5 * time.Minute
	}
	c.tokenCache = result.AccessToken
	c.tokenExpAt = time.Now().Add(ttl)
	return c.tokenCache, nil
}

// getOperatorUnionID resolves the operator unionid required by DingTalk wiki APIs.
// If the caller already provided a unionid, it is reused without the OAPI lookup.
func (c *client) getOperatorUnionID(ctx context.Context) (string, error) {
	c.operatorMu.Lock()
	defer c.operatorMu.Unlock()

	if c.operatorUnionID != "" {
		return c.operatorUnionID, nil
	}
	if c.operatorUserID == "" {
		return "", fmt.Errorf("%w: operator_user_id is required", datasource.ErrInvalidCredentials)
	}

	token, err := c.getAccessToken(ctx)
	if err != nil {
		return "", err
	}

	form := url.Values{}
	form.Set("access_token", token)
	form.Set("userid", c.operatorUserID)
	form.Set("language", "zh_CN")

	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		c.oapiBaseURL+"/topapi/v2/user/get",
		bytes.NewBufferString(form.Encode()),
	)
	if err != nil {
		return "", fmt.Errorf("create user detail request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded;charset=utf-8")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("request user detail: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read user detail response: %w", err)
	}
	if isAuthStatus(resp.StatusCode) {
		return "", fmt.Errorf("%w: dingtalk user detail status=%d", datasource.ErrInvalidCredentials, resp.StatusCode)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("dingtalk user detail error: status=%d body=%s",
			resp.StatusCode, truncate(string(respBody), logBodyPreviewBytes))
	}

	var result userGetResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return "", fmt.Errorf("decode user detail response: %w", err)
	}
	if !isZeroErrCode(result.ErrCode) {
		return "", fmt.Errorf("%w: dingtalk user detail errcode=%v errmsg=%s",
			datasource.ErrInvalidCredentials, result.ErrCode, result.ErrMsg)
	}
	if result.Result.UnionID == "" {
		return "", fmt.Errorf("%w: empty unionid for dingtalk userid %s",
			datasource.ErrInvalidCredentials, c.operatorUserID)
	}

	c.operatorUnionID = result.Result.UnionID
	return c.operatorUnionID, nil
}

// doRequest executes an authenticated DingTalk OpenAPI request and decodes JSON.
func (c *client) doRequest(
	ctx context.Context,
	method string,
	path string,
	query map[string]string,
	body interface{},
	result interface{},
) error {
	token, err := c.getAccessToken(ctx)
	if err != nil {
		return err
	}

	reqURL, err := url.Parse(c.baseURL + path)
	if err != nil {
		return fmt.Errorf("parse request URL: %w", err)
	}
	values := reqURL.Query()
	for k, v := range query {
		if v != "" {
			values.Set(k, v)
		}
	}
	reqURL.RawQuery = values.Encode()

	var bodyReader io.Reader
	if body != nil {
		bodyBytes, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshal request body: %w", err)
		}
		bodyReader = bytes.NewReader(bodyBytes)
	}

	req, err := http.NewRequestWithContext(ctx, method, reqURL.String(), bodyReader)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	req.Header.Set("x-acs-dingtalk-access-token", token)

	logger.Infof(ctx, "[DingTalk] %s %s", method, reqURL.RequestURI())

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("execute request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read response body: %w", err)
	}
	bodyPreview := truncate(string(respBody), logBodyPreviewBytes)
	logger.Infof(ctx, "[DingTalk] %s %s status=%d bodyLen=%d body=%s",
		method, path, resp.StatusCode, len(respBody), bodyPreview)

	if isAuthStatus(resp.StatusCode) {
		return fmt.Errorf("%w: dingtalk api status=%d body=%s",
			datasource.ErrInvalidCredentials, resp.StatusCode, bodyPreview)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var apiErr apiErrorBody
		_ = json.Unmarshal(respBody, &apiErr)
		if apiErr.Message != "" {
			return fmt.Errorf("dingtalk api error: status=%d code=%s msg=%s",
				resp.StatusCode, apiErr.Code, apiErr.Message)
		}
		return fmt.Errorf("dingtalk api error: status=%d body=%s", resp.StatusCode, bodyPreview)
	}

	if result != nil {
		if err := json.Unmarshal(respBody, result); err != nil {
			return fmt.Errorf("decode response: %w", err)
		}
	}
	return nil
}

// Ping verifies credentials by listing accessible workspaces.
func (c *client) Ping(ctx context.Context) error {
	_, err := c.ListWorkspaces(ctx)
	return err
}

// ListWorkspaces returns all DingTalk wiki workspaces accessible to the operator.
func (c *client) ListWorkspaces(ctx context.Context) ([]workspace, error) {
	var all []workspace
	nextToken := ""
	operatorID, err := c.getOperatorUnionID(ctx)
	if err != nil {
		return nil, err
	}
	for {
		var resp workspaceListResponse
		err = c.doRequest(ctx, http.MethodGet, "/v2.0/wiki/workspaces", map[string]string{
			"operatorId":         operatorID,
			"maxResults":         strconv.Itoa(workspacePageSize),
			"nextToken":          nextToken,
			"withPermissionRole": "false",
			"orderBy":            "MODIFIED_TIME_DESC",
		}, nil, &resp)
		if err != nil {
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

// ListNodes returns direct child nodes under a DingTalk wiki parent node.
func (c *client) ListNodes(ctx context.Context, parentNodeID string) ([]node, error) {
	var all []node
	nextToken := ""
	operatorID, err := c.getOperatorUnionID(ctx)
	if err != nil {
		return nil, err
	}
	for {
		var resp nodeListResponse
		err = c.doRequest(ctx, http.MethodGet, "/v2.0/wiki/nodes", map[string]string{
			"operatorId":         operatorID,
			"parentNodeId":       parentNodeID,
			"maxResults":         strconv.Itoa(defaultPageSize),
			"nextToken":          nextToken,
			"withPermissionRole": "false",
		}, nil, &resp)
		if err != nil {
			return nil, err
		}
		all = append(all, resp.Nodes...)
		if resp.NextToken == "" {
			break
		}
		nextToken = resp.NextToken
	}
	return all, nil
}

// GetNode returns metadata for a single DingTalk wiki node.
func (c *client) GetNode(ctx context.Context, nodeID string) (node, error) {
	var resp nodeInfoResponse
	operatorID, err := c.getOperatorUnionID(ctx)
	if err != nil {
		return node{}, err
	}
	err = c.doRequest(ctx, http.MethodGet, "/v2.0/wiki/nodes/"+url.PathEscape(nodeID), map[string]string{
		"operatorId":              operatorID,
		"withStatisticalInfo":     "true",
		"withPermissionRole":      "false",
		"withDocumentCreatorInfo": "false",
	}, nil, &resp)
	if err != nil {
		return node{}, err
	}
	return resp.Node, nil
}

// QueryDocBlocks returns the raw block tree for an online DingTalk document.
func (c *client) QueryDocBlocks(ctx context.Context, docKey string) ([]docBlock, error) {
	var resp docBlocksResponse
	operatorID, err := c.getOperatorUnionID(ctx)
	if err != nil {
		return nil, err
	}
	err = c.doRequest(
		ctx,
		http.MethodGet,
		"/v1.0/doc/suites/documents/"+url.PathEscape(docKey)+"/blocks",
		map[string]string{"operatorId": operatorID},
		nil,
		&resp,
	)
	if err != nil {
		return nil, err
	}
	return resp.Result.Data, nil
}

// isZeroErrCode normalizes DingTalk OAPI errcode values that may be strings or numbers.
func isZeroErrCode(value interface{}) bool {
	switch v := value.(type) {
	case nil:
		return true
	case float64:
		return v == 0
	case string:
		return v == "" || v == "0"
	default:
		return fmt.Sprint(v) == "0"
	}
}

func isAuthStatus(statusCode int) bool {
	return statusCode == http.StatusUnauthorized || statusCode == http.StatusForbidden
}

// truncate returns s truncated to maxLen with "..." appended if longer.
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
