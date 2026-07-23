package dingtalk

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"

	"github.com/Tencent/WeKnora/internal/datasource"
	"github.com/Tencent/WeKnora/internal/logger"
)

const (
	defaultTimeout        = 30 * time.Second
	defaultRequestsPerSec = 3
	defaultPageSize       = 30
	blockPageSize         = 100
	maxPaginationPages    = 1000
	maxResponseBytes      = 16 << 20
	maxTransientRetries   = 3
	maxRetryAfter         = 30 * time.Second
	userAgent             = "WeKnora-DingTalk-Connector/1.0"
)

type dingTalkAPI interface {
	Ping(context.Context) error
	ListWorkspaces(context.Context) ([]workspace, error)
	GetWorkspace(context.Context, string) (workspace, error)
	ListNodes(context.Context, string) ([]wikiNode, error)
	GetNode(context.Context, string) (wikiNode, error)
	GetDocumentBlocks(context.Context, string) ([]json.RawMessage, error)
}

type client struct {
	baseURL      string
	clientID     string
	clientSecret string
	operatorID   string
	httpClient   *http.Client
	limiter      *rate.Limiter
	sleep        func(context.Context, time.Duration) error

	tokenMu    sync.Mutex
	token      string
	tokenExpAt time.Time
}

func newClient(cfg *Config) *client {
	return &client{
		baseURL:      cfg.GetBaseURL(),
		clientID:     cfg.ClientID,
		clientSecret: cfg.ClientSecret,
		operatorID:   cfg.OperatorID,
		httpClient:   datasource.NewConnectorHTTPClient(defaultTimeout),
		limiter:      rate.NewLimiter(rate.Limit(defaultRequestsPerSec), defaultRequestsPerSec),
		sleep:        sleepContext,
	}
}

type dingTalkAPIError struct {
	StatusCode int
	Code       string
	Message    string
	RequestID  string
}

func (e *dingTalkAPIError) Error() string {
	parts := []string{fmt.Sprintf("dingtalk api error: status=%d", e.StatusCode)}
	if e.Code != "" {
		parts = append(parts, "code="+e.Code)
	}
	if e.Message != "" {
		parts = append(parts, "message="+e.Message)
	}
	if e.RequestID != "" {
		parts = append(parts, "request_id="+e.RequestID)
	}
	return strings.Join(parts, " ")
}

func (c *client) getAccessToken(ctx context.Context) (string, error) {
	c.tokenMu.Lock()
	defer c.tokenMu.Unlock()

	if c.token != "" && time.Until(c.tokenExpAt) > 0 {
		return c.token, nil
	}

	payload, err := json.Marshal(map[string]string{
		"appKey":    c.clientID,
		"appSecret": c.clientSecret,
	})
	if err != nil {
		return "", fmt.Errorf("marshal dingtalk token request: %w", err)
	}

	var lastErr error
	for attempt := 0; attempt <= maxTransientRetries; attempt++ {
		if err := c.limiter.Wait(ctx); err != nil {
			return "", fmt.Errorf("dingtalk rate limiter: %w", err)
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1.0/oauth2/accessToken", bytes.NewReader(payload))
		if err != nil {
			return "", fmt.Errorf("create dingtalk token request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json; charset=utf-8")
		req.Header.Set("User-Agent", userAgent)

		started := time.Now()
		resp, err := c.httpClient.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("request dingtalk access token: %w", err)
			if attempt < maxTransientRetries {
				if err := c.sleepFor(ctx, retryDelay(attempt)); err != nil {
					return "", err
				}
				continue
			}
			return "", lastErr
		}
		body, readErr := readLimitedBody(resp.Body)
		resp.Body.Close()
		logger.Infof(ctx, "[DingTalk] POST /v1.0/oauth2/accessToken status=%d duration=%s bytes=%d",
			resp.StatusCode, time.Since(started).Round(time.Millisecond), len(body))
		if readErr != nil {
			return "", fmt.Errorf("read dingtalk token response: %w", readErr)
		}

		if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
			lastErr = apiErrorFromResponse(resp.StatusCode, body)
			if attempt < maxTransientRetries {
				wait := retryDelay(attempt)
				if resp.StatusCode == http.StatusTooManyRequests {
					wait = parseRetryAfter(resp.Header.Get("Retry-After"), wait)
				}
				if err := c.sleepFor(ctx, wait); err != nil {
					return "", err
				}
				continue
			}
			return "", lastErr
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			apiErr := apiErrorFromResponse(resp.StatusCode, body)
			return "", fmt.Errorf("%w: %w", datasource.ErrInvalidCredentials, apiErr)
		}

		var tokenResp accessTokenResponse
		if err := json.Unmarshal(body, &tokenResp); err != nil {
			return "", fmt.Errorf("decode dingtalk token response: %w", err)
		}
		token := strings.TrimSpace(tokenResp.token())
		if token == "" {
			return "", fmt.Errorf("%w: dingtalk returned an empty access token", datasource.ErrInvalidCredentials)
		}

		ttl := time.Duration(tokenResp.ExpireIn) * time.Second
		if ttl <= 0 {
			ttl = 90 * time.Minute
		}
		if ttl > 5*time.Minute {
			ttl -= 5 * time.Minute
		}
		c.token = token
		c.tokenExpAt = time.Now().Add(ttl)
		logger.Infof(ctx, "[DingTalk] access token refreshed client_id=%s expires_in=%ds", redact(c.clientID), tokenResp.ExpireIn)
		return c.token, nil
	}
	return "", lastErr
}

