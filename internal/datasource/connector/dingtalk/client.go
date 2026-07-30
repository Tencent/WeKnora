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
)

const (
	defaultTimeout     = 30 * time.Second
	defaultPageSize    = 100
	defaultBlockWindow = 100
	maxBlockPages      = 1000
	maxResponseBytes   = 16 << 20
	userAgent          = "WeKnora-DingTalk-Connector/1.0"
)

type client struct {
	baseURL      string
	clientID     string
	clientSecret string
	operatorID   string
	httpClient   *http.Client
	sleep        func(context.Context, time.Duration) error

	tokenMu        sync.Mutex
	accessToken    string
	tokenExpiresAt time.Time
}

func newClient(cfg *Config) *client {
	return &client{
		baseURL:      cfg.getBaseURL(),
		clientID:     cfg.ClientID,
		clientSecret: cfg.ClientSecret,
		operatorID:   cfg.OperatorUnionID,
		httpClient:   datasource.NewConnectorHTTPClient(defaultTimeout),
		sleep:        sleepContext,
	}
}

func sleepContext(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (c *client) invalidateToken() {
	c.tokenMu.Lock()
	c.accessToken = ""
	c.tokenExpiresAt = time.Time{}
	c.tokenMu.Unlock()
}

func (c *client) token(ctx context.Context) (string, error) {
	c.tokenMu.Lock()
	defer c.tokenMu.Unlock()

	if c.accessToken != "" && time.Now().Before(c.tokenExpiresAt) {
		return c.accessToken, nil
	}
	payload := struct {
		AppKey    string `json:"appKey"`
		AppSecret string `json:"appSecret"`
	}{AppKey: c.clientID, AppSecret: c.clientSecret}

	var result accessTokenResponse
	if err := c.doUnauthenticatedJSON(ctx, http.MethodPost, "/v1.0/oauth2/accessToken", nil, payload, &result); err != nil {
		return "", fmt.Errorf("get dingtalk access token: %w", err)
	}
	if strings.TrimSpace(result.AccessToken) == "" {
		return "", fmt.Errorf("get dingtalk access token: response contained no accessToken")
	}
	ttl := time.Duration(result.ExpireIn) * time.Second
	if ttl <= 0 {
		ttl = 2 * time.Hour
	}
	// Refresh before the server-side expiry. For unusually short test tokens,
	// retain half the advertised lifetime instead of expiring immediately.
	refreshBefore := 5 * time.Minute
	if ttl <= 2*refreshBefore {
		refreshBefore = ttl / 2
	}
	c.accessToken = result.AccessToken
	c.tokenExpiresAt = time.Now().Add(ttl - refreshBefore)
	return c.accessToken, nil
}

func (c *client) doUnauthenticatedJSON(
	ctx context.Context,
	method, path string,
	query url.Values,
	body interface{},
	result interface{},
) error {
	return c.doJSONRequest(ctx, method, path, query, body, "", result)
}

func (c *client) doJSON(
	ctx context.Context,
	method, path string,
	query url.Values,
	body interface{},
	result interface{},
) error {
	for authAttempt := 0; authAttempt < 2; authAttempt++ {
		token, err := c.token(ctx)
		if err != nil {
			return err
		}
		err = c.doJSONRequest(ctx, method, path, query, body, token, result)
		if !errors.Is(err, errTokenExpired) || authAttempt == 1 {
			return err
		}
		c.invalidateToken()
	}
	return nil
}

var errTokenExpired = errors.New("dingtalk access token expired")

func (c *client) doJSONRequest(
	ctx context.Context,
	method, path string,
	query url.Values,
	body interface{},
	accessToken string,
	result interface{},
) error {
	var encodedBody []byte
	var err error
	if body != nil {
		encodedBody, err = json.Marshal(body)
		if err != nil {
			return fmt.Errorf("encode request body: %w", err)
		}
	}

	const maxAttempts = 3
	for attempt := 0; attempt < maxAttempts; attempt++ {
		requestURL := c.baseURL + path
		if len(query) > 0 {
			requestURL += "?" + query.Encode()
		}
		req, err := http.NewRequestWithContext(ctx, method, requestURL, bytes.NewReader(encodedBody))
		if err != nil {
			return fmt.Errorf("create dingtalk request: %w", err)
		}
		req.Header.Set("Accept", "application/json")
		req.Header.Set("Content-Type", "application/json; charset=utf-8")
		req.Header.Set("User-Agent", userAgent)
		if accessToken != "" {
			req.Header.Set("x-acs-dingtalk-access-token", accessToken)
		}

		logger.Infof(ctx, "[DingTalk] %s %s (attempt %d/%d)", method, path, attempt+1, maxAttempts)
		resp, err := c.httpClient.Do(req)
		if err != nil {
			if attempt+1 < maxAttempts {
				if sleepErr := c.sleep(ctx, retryDelay(attempt)); sleepErr != nil {
					return sleepErr
				}
				continue
			}
			return fmt.Errorf("execute dingtalk request: %w", err)
		}
		responseBody, readErr := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
		resp.Body.Close()
		if readErr != nil {
			return fmt.Errorf("read dingtalk response: %w", readErr)
		}
		if len(responseBody) > maxResponseBytes {
			return fmt.Errorf("dingtalk response exceeds %d bytes", maxResponseBytes)
		}

		if resp.StatusCode == http.StatusUnauthorized {
			if accessToken != "" {
				return errTokenExpired
			}
			return fmt.Errorf("%w: dingtalk token endpoint returned status 401", datasource.ErrInvalidCredentials)
		}
		if resp.StatusCode == http.StatusForbidden {
			return fmt.Errorf("%w: dingtalk API returned status 403", datasource.ErrInvalidCredentials)
		}
		if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
			if attempt+1 < maxAttempts {
				delay := retryDelay(attempt)
				if retryAfter := resp.Header.Get("Retry-After"); retryAfter != "" {
					if seconds, parseErr := strconv.Atoi(retryAfter); parseErr == nil && seconds >= 0 {
						delay = time.Duration(seconds) * time.Second
						if delay == 0 {
							delay = 10 * time.Millisecond
						}
					}
				}
				if sleepErr := c.sleep(ctx, delay); sleepErr != nil {
					return sleepErr
				}
				continue
			}
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			var apiErr apiErrorBody
			_ = json.Unmarshal(responseBody, &apiErr)
			if apiErr.Message != "" {
				return fmt.Errorf("dingtalk API error: status=%d code=%s message=%s request_id=%s",
					resp.StatusCode, apiErr.Code, apiErr.Message, apiErr.RequestID)
			}
			return fmt.Errorf("dingtalk API error: status=%d", resp.StatusCode)
		}
		if result == nil || len(responseBody) == 0 {
			return nil
		}
		if err := json.Unmarshal(responseBody, result); err != nil {
			return fmt.Errorf("decode dingtalk response: %w", err)
		}
		return nil
	}
	return fmt.Errorf("dingtalk request exhausted retry budget")
}

