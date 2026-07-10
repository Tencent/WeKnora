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
	"time"

	"github.com/Tencent/WeKnora/internal/datasource"
)

const defaultTimeout = 30 * time.Second

type client struct {
	baseURL    string
	appKey     string
	appSecret  string
	httpClient *http.Client
	token      string
	tokenExpAt time.Time
}

func newClient(cfg *Config) *client {
	return &client{
		baseURL:    cfg.GetBaseURL(),
		appKey:     cfg.AppKey,
		appSecret:  cfg.AppSecret,
		httpClient: datasource.NewConnectorHTTPClient(defaultTimeout),
	}
}

func (c *client) accessToken(ctx context.Context) (string, error) {
	if c.token != "" && time.Now().Before(c.tokenExpAt) {
		return c.token, nil
	}
	body, _ := json.Marshal(map[string]string{
		"appKey":    c.appKey,
		"appSecret": c.appSecret,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1.0/oauth2/accessToken", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")

	var result tokenResponse
	if err := c.do(req, &result, false); err != nil {
		return "", fmt.Errorf("get dingtalk access token: %w", err)
	}
	if result.AccessToken == "" {
		return "", fmt.Errorf("empty access token from dingtalk")
	}
	c.token = result.AccessToken
	expireIn := result.ExpireIn
	if expireIn <= 0 {
		expireIn = 7200
	}
	c.tokenExpAt = time.Now().Add(time.Duration(expireIn)*time.Second - 5*time.Minute)
	return c.token, nil
}

func (c *client) get(ctx context.Context, path string, query map[string]string, out any) error {
	token, err := c.accessToken(ctx)
	if err != nil {
		return err
	}
	reqURL := c.baseURL + path + buildQuery(query)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("x-acs-dingtalk-access-token", token)
	return c.do(req, out, true)
}

func (c *client) do(req *http.Request, out any, authenticated bool) error {
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	body, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		return readErr
	}
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return fmt.Errorf("%w: status=%d body=%s", datasource.ErrInvalidCredentials, resp.StatusCode, truncate(string(body), 500))
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("dingtalk api error: status=%d body=%s", resp.StatusCode, truncate(string(body), 500))
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	if authenticated && isDingTalkError(out) {
		return fmt.Errorf("dingtalk api returned error payload: %s", truncate(string(body), 500))
	}
	return nil
}

func (c *client) Ping(ctx context.Context) error {
	_, err := c.accessToken(ctx)
	if err != nil {
		return err
	}
	_, err = c.ListSpaces(ctx)
	return err
}

func (c *client) ListSpaces(ctx context.Context) ([]docSpace, error) {
	var resp spaceListResponse
	if err := c.get(ctx, "/v1.0/doc/spaces", nil, &resp); err != nil {
		return nil, err
	}
	return resp.items(), nil
}

func (c *client) ListNodes(ctx context.Context, spaceID string, parentID string) ([]docNode, error) {
	var resp nodeListResponse
	query := map[string]string{}
	if parentID != "" {
		query["parentId"] = parentID
	}
	if err := c.get(ctx, fmt.Sprintf("/v1.0/doc/spaces/%s/nodes", url.PathEscape(spaceID)), query, &resp); err != nil {
		return nil, err
	}
	return resp.items(), nil
}

func (c *client) GetDocumentContent(ctx context.Context, documentID string) (contentResponse, error) {
	var resp contentResponse
	if err := c.get(ctx, fmt.Sprintf("/v1.0/doc/documents/%s/content", url.PathEscape(documentID)), nil, &resp); err != nil {
		return contentResponse{}, err
	}
	return resp.normalized(), nil
}

func buildQuery(params map[string]string) string {
	if len(params) == 0 {
		return ""
	}
	values := url.Values{}
	for key, value := range params {
		if strings.TrimSpace(value) != "" {
			values.Set(key, value)
		}
	}
	if len(values) == 0 {
		return ""
	}
	return "?" + values.Encode()
}

func isDingTalkError(v any) bool {
	b, err := json.Marshal(v)
	if err != nil {
		return false
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return false
	}
	if code, ok := m["errcode"].(float64); ok && code != 0 {
		return true
	}
	if code, ok := m["code"].(string); ok && code != "" && code != "0" {
		return true
	}
	return false
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
