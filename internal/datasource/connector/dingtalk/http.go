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

	"github.com/Tencent/WeKnora/internal/datasource"
	"github.com/Tencent/WeKnora/internal/logger"
	"golang.org/x/time/rate"
)

const (
	requestTimeout    = 30 * time.Second
	workspacePageSize = 30
	nodePageSize      = 50
	blockPageSize     = 100
	maxPageCount      = 1000
	maxResponseBytes  = 16 << 20
	maxRetries        = 3
	accessTokenHeader = "x-acs-dingtalk-access-token"
	connectorAgent    = "WeKnora-DingTalk/1.0"
)

type dingTalkAPI interface {
	validate(context.Context) error
	listWorkspaces(context.Context) ([]workspace, error)
	getWorkspace(context.Context, string) (workspace, error)
	listChildren(context.Context, string) ([]node, error)
	getNode(context.Context, string) (node, error)
	listDocumentBlocks(context.Context, string) ([]json.RawMessage, error)
}

type httpClient struct {
	baseURL      string
	clientID     string
	clientSecret string
	operatorID   string
	transport    *http.Client
	limiter      *rate.Limiter
	sleep        func(context.Context, time.Duration) error

	tokenMu sync.Mutex
	token   string
	expires time.Time
}

func newHTTPClient(cfg *Config) *httpClient {
	return &httpClient{
		baseURL:      cfg.baseURL(),
		clientID:     cfg.ClientID,
		clientSecret: cfg.ClientSecret,
		operatorID:   cfg.OperatorID,
		transport:    datasource.NewConnectorHTTPClient(requestTimeout),
		limiter:      rate.NewLimiter(4, 4),
		sleep:        sleepWithContext,
	}
}

type apiError struct {
	StatusCode int
	Code       string
	Message    string
	RequestID  string
}