func (c *client) invalidateToken(token string) {
	c.tokenMu.Lock()
	defer c.tokenMu.Unlock()
	if c.token == token {
		c.token = ""
		c.tokenExpAt = time.Time{}
	}
}

func (c *client) doRequest(ctx context.Context, method, path string, result interface{}) error {
	transientAttempt := 0
	authRefreshed := false

	for {
		token, err := c.getAccessToken(ctx)
		if err != nil {
			return err
		}
		if err := c.limiter.Wait(ctx); err != nil {
			return fmt.Errorf("dingtalk rate limiter: %w", err)
		}

		req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, nil)
		if err != nil {
			return fmt.Errorf("create dingtalk request: %w", err)
		}
		req.Header.Set("x-acs-dingtalk-access-token", token)
		req.Header.Set("Accept", "application/json")
		req.Header.Set("User-Agent", userAgent)

		started := time.Now()
		resp, err := c.httpClient.Do(req)
		if err != nil {
			if transientAttempt < maxTransientRetries {
				if err := c.sleepFor(ctx, retryDelay(transientAttempt)); err != nil {
					return err
				}
				transientAttempt++
				continue
			}
			return fmt.Errorf("execute dingtalk request: %w", err)
		}

		body, readErr := readLimitedBody(resp.Body)
		resp.Body.Close()
		endpoint := path
		if idx := strings.IndexByte(endpoint, '?'); idx >= 0 {
			endpoint = endpoint[:idx]
		}
		logger.Infof(ctx, "[DingTalk] %s %s status=%d duration=%s bytes=%d",
			method, endpoint, resp.StatusCode, time.Since(started).Round(time.Millisecond), len(body))
		if readErr != nil {
			return fmt.Errorf("read dingtalk response: %w", readErr)
		}

		if resp.StatusCode == http.StatusUnauthorized && !authRefreshed {
			c.invalidateToken(token)
			authRefreshed = true
			continue
		}
		if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
			apiErr := apiErrorFromResponse(resp.StatusCode, body)
			if transientAttempt < maxTransientRetries {
				wait := retryDelay(transientAttempt)
				if resp.StatusCode == http.StatusTooManyRequests {
					wait = parseRetryAfter(resp.Header.Get("Retry-After"), wait)
				}
				if err := c.sleepFor(ctx, wait); err != nil {
					return err
				}
				transientAttempt++
				continue
			}
			return apiErr
		}
		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
			return fmt.Errorf("%w: %w", datasource.ErrInvalidCredentials, apiErrorFromResponse(resp.StatusCode, body))
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return apiErrorFromResponse(resp.StatusCode, body)
		}
		if result == nil || len(body) == 0 {
			return nil
		}
		if err := json.Unmarshal(body, result); err != nil {
			return fmt.Errorf("decode dingtalk response for %s: %w", endpoint, err)
		}
		return nil
	}
}

