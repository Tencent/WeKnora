// Package dingtalk implements the DingTalk document data source connector.
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
	"time"

	"github.com/Tencent/WeKnora/internal/datasource"
	"github.com/Tencent/WeKnora/internal/types"
)

const (
	apiBaseURL       = "https://api.dingtalk.com"
	apiTimeout       = 30 * time.Second
	maxResponseBytes = 16 << 20
	maxPages         = 1000
	maxAttempts      = 3
)

type config struct {
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret"`
	OperatorID   string `json:"operator_id"`
}

func parseConfig(dataSourceConfig *types.DataSourceConfig) (*config, error) {
	if dataSourceConfig == nil {
		return nil, fmt.Errorf("%w: config is nil", datasource.ErrInvalidConfig)
	}

	raw, err := json.Marshal(dataSourceConfig.Credentials)
	if err != nil {
		return nil, fmt.Errorf("%w: marshal DingTalk credentials: %v",
			datasource.ErrInvalidCredentials, err)
	}
	var cfg config
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return nil, fmt.Errorf("%w: decode DingTalk credentials: %v",
			datasource.ErrInvalidCredentials, err)
	}
	cfg.ClientID = strings.TrimSpace(cfg.ClientID)
	cfg.ClientSecret = strings.TrimSpace(cfg.ClientSecret)
	cfg.OperatorID = strings.TrimSpace(cfg.OperatorID)

	switch {
	case cfg.ClientID == "":
		return nil, fmt.Errorf("%w: client_id is required", datasource.ErrInvalidCredentials)
	case cfg.ClientSecret == "":
		return nil, fmt.Errorf("%w: client_secret is required", datasource.ErrInvalidCredentials)
	case cfg.OperatorID == "":
		return nil, fmt.Errorf("%w: operator_id is required", datasource.ErrInvalidCredentials)
	}
	return &cfg, nil
}

type workspace struct {
	ID           string `json:"workspaceId"`
	RootNodeID   string `json:"rootNodeId"`
	Name         string `json:"name"`
	Description  string `json:"description"`
	URL          string `json:"url"`
	ModifiedTime string `json:"modifiedTime"`
}

type node struct {
	ID                string `json:"nodeId"`
	WorkspaceID       string `json:"workspaceId"`
	Name              string `json:"name"`
	Type              string `json:"type"`
	Category          string `json:"category"`
	Extension         string `json:"extension"`
	URL               string `json:"url"`
	ModifiedTime      string `json:"modifiedTime"`
	ModifiedTimestamp int64  `json:"modifiedTimestamp"`
	HasChildren       bool   `json:"hasChildren"`
}

func (n node) isFolder() bool {
	return strings.EqualFold(n.Type, "FOLDER")
}

func (n node) isDocument() bool {
	return strings.EqualFold(n.Type, "FILE") &&
		strings.EqualFold(n.Category, "ALIDOC") &&
		strings.EqualFold(n.Extension, "adoc")
}

func (n node) title() string {
	if title := strings.TrimSpace(n.Name); title != "" {
		return title
	}
	return n.ID
}

func (n node) revision() string {
	if revision := strings.TrimSpace(n.ModifiedTime); revision != "" {
		return revision
	}
	if n.ModifiedTimestamp > 0 {
		return strconv.FormatInt(n.ModifiedTimestamp, 10)
	}
	return ""
}

func (n node) modifiedAt() time.Time {
	if value := strings.TrimSpace(n.ModifiedTime); value != "" {
		if parsed, err := time.Parse(time.RFC3339Nano, value); err == nil {
			return parsed
		}
	}
	if n.ModifiedTimestamp > 0 {
		return time.UnixMilli(n.ModifiedTimestamp)
	}
	return time.Time{}
}

type dingTalkAPI interface {
	listWorkspaces(context.Context) ([]workspace, error)
	listNodes(context.Context, string) ([]node, error)
	documentBlocks(context.Context, string) ([]json.RawMessage, error)
}

type client struct {
	baseURL   string
	operator  string
	appKey    string
	appSecret string
	http      *http.Client
	sleep     func(context.Context, time.Duration) error

	token       string
	tokenExpiry time.Time
}

func newClient(cfg *config) *client {
	return &client{
		baseURL:   apiBaseURL,
		operator:  cfg.OperatorID,
		appKey:    cfg.ClientID,
		appSecret: cfg.ClientSecret,
		http:      datasource.NewConnectorHTTPClient(apiTimeout),
		sleep:     sleepContext,
	}
}

type accessTokenResponse struct {
	AccessToken string `json:"accessToken"`
	ExpireIn    int64  `json:"expireIn"`
}

