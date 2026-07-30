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
)

const (
	defaultTimeout = 30 * time.Second
	userAgent      = "WeKnora-DingTalk-Connector/1.0"
)

type client struct {
	baseURL    string
	appKey     string
	appSecret  string
	operatorID string
	httpClient *http.Client

	tokenMu    sync.Mutex
	token      string
	tokenUntil time.Time
}

func newClient(cfg *Config) *client {
	return &client{
		baseURL:    cfg.GetBaseURL(),
		appKey:     strings.TrimSpace(cfg.AppKey),
		appSecret:  strings.TrimSpace(cfg.AppSecret),
		operatorID: strings.TrimSpace(cfg.OperatorID),
		httpClient: datasource.NewConnectorHTTPClient(defaultTimeout),
	}
}

func (c *client) Ping(ctx context.Context) error {
	_, err := c.accessToken(ctx)
	return err
}

func (c *client) accessToken(ctx context.Context) (string, error) {
	c.tokenMu.Lock()
	defer c.tokenMu.Unlock()

	if c.token != "" && time.Now().Before(c.tokenUntil.Add(-time.Minute)) {
		return c.token, nil
	}

	body, err := json.Marshal(map[string]string{
		"appKey":    c.appKey,
		"appSecret": c.appSecret,
	})
	if err != nil {
		return "", fmt.Errorf("marshal access token request: %w", err)
	}

	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		c.baseURL+"/v1.0/oauth2/accessToken",
		bytes.NewReader(body),
	)
	if err != nil {
		return "", fmt.Errorf("create access token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", userAgent)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("request dingtalk access token: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read access token response: %w", err)
	}
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return "", fmt.Errorf("%w: status=%d body=%s", datasource.ErrInvalidCredentials, resp.StatusCode, truncate(string(respBody), 500))
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("dingtalk access token failed: status=%d body=%s", resp.StatusCode, truncate(string(respBody), 500))
	}

	var tokenResp accessTokenResponse
	if err := decodeObject(respBody, &tokenResp, "data", "result"); err != nil {
		return "", fmt.Errorf("decode access token response: %w", err)
	}
	if tokenResp.ErrCode != 0 {
		return "", fmt.Errorf("dingtalk access token failed: errcode=%d message=%s", tokenResp.ErrCode, tokenResp.Message)
	}
	if !successCode(tokenResp.Code) {
		return "", fmt.Errorf("dingtalk access token failed: code=%s message=%s", tokenResp.Code, tokenResp.Message)
	}
	if strings.TrimSpace(tokenResp.AccessToken) == "" {
		return "", fmt.Errorf("empty access token from dingtalk")
	}

	ttl := time.Duration(tokenResp.ExpireIn) * time.Second
	if ttl <= 0 {
		ttl = 2 * time.Hour
	}
	c.token = tokenResp.AccessToken
	c.tokenUntil = time.Now().Add(ttl)
	return c.token, nil
}

func (c *client) doRequest(ctx context.Context, method, path string, result interface{}) error {
	token, err := c.accessToken(ctx)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, nil)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("x-acs-dingtalk-access-token", token)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", userAgent)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("execute request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read response body: %w", err)
	}
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return fmt.Errorf("%w: status=%d body=%s", datasource.ErrInvalidCredentials, resp.StatusCode, truncate(string(body), 500))
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var apiErr apiErrorBody
		_ = json.Unmarshal(body, &apiErr)
		msg := firstNonEmpty(apiErr.Message, apiErr.ErrMsg, truncate(string(body), 500))
		return fmt.Errorf("dingtalk api error: status=%d code=%s errcode=%d message=%s", resp.StatusCode, apiErr.Code, apiErr.ErrCode, msg)
	}
	if result == nil {
		return nil
	}
	if err := decodeDingTalkBody(body, result); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}

func (c *client) ListWorkspaces(ctx context.Context) ([]workspace, error) {
	var out []workspace
	nextToken := ""
	for {
		query := url.Values{}
		query.Set("operatorId", c.operatorID)
		query.Set("maxResults", "30")
		if nextToken != "" {
			query.Set("nextToken", nextToken)
		}
		var page workspaceListResponse
		if err := c.doRequest(ctx, http.MethodGet, "/v2.0/wiki/workspaces?"+query.Encode(), &page); err != nil {
			return nil, err
		}
		out = append(out, page.Workspaces...)
		nextToken = strings.TrimSpace(page.NextToken)
		if nextToken == "" {
			break
		}
	}
	return out, nil
}