func (c *client) sleepFor(ctx context.Context, duration time.Duration) error {
	if c.sleep != nil {
		return c.sleep(ctx, duration)
	}
	return sleepContext(ctx, duration)
}

func (c *client) Ping(ctx context.Context) error {
	_, _, err := c.listWorkspacePage(ctx, "", 1)
	return err
}

func (c *client) ListWorkspaces(ctx context.Context) ([]workspace, error) {
	var all []workspace
	seenTokens := make(map[string]struct{})
	nextToken := ""
	for page := 0; page < maxPaginationPages; page++ {
		items, token, err := c.listWorkspacePage(ctx, nextToken, defaultPageSize)
		if err != nil {
			return nil, err
		}
		all = append(all, items...)
		if token == "" {
			return all, nil
		}
		if _, exists := seenTokens[token]; exists {
			return nil, fmt.Errorf("dingtalk workspace pagination repeated nextToken")
		}
		seenTokens[token] = struct{}{}
		nextToken = token
	}
	return nil, fmt.Errorf("dingtalk workspace pagination exceeded %d pages", maxPaginationPages)
}

func (c *client) listWorkspacePage(ctx context.Context, nextToken string, pageSize int) ([]workspace, string, error) {
	query := url.Values{}
	query.Set("operatorId", c.operatorID)
	query.Set("maxResults", strconv.Itoa(pageSize))
	if nextToken != "" {
		query.Set("nextToken", nextToken)
	}
	var response workspaceListResponse
	if err := c.doRequest(ctx, http.MethodGet, "/v2.0/wiki/workspaces?"+query.Encode(), &response); err != nil {
		return nil, "", fmt.Errorf("list dingtalk workspaces: %w", err)
	}
	return response.Workspaces, response.NextToken, nil
}

func (c *client) GetWorkspace(ctx context.Context, workspaceID string) (workspace, error) {
	query := url.Values{"operatorId": []string{c.operatorID}}
	path := "/v2.0/wiki/workspaces/" + url.PathEscape(workspaceID) + "?" + query.Encode()
	var response workspaceResponse
	if err := c.doRequest(ctx, http.MethodGet, path, &response); err != nil {
		return workspace{}, fmt.Errorf("get dingtalk workspace %s: %w", workspaceID, err)
	}
	if response.Workspace.WorkspaceID == "" {
		return workspace{}, fmt.Errorf("dingtalk workspace %s response has no workspace", workspaceID)
	}
	return response.Workspace, nil
}

func (c *client) ListNodes(ctx context.Context, parentNodeID string) ([]wikiNode, error) {
	var all []wikiNode
	seenTokens := make(map[string]struct{})
	nextToken := ""
	for page := 0; page < maxPaginationPages; page++ {
		query := url.Values{}
		query.Set("operatorId", c.operatorID)
		query.Set("parentNodeId", parentNodeID)
		query.Set("maxResults", strconv.Itoa(defaultPageSize))
		if nextToken != "" {
			query.Set("nextToken", nextToken)
		}
		var response nodeListResponse
		if err := c.doRequest(ctx, http.MethodGet, "/v2.0/wiki/nodes?"+query.Encode(), &response); err != nil {
			return nil, fmt.Errorf("list dingtalk nodes under %s: %w", parentNodeID, err)
		}
		for i := range response.Nodes {
			response.Nodes[i].ParentNodeID = parentNodeID
		}
		all = append(all, response.Nodes...)
		if response.NextToken == "" {
			return all, nil
		}
		if _, exists := seenTokens[response.NextToken]; exists {
			return nil, fmt.Errorf("dingtalk node pagination repeated nextToken for parent %s", parentNodeID)
		}
		seenTokens[response.NextToken] = struct{}{}
		nextToken = response.NextToken
	}
	return nil, fmt.Errorf("dingtalk node pagination exceeded %d pages for parent %s", maxPaginationPages, parentNodeID)
}

