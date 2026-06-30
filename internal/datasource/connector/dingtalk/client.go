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
	defaultTimeout    = 30 * time.Second
	defaultPageSize   = 50
	workspacePageSize = 30
	userAgent         = "WeKnora-DingTalk-Connector/1.0"
)

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

func newClient(cfg *Config) *client {
	return &client{
		baseURL:    cfg.GetBaseURL(),
		appKey:     cfg.AppKey,
		appSecret:  cfg.AppSecret,
		operatorID: cfg.OperatorUnionID,
		httpClient: &http.Client{Timeout: defaultTimeout},
	}
}

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
	req.Header.Set("User-Agent", userAgent)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("request token: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read token response: %w", err)
	}
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return "", fmt.Errorf("%w: dingtalk auth status=%d", datasource.ErrInvalidCredentials, resp.StatusCode)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("dingtalk auth error: status=%d body=%s", resp.StatusCode, truncate(string(respBody), 500))
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
	req.Header.Set("User-Agent", userAgent)
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
	logger.Infof(ctx, "[DingTalk] %s %s status=%d bodyLen=%d body=%s",
		method, path, resp.StatusCode, len(respBody), truncate(string(respBody), 500))

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return fmt.Errorf("%w: dingtalk api status=%d body=%s",
			datasource.ErrInvalidCredentials, resp.StatusCode, truncate(string(respBody), 500))
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var apiErr apiErrorBody
		_ = json.Unmarshal(respBody, &apiErr)
		if apiErr.Message != "" {
			return fmt.Errorf("dingtalk api error: status=%d code=%s msg=%s",
				resp.StatusCode, apiErr.Code, apiErr.Message)
		}
		return fmt.Errorf("dingtalk api error: status=%d body=%s", resp.StatusCode, truncate(string(respBody), 500))
	}

	if result != nil {
		if err := json.Unmarshal(respBody, result); err != nil {
			return fmt.Errorf("decode response: %w", err)
		}
	}
	return nil
}

func (c *client) Ping(ctx context.Context) error {
	_, err := c.ListWorkspaces(ctx)
	return err
}

func (c *client) ListWorkspaces(ctx context.Context) ([]workspace, error) {
	var all []workspace
	nextToken := ""
	for {
		var resp workspaceListResponse
		err := c.doRequest(ctx, http.MethodGet, "/v2.0/wiki/workspaces", map[string]string{
			"operatorId":         c.operatorID,
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

func (c *client) ListNodes(ctx context.Context, parentNodeID string) ([]node, error) {
	var all []node
	nextToken := ""
	for {
		var resp nodeListResponse
		err := c.doRequest(ctx, http.MethodGet, "/v2.0/wiki/nodes", map[string]string{
			"operatorId":         c.operatorID,
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

func (c *client) GetNode(ctx context.Context, nodeID string) (node, error) {
	var resp nodeInfoResponse
	err := c.doRequest(ctx, http.MethodGet, "/v2.0/wiki/nodes/"+url.PathEscape(nodeID), map[string]string{
		"operatorId":              c.operatorID,
		"withStatisticalInfo":     "true",
		"withPermissionRole":      "false",
		"withDocumentCreatorInfo": "false",
	}, nil, &resp)
	if err != nil {
		return node{}, err
	}
	return resp.Node, nil
}

func (c *client) QueryDocBlocks(ctx context.Context, docKey string) ([]docBlock, error) {
	var resp docBlocksResponse
	err := c.doRequest(
		ctx,
		http.MethodGet,
		"/v1.0/doc/suites/documents/"+url.PathEscape(docKey)+"/blocks",
		map[string]string{"operatorId": c.operatorID},
		nil,
		&resp,
	)
	if err != nil {
		return nil, err
	}
	return resp.Result.Data, nil
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