func retryDelay(attempt int) time.Duration {
	return time.Duration(1<<attempt) * time.Second
}

func (c *client) Ping(ctx context.Context) error {
	_, err := c.listSpacesPage(ctx, "")
	return err
}

func (c *client) listSpacesPage(ctx context.Context, nextToken string) (relatedSpacesResponse, error) {
	query := url.Values{
		"operatorId": {c.operatorID},
		"maxResults": {strconv.Itoa(defaultPageSize)},
	}
	if nextToken != "" {
		query.Set("nextToken", nextToken)
	}
	var response relatedSpacesResponse
	if err := c.doJSON(ctx, http.MethodGet, "/v2.0/doc/relations/spaces", query, nil, &response); err != nil {
		return relatedSpacesResponse{}, err
	}
	return response, nil
}

func (c *client) ListSpaces(ctx context.Context) ([]dingtalkSpace, error) {
	var spaces []dingtalkSpace
	nextToken := ""
	seenTokens := make(map[string]struct{})
	for {
		page, err := c.listSpacesPage(ctx, nextToken)
		if err != nil {
			return nil, err
		}
		spaces = append(spaces, page.Items...)
		if !page.HasMore || page.NextToken == "" {
			return spaces, nil
		}
		if _, exists := seenTokens[page.NextToken]; exists {
			return nil, fmt.Errorf("dingtalk spaces pagination repeated nextToken")
		}
		seenTokens[page.NextToken] = struct{}{}
		nextToken = page.NextToken
	}
}