func (c *client) GetNode(ctx context.Context, nodeID string) (wikiNode, error) {
	query := url.Values{}
	query.Set("operatorId", c.operatorID)
	query.Set("withStatisticalInfo", "true")
	path := "/v2.0/wiki/nodes/" + url.PathEscape(nodeID) + "?" + query.Encode()
	var response nodeResponse
	if err := c.doRequest(ctx, http.MethodGet, path, &response); err != nil {
		return wikiNode{}, fmt.Errorf("get dingtalk node %s: %w", nodeID, err)
	}
	if response.Node.NodeID == "" {
		return wikiNode{}, fmt.Errorf("dingtalk node %s response has no node", nodeID)
	}
	return response.Node, nil
}

func (c *client) GetDocumentBlocks(ctx context.Context, nodeID string) ([]json.RawMessage, error) {
	var all []json.RawMessage
	for page := 0; page < maxPaginationPages; page++ {
		start := page * blockPageSize
		query := url.Values{}
		query.Set("operatorId", c.operatorID)
		query.Set("startIndex", strconv.Itoa(start))
		query.Set("endIndex", strconv.Itoa(start+blockPageSize-1))
		path := "/v1.0/doc/suites/documents/" + url.PathEscape(nodeID) + "/blocks?" + query.Encode()
		var response docBlocksResponse
		if err := c.doRequest(ctx, http.MethodGet, path, &response); err != nil {
			return nil, fmt.Errorf("query dingtalk document blocks for %s: %w", nodeID, err)
		}
		if response.Success != nil && !*response.Success {
			return nil, fmt.Errorf("dingtalk document block query for %s returned success=false", nodeID)
		}
		all = append(all, response.Result.Data...)
		if len(response.Result.Data) < blockPageSize {
			return all, nil
		}
	}
	return nil, fmt.Errorf("dingtalk document block query exceeded %d pages for node %s", maxPaginationPages, nodeID)
}

func apiErrorFromResponse(status int, body []byte) error {
	var parsed apiErrorBody
	_ = json.Unmarshal(body, &parsed)
	message := parsed.message()
	if message == "" {
		message = http.StatusText(status)
	}
	return &dingTalkAPIError{
		StatusCode: status,
		Code:       parsed.code(),
		Message:    message,
		RequestID:  parsed.RequestID,
	}
}

func readLimitedBody(body io.Reader) ([]byte, error) {
	limited := io.LimitReader(body, maxResponseBytes+1)
	data, err := io.ReadAll(limited)
	if err != nil {
		return nil, err
	}
	if len(data) > maxResponseBytes {
		return nil, fmt.Errorf("response exceeds %d bytes", maxResponseBytes)
	}
	return data, nil
}

func retryDelay(attempt int) time.Duration {
	if attempt < 0 {
		attempt = 0
	}
	if attempt > 4 {
		attempt = 4
	}
	return time.Duration(1<<attempt) * 500 * time.Millisecond
}

func parseRetryAfter(value string, fallback time.Duration) time.Duration {
	value = strings.TrimSpace(value)
	wait := fallback
	if value != "" {
		if seconds, err := strconv.Atoi(value); err == nil {
			if seconds <= 0 {
				wait = 100 * time.Millisecond
			} else {
				wait = time.Duration(seconds) * time.Second
			}
		} else if when, err := http.ParseTime(value); err == nil {
			wait = time.Until(when)
			if wait <= 0 {
				wait = 100 * time.Millisecond
			}
		}
	}
	if wait > maxRetryAfter {
		return maxRetryAfter
	}
	return wait
}

func sleepContext(ctx context.Context, duration time.Duration) error {
	if duration <= 0 {
		return nil
	}
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func isContextError(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}