func (c *client) GetWorkspace(ctx context.Context, workspaceID string) (workspace, error) {
	query := url.Values{}
	query.Set("operatorId", c.operatorID)
	path := "/v2.0/wiki/workspaces/" + url.PathEscape(workspaceID) + "?" + query.Encode()
	var out workspace
	if err := c.doRequest(ctx, http.MethodGet, path, objectTarget(&out, "workspace")); err != nil {
		return workspace{}, err
	}
	return out, nil
}

func (c *client) ListNodes(ctx context.Context, workspaceID, parentNodeID string) ([]node, error) {
	var out []node
	nextToken := ""
	for {
		query := url.Values{}
		query.Set("operatorId", c.operatorID)
		query.Set("parentNodeId", parentNodeID)
		query.Set("maxResults", "30")
		if nextToken != "" {
			query.Set("nextToken", nextToken)
		}
		var page nodeListResponse
		if err := c.doRequest(ctx, http.MethodGet, "/v2.0/wiki/nodes?"+query.Encode(), &page); err != nil {
			return nil, err
		}
		out = append(out, page.Nodes...)
		nextToken = strings.TrimSpace(page.NextToken)
		if nextToken == "" {
			break
		}
	}
	return out, nil
}

func (c *client) GetDocument(ctx context.Context, docID string) (documentDetail, error) {
	query := url.Values{}
	query.Set("operatorId", c.operatorID)
	query.Set("withStatisticalInfo", "true")
	path := "/v2.0/wiki/nodes/" + url.PathEscape(docID) + "?" + query.Encode()
	var out documentDetail
	if err := c.doRequest(ctx, http.MethodGet, path, objectTarget(&out, "node", "document", "doc", "file", "dentry")); err != nil {
		return documentDetail{}, err
	}
	return out, nil
}

type workspaceListResponse struct {
	Workspaces []workspace `json:"workspaces"`
	NextToken  string      `json:"nextToken"`
}

type nodeListResponse struct {
	Nodes     []node `json:"nodes"`
	NextToken string `json:"nextToken"`
}

type arrayDecodeTarget struct {
	out  interface{}
	keys []string
}

type objectDecodeTarget struct {
	out  interface{}
	keys []string
}

func arrayTarget(out interface{}, keys ...string) arrayDecodeTarget {
	return arrayDecodeTarget{out: out, keys: keys}
}

func objectTarget(out interface{}, keys ...string) objectDecodeTarget {
	return objectDecodeTarget{out: out, keys: keys}
}

func decodeDingTalkBody(body []byte, target interface{}) error {
	switch t := target.(type) {
	case arrayDecodeTarget:
		return decodeArray(body, t.out, t.keys...)
	case objectDecodeTarget:
		return decodeObject(body, t.out, t.keys...)
	default:
		return json.Unmarshal(body, target)
	}
}

func decodeArray(body []byte, out interface{}, keys ...string) error {
	raw, ok := findJSONValue(body, keys...)
	if !ok {
		raw = body
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return err
	}
	return nil
}

func decodeObject(body []byte, out interface{}, keys ...string) error {
	raw, ok := findJSONValue(body, keys...)
	if !ok {
		raw = body
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return err
	}
	return nil
}

func findJSONValue(body []byte, keys ...string) (json.RawMessage, bool) {
	return findJSONValueDepth(json.RawMessage(body), 0, keys...)
}

func findJSONValueDepth(raw json.RawMessage, depth int, keys ...string) (json.RawMessage, bool) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return nil, false
	}
	if trimmed[0] == '[' {
		return raw, true
	}
	if trimmed[0] != '{' || depth > 3 {
		return nil, false
	}

	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		return nil, false
	}
	for _, key := range keys {
		if val, ok := obj[key]; ok {
			return val, true
		}
	}
	for _, wrapper := range []string{"data", "result"} {
		if val, ok := obj[wrapper]; ok {
			if found, ok := findJSONValueDepth(val, depth+1, keys...); ok {
				return found, true
			}
		}
	}
	return nil, false
}

func successCode(code string) bool {
	code = strings.TrimSpace(strings.ToLower(code))
	return code == "" || code == "0" || code == "ok" || code == "success"
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