func (c *client) listDirectoryPage(
	ctx context.Context,
	spaceID, parentID, nextToken string,
) (directoriesResponse, error) {
	query := url.Values{
		"operatorId": {c.operatorID},
		"maxResults": {strconv.Itoa(defaultPageSize)},
	}
	if parentID != "" {
		query.Set("dentryId", parentID)
	}
	if nextToken != "" {
		query.Set("nextToken", nextToken)
	}
	path := "/v2.0/doc/spaces/" + url.PathEscape(spaceID) + "/directories"
	var response directoriesResponse
	if err := c.doJSON(ctx, http.MethodGet, path, query, nil, &response); err != nil {
		return directoriesResponse{}, err
	}
	return response, nil
}

func (c *client) listDirectory(ctx context.Context, spaceID, parentID string) ([]dentry, error) {
	var entries []dentry
	nextToken := ""
	seenTokens := make(map[string]struct{})
	for {
		page, err := c.listDirectoryPage(ctx, spaceID, parentID, nextToken)
		if err != nil {
			return nil, err
		}
		entries = append(entries, page.Children...)
		if !page.HasMore || page.NextToken == "" {
			return entries, nil
		}
		if _, exists := seenTokens[page.NextToken]; exists {
			return nil, fmt.Errorf("dingtalk directory pagination repeated nextToken")
		}
		seenTokens[page.NextToken] = struct{}{}
		nextToken = page.NextToken
	}
}

// ListSpaceEntries recursively walks a space. Directory IDs are tracked to
// protect a sync from malformed trees containing cycles.
func (c *client) ListSpaceEntries(ctx context.Context, spaceID string) ([]dentry, error) {
	type directory struct{ id string }
	queue := []directory{{}}
	seenDirectories := make(map[string]struct{})
	var all []dentry
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		children, err := c.listDirectory(ctx, spaceID, current.id)
		if err != nil {
			return nil, err
		}
		all = append(all, children...)
		for _, entry := range children {
			if !entry.isFolder() && !entry.HasChildren {
				continue
			}
			id := entry.DentryID
			if id == "" {
				id = entry.DentryUUID
			}
			if id == "" {
				continue
			}
			if _, exists := seenDirectories[id]; exists {
				continue
			}
			seenDirectories[id] = struct{}{}
			queue = append(queue, directory{id: id})
		}
	}
	return all, nil
}

func (c *client) GetDocumentBlocks(ctx context.Context, docKey string) ([]json.RawMessage, error) {
	if strings.TrimSpace(docKey) == "" {
		return nil, fmt.Errorf("dingtalk document has no docKey or dentryUuid")
	}
	var all []json.RawMessage
	for page := 0; page < maxBlockPages; page++ {
		start := page * defaultBlockWindow
		query := url.Values{
			"operatorId": {c.operatorID},
			"startIndex": {strconv.Itoa(start)},
			"endIndex":   {strconv.Itoa(start + defaultBlockWindow - 1)},
		}
		path := "/v1.0/doc/suites/documents/" + url.PathEscape(docKey) + "/blocks"
		var response blocksResponse
		if err := c.doJSON(ctx, http.MethodGet, path, query, nil, &response); err != nil {
			return nil, err
		}
		if !response.Success {
			return nil, fmt.Errorf("dingtalk block query returned success=false")
		}
		all = append(all, response.Result.Data...)
		if len(response.Result.Data) < defaultBlockWindow {
			return all, nil
		}
	}
	return nil, fmt.Errorf("dingtalk document exceeded the %d-block safety limit", maxBlockPages*defaultBlockWindow)
}