func (e *apiError) Error() string {
	parts := []string{fmt.Sprintf("dingtalk API status=%d", e.StatusCode)}
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

func (c *httpClient) accessToken(ctx context.Context) (string, error) {
	c.tokenMu.Lock()
	defer c.tokenMu.Unlock()

	if c.token != "" && time.Now().Before(c.expires) {
		return c.token, nil
	}
	payload, err := json.Marshal(map[string]string{
		"appKey":    c.clientID,
		"appSecret": c.clientSecret,
	})
	if err != nil {
		return "", fmt.Errorf("encode access-token request: %w", err)
	}

	var lastErr error
	for attempt := 0; attempt <= maxRetries; attempt++ {
		if err := c.limiter.Wait(ctx); err != nil {
			return "", err
		}
		req, err := http.NewRequestWithContext(
			ctx, http.MethodPost, c.baseURL+"/v1.0/oauth2/accessToken", bytes.NewReader(payload),
		)
		if err != nil {
			return "", fmt.Errorf("create access-token request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("User-Agent", connectorAgent)

		resp, err := c.transport.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("request dingtalk access token: %w", err)
			if attempt < maxRetries {
				if err := c.wait(ctx, retryDelay(attempt)); err != nil {
					return "", err
				}
				continue
			}
			return "", lastErr
		}
		body, readErr := readResponse(resp.Body)
		resp.Body.Close()
		if readErr != nil {
			return "", fmt.Errorf("read dingtalk access-token response: %w", readErr)
		}
		if isTransientStatus(resp.StatusCode) {
			lastErr = decodeAPIError(resp.StatusCode, body)
			if attempt < maxRetries {
				delay := retryDelay(attempt)
				if resp.StatusCode == http.StatusTooManyRequests {
					delay = retryAfter(resp.Header.Get("Retry-After"), delay)
				}
				if err := c.wait(ctx, delay); err != nil {
					return "", err
				}
				continue
			}
			return "", lastErr
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return "", fmt.Errorf("%w: %w",
				datasource.ErrInvalidCredentials, decodeAPIError(resp.StatusCode, body))
		}

		var token accessTokenResponse
		if err := json.Unmarshal(body, &token); err != nil {
			return "", fmt.Errorf("decode dingtalk access token: %w", err)
		}
		token.AccessToken = strings.TrimSpace(token.AccessToken)
		if token.AccessToken == "" {
			return "", fmt.Errorf("%w: dingtalk returned an empty access token",
				datasource.ErrInvalidCredentials)
		}
		ttl := time.Duration(token.ExpireIn) * time.Second
		if ttl <= 0 {
			ttl = 90 * time.Minute
		}
		if ttl > 5*time.Minute {
			ttl -= 5 * time.Minute
		}
		c.token = token.AccessToken
		c.expires = time.Now().Add(ttl)
		logger.Infof(ctx, "[DingTalk datasource] access token refreshed for client_id=%s",
			redactIdentifier(c.clientID))
		return c.token, nil
	}
	return "", lastErr
}

func (c *httpClient) invalidateToken(token string) {
	c.tokenMu.Lock()
	defer c.tokenMu.Unlock()
	if c.token == token {
		c.token = ""
		c.expires = time.Time{}
	}
}

func (c *httpClient) doJSON(
	ctx context.Context,
	method string,
	path string,
	result interface{},
) error {
	transientAttempts := 0
	refreshed := false

	for {
		token, err := c.accessToken(ctx)
		if err != nil {
			return err
		}
		if err := c.limiter.Wait(ctx); err != nil {
			return err
		}
		req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, nil)
		if err != nil {
			return fmt.Errorf("create dingtalk request: %w", err)
		}
		req.Header.Set(accessTokenHeader, token)
		req.Header.Set("Accept", "application/json")
		req.Header.Set("User-Agent", connectorAgent)

		start := time.Now()
		resp, err := c.transport.Do(req)
		if err != nil {
			if transientAttempts < maxRetries {
				if err := c.wait(ctx, retryDelay(transientAttempts)); err != nil {
					return err
				}
				transientAttempts++
				continue
			}
			return fmt.Errorf("execute dingtalk request: %w", err)
		}
		body, readErr := readResponse(resp.Body)
		resp.Body.Close()
		endpoint := path
		if index := strings.IndexByte(endpoint, '?'); index >= 0 {
			endpoint = endpoint[:index]
		}
		logger.Infof(ctx, "[DingTalk datasource] %s %s status=%d duration=%s bytes=%d",
			method, endpoint, resp.StatusCode, time.Since(start).Round(time.Millisecond), len(body))
		if readErr != nil {
			return fmt.Errorf("read dingtalk response: %w", readErr)
		}

		if resp.StatusCode == http.StatusUnauthorized && !refreshed {
			c.invalidateToken(token)
			refreshed = true
			continue
		}
		if isTransientStatus(resp.StatusCode) {
			apiErr := decodeAPIError(resp.StatusCode, body)
			if transientAttempts < maxRetries {
				delay := retryDelay(transientAttempts)
				if resp.StatusCode == http.StatusTooManyRequests {
					delay = retryAfter(resp.Header.Get("Retry-After"), delay)
				}
				if err := c.wait(ctx, delay); err != nil {
					return err
				}
				transientAttempts++
				continue
			}
			return apiErr
		}
		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
			return fmt.Errorf("%w: %w",
				datasource.ErrInvalidCredentials, decodeAPIError(resp.StatusCode, body))
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return decodeAPIError(resp.StatusCode, body)
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

func (c *httpClient) validate(ctx context.Context) error {
	_, _, err := c.workspacePage(ctx, "", 1)
	return err
}

func (c *httpClient) listWorkspaces(ctx context.Context) ([]workspace, error) {
	var all []workspace
	seen := make(map[string]struct{})
	next := ""
	for page := 0; page < maxPageCount; page++ {
		items, token, err := c.workspacePage(ctx, next, workspacePageSize)
		if err != nil {
			return nil, err
		}
		all = append(all, items...)
		if token == "" {
			return all, nil
		}
		if _, exists := seen[token]; exists {
			return nil, fmt.Errorf("dingtalk workspace pagination repeated nextToken")
		}
		seen[token] = struct{}{}
		next = token
	}
	return nil, fmt.Errorf("dingtalk workspace pagination exceeded %d pages", maxPageCount)
}

func (c *httpClient) workspacePage(
	ctx context.Context,
	next string,
	pageSize int,
) ([]workspace, string, error) {
	query := url.Values{
		"operatorId": {c.operatorID},
		"maxResults": {strconv.Itoa(pageSize)},
	}
	if next != "" {
		query.Set("nextToken", next)
	}
	var response workspaceListResponse
	if err := c.doJSON(ctx, http.MethodGet, "/v2.0/wiki/workspaces?"+query.Encode(), &response); err != nil {
		return nil, "", fmt.Errorf("list dingtalk workspaces: %w", err)
	}
	return response.Workspaces, response.NextToken, nil
}

func (c *httpClient) getWorkspace(ctx context.Context, workspaceID string) (workspace, error) {
	query := url.Values{"operatorId": {c.operatorID}}
	path := "/v2.0/wiki/workspaces/" + url.PathEscape(workspaceID) + "?" + query.Encode()
	var response workspaceResponse
	if err := c.doJSON(ctx, http.MethodGet, path, &response); err != nil {
		return workspace{}, fmt.Errorf("get dingtalk workspace: %w", err)
	}
	if response.Workspace.WorkspaceID == "" {
		return workspace{}, fmt.Errorf("dingtalk workspace response is empty")
	}
	return response.Workspace, nil
}

func (c *httpClient) listChildren(ctx context.Context, parentNodeID string) ([]node, error) {
	var all []node
	seen := make(map[string]struct{})
	next := ""
	for page := 0; page < maxPageCount; page++ {
		query := url.Values{
			"operatorId":   {c.operatorID},
			"parentNodeId": {parentNodeID},
			"maxResults":   {strconv.Itoa(nodePageSize)},
		}
		if next != "" {
			query.Set("nextToken", next)
		}
		var response nodeListResponse
		if err := c.doJSON(ctx, http.MethodGet, "/v2.0/wiki/nodes?"+query.Encode(), &response); err != nil {
			return nil, fmt.Errorf("list dingtalk nodes: %w", err)
		}
		for i := range response.Nodes {
			response.Nodes[i].ParentNodeID = parentNodeID
		}
		all = append(all, response.Nodes...)
		if response.NextToken == "" {
			return all, nil
		}
		if _, exists := seen[response.NextToken]; exists {
			return nil, fmt.Errorf("dingtalk node pagination repeated nextToken")
		}
		seen[response.NextToken] = struct{}{}
		next = response.NextToken
	}
	return nil, fmt.Errorf("dingtalk node pagination exceeded %d pages", maxPageCount)
}

func (c *httpClient) getNode(ctx context.Context, nodeID string) (node, error) {
	query := url.Values{
		"operatorId":          {c.operatorID},
		"withStatisticalInfo": {"true"},
	}
	path := "/v2.0/wiki/nodes/" + url.PathEscape(nodeID) + "?" + query.Encode()
	var response nodeResponse
	if err := c.doJSON(ctx, http.MethodGet, path, &response); err != nil {
		return node{}, fmt.Errorf("get dingtalk node: %w", err)
	}
	if response.Node.NodeID == "" {
		return node{}, fmt.Errorf("dingtalk node response is empty")
	}
	return response.Node, nil
}

func (c *httpClient) listDocumentBlocks(
	ctx context.Context,
	documentID string,
) ([]json.RawMessage, error) {
	var all []json.RawMessage
	for page := 0; page < maxPageCount; page++ {
		start := page * blockPageSize
		query := url.Values{
			"operatorId": {c.operatorID},
			"startIndex": {strconv.Itoa(start)},
			"endIndex":   {strconv.Itoa(start + blockPageSize - 1)},
		}
		path := "/v1.0/doc/suites/documents/" + url.PathEscape(documentID) +
			"/blocks?" + query.Encode()
		var response documentBlocksResponse
		if err := c.doJSON(ctx, http.MethodGet, path, &response); err != nil {
			return nil, fmt.Errorf("list dingtalk document blocks: %w", err)
		}
		if !response.Success {
			return nil, fmt.Errorf("dingtalk document block query returned success=false")
		}
		all = append(all, response.Result.Data...)
		if len(response.Result.Data) < blockPageSize {
			return all, nil
		}
	}
	return nil, fmt.Errorf("dingtalk document blocks exceeded %d pages", maxPageCount)
}

func decodeAPIError(status int, body []byte) error {
	var payload apiErrorPayload
	_ = json.Unmarshal(body, &payload)
	message := strings.TrimSpace(payload.Message)
	if message == "" {
		message = strings.TrimSpace(payload.ErrMsg)
	}
	if message == "" {
		message = http.StatusText(status)
	}
	code := rawScalar(payload.Code)
	if code == "" {
		code = rawScalar(payload.ErrCode)
	}
	return &apiError{
		StatusCode: status,
		Code:       code,
		Message:    message,
		RequestID:  strings.TrimSpace(payload.RequestID),
	}
}

func rawScalar(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return text
	}
	var number json.Number
	if json.Unmarshal(raw, &number) == nil {
		return number.String()
	}
	return ""
}

func readResponse(body io.Reader) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(body, maxResponseBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxResponseBytes {
		return nil, fmt.Errorf("dingtalk response exceeds %d bytes", maxResponseBytes)
	}
	return data, nil
}

func isTransientStatus(status int) bool {
	return status == http.StatusTooManyRequests || status >= 500
}

func retryDelay(attempt int) time.Duration {
	if attempt < 0 {
		attempt = 0
	}
	if attempt > 4 {
		attempt = 4
	}
	return time.Duration(1<<attempt) * 250 * time.Millisecond
}

func retryAfter(raw string, fallback time.Duration) time.Duration {
	const maxWait = 30 * time.Second
	wait := fallback
	if seconds, err := strconv.Atoi(strings.TrimSpace(raw)); err == nil && raw != "" {
		if seconds <= 0 {
			wait = 100 * time.Millisecond
		} else {
			wait = time.Duration(seconds) * time.Second
		}
	} else if parsed, err := http.ParseTime(raw); err == nil {
		wait = time.Until(parsed)
		if wait <= 0 {
			wait = 100 * time.Millisecond
		}
	}
	if wait > maxWait {
		return maxWait
	}
	return wait
}

func sleepWithContext(ctx context.Context, duration time.Duration) error {
	if duration <= 0 {
		return nil
	}
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (c *httpClient) wait(ctx context.Context, duration time.Duration) error {
	if c.sleep == nil {
		return sleepWithContext(ctx, duration)
	}
	return c.sleep(ctx, duration)
}

func redactIdentifier(value string) string {
	value = strings.TrimSpace(value)
	if len(value) <= 8 {
		return "***"
	}
	return value[:4] + "..." + value[len(value)-4:]
}

func isContextError(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}