func (c *client) accessToken(ctx context.Context) (string, error) {
	if c.token != "" && time.Now().Before(c.tokenExpiry) {
		return c.token, nil
	}

	var response accessTokenResponse
	err := c.doJSON(ctx, http.MethodPost, "/v1.0/oauth2/accessToken", map[string]string{
		"appKey":    c.appKey,
		"appSecret": c.appSecret,
	}, false, &response)
	if err != nil {
		return "", fmt.Errorf("get DingTalk access token: %w", err)
	}
	response.AccessToken = strings.TrimSpace(response.AccessToken)
	if response.AccessToken == "" {
		return "", fmt.Errorf("%w: DingTalk returned an empty access token", datasource.ErrInvalidCredentials)
	}

	ttl := time.Duration(response.ExpireIn) * time.Second
	if ttl <= 0 {
		ttl = 90 * time.Minute
	}
	if ttl > 5*time.Minute {
		ttl -= 5 * time.Minute
	}
	c.token = response.AccessToken
	c.tokenExpiry = time.Now().Add(ttl)
	return c.token, nil
}

func (c *client) doJSON(
	ctx context.Context,
	method, path string,
	requestBody any,
	authenticated bool,
	result any,
) error {
	var payload []byte
	var err error
	if requestBody != nil {
		payload, err = json.Marshal(requestBody)
		if err != nil {
			return fmt.Errorf("encode DingTalk request: %w", err)
		}
	}

	refreshed := false
	retryAttempt := 0
	for {
		var body io.Reader
		if payload != nil {
			body = bytes.NewReader(payload)
		}
		req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, body)
		if err != nil {
			return fmt.Errorf("create DingTalk request: %w", err)
		}
		req.Header.Set("Accept", "application/json")
		req.Header.Set("Content-Type", "application/json")

		var token string
		if authenticated {
			token, err = c.accessToken(ctx)
			if err != nil {
				return err
			}
			req.Header.Set("x-acs-dingtalk-access-token", token)
		}

		resp, err := c.http.Do(req)
		if err != nil {
			requestErr := redactRequestError(err)
			if retryAttempt+1 < maxAttempts && !isContextError(requestErr) {
				delay := retryDelay(retryAttempt)
				retryAttempt++
				if err := c.wait(ctx, delay); err != nil {
					return err
				}
				continue
			}
			return fmt.Errorf("execute DingTalk request: %w", requestErr)
		}

		responseBody, readErr := readBody(resp.Body)
		resp.Body.Close()
		if readErr != nil {
			return fmt.Errorf("read DingTalk response: %w", readErr)
		}

		if authenticated && resp.StatusCode == http.StatusUnauthorized && !refreshed {
			if c.token == token {
				c.token = ""
				c.tokenExpiry = time.Time{}
			}
			refreshed = true
			continue
		}

		if isTransient(resp.StatusCode) {
			apiErr := c.redactAPIError(decodeAPIError(resp.StatusCode, responseBody))
			if retryAttempt+1 < maxAttempts {
				delay := retryDelay(retryAttempt)
				if resp.StatusCode == http.StatusTooManyRequests {
					delay = parseRetryAfter(resp.Header.Get("Retry-After"), delay)
				}
				retryAttempt++
				if err := c.wait(ctx, delay); err != nil {
					return err
				}
				continue
			}
			return apiErr
		}

		if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
			apiErr := c.redactAPIError(decodeAPIError(resp.StatusCode, responseBody))
			if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
				return fmt.Errorf("%w: %w", datasource.ErrInvalidCredentials, apiErr)
			}
			return apiErr
		}
		if result != nil && len(responseBody) > 0 {
			if err := json.Unmarshal(responseBody, result); err != nil {
				return fmt.Errorf("decode DingTalk response: %w", err)
			}
		}
		return nil
	}
}

func (c *client) redactAPIError(err error) error {
	message := err.Error()
	for _, sensitive := range []string{c.appKey, c.appSecret, c.operator, c.token} {
		if sensitive != "" {
			message = strings.ReplaceAll(message, sensitive, "[REDACTED]")
		}
	}
	return errors.New(message)
}

func (c *client) listWorkspaces(ctx context.Context) ([]workspace, error) {
	var all []workspace
	nextToken := ""
	seenTokens := make(map[string]struct{})

	for page := 0; page < maxPages; page++ {
		query := url.Values{
			"maxResults": {"30"},
			"operatorId": {c.operator},
		}
		if nextToken != "" {
			query.Set("nextToken", nextToken)
		}
		var response struct {
			Workspaces []workspace `json:"workspaces"`
			NextToken  string      `json:"nextToken"`
		}
		if err := c.doJSON(
			ctx, http.MethodGet, "/v2.0/wiki/workspaces?"+query.Encode(), nil, true, &response,
		); err != nil {
			return nil, fmt.Errorf("list DingTalk workspaces: %w", err)
		}
		all = append(all, response.Workspaces...)
		nextToken = strings.TrimSpace(response.NextToken)
		if nextToken == "" {
			return all, nil
		}
		if _, exists := seenTokens[nextToken]; exists {
			return nil, errors.New("DingTalk workspace pagination repeated nextToken")
		}
		seenTokens[nextToken] = struct{}{}
	}
	return nil, fmt.Errorf("DingTalk workspace pagination exceeded %d pages", maxPages)
}

