package dingtalk

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/Tencent/WeKnora/internal/datasource"
	"github.com/Tencent/WeKnora/internal/logger"
)

const (
	defaultTimeout = 30 * time.Second
	pageSize       = 50
)

// Client wraps the DingTalk Open API for document/workspace operations.
type Client struct {
	baseURL    string
	appKey     string
	appSecret  string
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

// getAccessToken retrieves (or returns cached) access token.
// DingTalk tokens expire in 7200 seconds; we cache with a 5-minute safety margin.
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

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read token response: %w", err)
	}

	var result tokenResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return "", fmt.Errorf("decode token response: %w", err)
	}
	if result.ErrCode != 0 {
		return "", fmt.Errorf("dingtalk auth error: errcode=%d errmsg=%s", result.ErrCode, result.ErrMsg)
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

	logger.Infof(ctx, "[DingTalk] got accessToken: %s...%s expire=%ds",
		redactToken(result.AccessToken), redactToken(result.AccessToken), result.ExpireIn)

	return c.tokenCache, nil
}

// doRequest executes an authenticated API request and decodes the JSON response.
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

	logger.Infof(ctx, "[DingTalk] %s %s", method, path)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("execute request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read response body: %w", err)
	}

	logger.Infof(ctx, "[DingTalk] %s %s → status=%d bodyLen=%d body=%s",
		method, path, resp.StatusCode, len(respBody), truncate(string(respBody), 500))

	// Check DingTalk API-level errors
	var apiErr apiErrorBody
	if err := json.Unmarshal(respBody, &apiErr); err == nil && apiErr.ErrCode != 0 {
		return fmt.Errorf("dingtalk api error: errcode=%d errmsg=%s", apiErr.ErrCode, apiErr.ErrMsg)
	}

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return fmt.Errorf("%w: status=%d body=%s", datasource.ErrInvalidCredentials, resp.StatusCode, string(respBody))
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("dingtalk api error: status=%d body=%s", resp.StatusCode, string(respBody))
	}

	if result != nil {
		if err := json.Unmarshal(respBody, result); err != nil {
			return fmt.Errorf("decode response: %w", err)
		}
	}

	return nil
}

// Ping verifies the credentials by attempting to get an access token.
func (c *Client) Ping(ctx context.Context) error {
	_, err := c.getAccessToken(ctx)
	return err
}

// ListWorkspaces returns all workspaces accessible to the app.
func (c *Client) ListWorkspaces(ctx context.Context) ([]workspace, error) {
	var all []workspace
	cursor := int64(0)
	for {
		var resp workspaceListResponse
		path := fmt.Sprintf("/v1.0/doc/workspaces?maxResults=%d", pageSize)
		if cursor > 0 {
			path += fmt.Sprintf("&nextCursor=%d", cursor)
		}
		if err := c.doRequest(ctx, http.MethodGet, path, nil, &resp); err != nil {
			return nil, fmt.Errorf("list workspaces: %w", err)
		}
		all = append(all, resp.Workspaces...)
		if !resp.HasMore {
			break
		}
		cursor = resp.NextCursor
	}
	return all, nil
}

// ListDocuments lists all documents in a workspace, handling pagination.
func (c *Client) ListDocuments(ctx context.Context, workspaceID string) ([]document, error) {
	var all []document
	cursor := int64(0)
	for {
		var resp docListResponse
		path := fmt.Sprintf("/v1.0/doc/workspaces/%s/documents?maxResults=%d", workspaceID, pageSize)
		if cursor > 0 {
			path += fmt.Sprintf("&nextCursor=%d", cursor)
		}
		if err := c.doRequest(ctx, http.MethodGet, path, nil, &resp); err != nil {
			return nil, fmt.Errorf("list documents for workspace %s: %w", workspaceID, err)
		}
		all = append(all, resp.Documents...)
		if !resp.HasMore {
			break
		}
		cursor = resp.NextCursor
	}
	return all, nil
}

// GetDocumentContent fetches the full document content by document ID.
func (c *Client) GetDocumentContent(ctx context.Context, documentID string) (*docDetailResponse, error) {
	path := fmt.Sprintf("/v1.0/doc/documents/%s", documentID)
	var resp docDetailResponse
	if err := c.doRequest(ctx, http.MethodGet, path, nil, &resp); err != nil {
		return nil, fmt.Errorf("get document %s: %w", documentID, err)
	}
	return &resp, nil
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

func redactToken(t string) string {
	if len(t) < 12 {
		return "***"
	}
	return t[:6] + "..." + t[len(t)-4:]
}