func (c *client) listNodes(ctx context.Context, parentNodeID string) ([]node, error) {
	var all []node
	nextToken := ""
	seenTokens := make(map[string]struct{})

	for page := 0; page < maxPages; page++ {
		query := url.Values{
			"maxResults":   {"50"},
			"operatorId":   {c.operator},
			"parentNodeId": {parentNodeID},
		}
		if nextToken != "" {
			query.Set("nextToken", nextToken)
		}
		var response struct {
			Nodes     []node `json:"nodes"`
			NextToken string `json:"nextToken"`
		}
		if err := c.doJSON(
			ctx, http.MethodGet, "/v2.0/wiki/nodes?"+query.Encode(), nil, true, &response,
		); err != nil {
			return nil, fmt.Errorf("list DingTalk nodes: %w", err)
		}
		all = append(all, response.Nodes...)
		nextToken = strings.TrimSpace(response.NextToken)
		if nextToken == "" {
			return all, nil
		}
		if _, exists := seenTokens[nextToken]; exists {
			return nil, errors.New("DingTalk node pagination repeated nextToken")
		}
		seenTokens[nextToken] = struct{}{}
	}
	return nil, fmt.Errorf("DingTalk node pagination exceeded %d pages", maxPages)
}

func (c *client) documentBlocks(ctx context.Context, documentID string) ([]json.RawMessage, error) {
	const pageSize = 100
	var all []json.RawMessage

	for page := 0; page < maxPages; page++ {
		start := page * pageSize
		query := url.Values{
			"endIndex":   {strconv.Itoa(start + pageSize - 1)},
			"operatorId": {c.operator},
			"startIndex": {strconv.Itoa(start)},
		}
		var response struct {
			Success *bool `json:"success"`
			Result  struct {
				Data []json.RawMessage `json:"data"`
			} `json:"result"`
		}
		path := "/v1.0/doc/suites/documents/" + url.PathEscape(documentID) +
			"/blocks?" + query.Encode()
		if err := c.doJSON(ctx, http.MethodGet, path, nil, true, &response); err != nil {
			return nil, fmt.Errorf("query DingTalk document blocks: %w", err)
		}
		if response.Success != nil && !*response.Success {
			return nil, errors.New("DingTalk document blocks request was unsuccessful")
		}
		all = append(all, response.Result.Data...)
		if len(response.Result.Data) < pageSize {
			return all, nil
		}
	}
	return nil, fmt.Errorf("DingTalk document block pagination exceeded %d pages", maxPages)
}

type apiError struct {
	status  int
	code    string
	message string
}

func (e *apiError) Error() string {
	switch {
	case e.code != "" && e.message != "":
		return fmt.Sprintf("DingTalk API status=%d code=%s message=%s", e.status, e.code, e.message)
	case e.code != "":
		return fmt.Sprintf("DingTalk API status=%d code=%s", e.status, e.code)
	default:
		return fmt.Sprintf("DingTalk API status=%d", e.status)
	}
}

func decodeAPIError(status int, body []byte) error {
	var response struct {
		Code    json.RawMessage `json:"code"`
		ErrCode json.RawMessage `json:"errcode"`
		Message string          `json:"message"`
		ErrMsg  string          `json:"errmsg"`
	}
	_ = json.Unmarshal(body, &response)
	code := rawValue(response.Code)
	if code == "" {
		code = rawValue(response.ErrCode)
	}
	message := strings.TrimSpace(response.Message)
	if message == "" {
		message = strings.TrimSpace(response.ErrMsg)
	}
	return &apiError{status: status, code: code, message: message}
}

func rawValue(value json.RawMessage) string {
	if len(value) == 0 || string(value) == "null" {
		return ""
	}
	var text string
	if err := json.Unmarshal(value, &text); err == nil {
		return strings.TrimSpace(text)
	}
	return strings.TrimSpace(string(value))
}

func readBody(body io.Reader) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(body, maxResponseBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxResponseBytes {
		return nil, fmt.Errorf("response exceeds %d bytes", maxResponseBytes)
	}
	return data, nil
}

func isTransient(status int) bool {
	return status == http.StatusTooManyRequests || status >= http.StatusInternalServerError
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

func parseRetryAfter(value string, fallback time.Duration) time.Duration {
	const maximum = 30 * time.Second
	delay := fallback
	if seconds, err := strconv.Atoi(strings.TrimSpace(value)); err == nil && value != "" {
		if seconds <= 0 {
			delay = 100 * time.Millisecond
		} else {
			delay = time.Duration(seconds) * time.Second
		}
	} else if parsed, err := http.ParseTime(value); err == nil {
		delay = time.Until(parsed)
		if delay <= 0 {
			delay = 100 * time.Millisecond
		}
	}
	if delay > maximum {
		return maximum
	}
	return delay
}

func (c *client) wait(ctx context.Context, delay time.Duration) error {
	if c.sleep == nil {
		return sleepContext(ctx, delay)
	}
	return c.sleep(ctx, delay)
}

func sleepContext(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func isContextError(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}

func redactRequestError(err error) error {
	for {
		var requestErr *url.Error
		if !errors.As(err, &requestErr) || requestErr.Err == nil {
			return err
		}
		// url.Error includes the full request URL. DingTalk puts operatorId in
		// the query string, so retain the cause without persisting that ID.
		err = requestErr.Err
	}
}
