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

	// notableRecordPageSize is how many notable records one records/list call
	// asks for. The value travels in the request body: this endpoint is
	// POST-only and ignores maxResults in the query string.
	notableRecordPageSize = 100
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

// notableTable is one table of a multi-dimensional table (able). The notable
// API calls these objects "sheets", but they are tables to their users — records
// live in them and fields describe their columns — so the connector uses the
// user-facing word everywhere.
type notableTable struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// notableField is one column definition. Type drives how a cell is rendered:
// the API returns dates as epoch milliseconds, selects as {name,id} objects and
// users as arrays of objects, none of which are the string a reader expects.
type notableField struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Type string `json:"type"`
}

// notableRecord is one row. Fields is keyed by field name, so a member that is
// absent from the map is an empty cell rather than a missing column.
type notableRecord struct {
	ID     string         `json:"id"`
	Fields map[string]any `json:"fields"`
}

// notableRecordPage is one page of records/list.
type notableRecordPage struct {
	Records   []notableRecord `json:"records"`
	NextToken string          `json:"nextToken"`
	HasMore   bool            `json:"hasMore"`
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
	// Prefer the millisecond timestamp when present. Official node listings
	// also return modifiedTime at minute precision (e.g. 2023-05-15T11:29Z),
	// which would skip same-minute edits during incremental sync.
	if n.ModifiedTimestamp > 0 {
		return strconv.FormatInt(n.ModifiedTimestamp, 10)
	}
	return strings.TrimSpace(n.ModifiedTime)
}

func (n node) modifiedAt() time.Time {
	if n.ModifiedTimestamp > 0 {
		return time.UnixMilli(n.ModifiedTimestamp)
	}
	return parseDingTalkTime(n.ModifiedTime)
}

type dingTalkAPI interface {
	listWorkspaces(context.Context) ([]workspace, error)
	listNodes(context.Context, string) ([]node, error)
	listNodesPage(ctx context.Context, parentNodeID, pageToken string) ([]node, string, error)
	// getNode reads one wiki node by its own id. A node in a personal space is
	// reachable this way but never appears in listWorkspaces, so listing alone
	// cannot resolve it.
	getNode(context.Context, string) (node, error)
	documentBlocks(context.Context, string) ([]json.RawMessage, error)
	// listNotableTables, listNotableFields and listNotableRecords read a
	// multi-dimensional table (able) through the notable API. A Base is not a
	// wiki node and has no listable index, so every call is addressed by the
	// Base ID the user supplied.
	listNotableTables(context.Context, string) ([]notableTable, error)
	listNotableFields(context.Context, string, string) ([]notableField, error)
	listNotableRecords(context.Context, string, string, string) (notableRecordPage, error)
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
		_ = resp.Body.Close()
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
		nodes, next, err := c.listNodesPage(ctx, parentNodeID, nextToken)
		if err != nil {
			return nil, err
		}
		all = append(all, nodes...)
		nextToken = next
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

// listNodesPage requests a single page of children, so one call is one HTTP
// request plus the client's own retries. An empty next token marks the last
// page.
func (c *client) listNodesPage(ctx context.Context, parentNodeID, pageToken string) ([]node, string, error) {
	query := url.Values{
		"maxResults":   {"50"},
		"operatorId":   {c.operator},
		"parentNodeId": {parentNodeID},
	}
	if pageToken != "" {
		query.Set("nextToken", pageToken)
	}
	var response struct {
		Nodes     []node `json:"nodes"`
		NextToken string `json:"nextToken"`
	}
	if err := c.doJSON(
		ctx, http.MethodGet, "/v2.0/wiki/nodes?"+query.Encode(), nil, true, &response,
	); err != nil {
		return nil, "", fmt.Errorf("list DingTalk nodes: %w", err)
	}
	return response.Nodes, strings.TrimSpace(response.NextToken), nil
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
			Result  *struct {
				Data []json.RawMessage `json:"data"`
			} `json:"result"`
		}
		path := "/v1.0/doc/suites/documents/" + url.PathEscape(documentID) +
			"/blocks?" + query.Encode()
		if err := c.doJSON(ctx, http.MethodGet, path, nil, true, &response); err != nil {
			return nil, fmt.Errorf("query DingTalk document blocks: %w", err)
		}
		if response.Success == nil || !*response.Success || response.Result == nil {
			return nil, errors.New("DingTalk document blocks request was unsuccessful")
		}
		all = append(all, response.Result.Data...)
		if len(response.Result.Data) < pageSize {
			return all, nil
		}
	}
	return nil, fmt.Errorf("DingTalk document block pagination exceeded %d pages", maxPages)
}

// getNode reads one wiki node by its own id. Listing a workspace's children
// requires the workspace root, which only exists for workspaces returned by
// listWorkspaces; this endpoint answers for a node in a workspace the listing
// never returns, such as the operator's personal space.
//
// Verified live: GET /v2.0/wiki/nodes/{nodeId} answers 200 with
// {"node":{...same shape as a listed node...}} and 404 nodeNotExist for an
// unknown id, so the API itself decides whether an explicitly referenced node
// is readable.
func (c *client) getNode(ctx context.Context, nodeID string) (node, error) {
	query := url.Values{"operatorId": {c.operator}}
	path := "/v2.0/wiki/nodes/" + url.PathEscape(nodeID) + "?" + query.Encode()

	var response struct {
		Node node `json:"node"`
	}
	if err := c.doJSON(ctx, http.MethodGet, path, nil, true, &response); err != nil {
		return node{}, fmt.Errorf("read DingTalk node: %w", err)
	}
	if strings.TrimSpace(response.Node.ID) == "" {
		return node{}, errors.New("DingTalk returned no node for the requested id")
	}
	return response.Node, nil
}

// listNotableTables names the tables of one multi-dimensional table (Base).
// This is also the cheapest proof that a Base is readable: there is no endpoint
// that returns a Base itself, and no endpoint lists Bases at all.
func (c *client) listNotableTables(ctx context.Context, baseID string) ([]notableTable, error) {
	query := url.Values{"operatorId": {c.operator}}
	path := "/v1.0/notable/bases/" + url.PathEscape(baseID) + "/sheets?" + query.Encode()

	var response struct {
		Value []notableTable `json:"value"`
	}
	if err := c.doJSON(ctx, http.MethodGet, path, nil, true, &response); err != nil {
		return nil, fmt.Errorf("list DingTalk notable tables: %w", err)
	}
	return response.Value, nil
}

// listNotableFields reads one table's column definitions, including each
// column's type. The response order is the table's column order and is the only
// order a rendered row may follow.
func (c *client) listNotableFields(
	ctx context.Context,
	baseID, tableID string,
) ([]notableField, error) {
	query := url.Values{"operatorId": {c.operator}}
	path := "/v1.0/notable/bases/" + url.PathEscape(baseID) +
		"/sheets/" + url.PathEscape(tableID) + "/fields?" + query.Encode()

	var response struct {
		Value []notableField `json:"value"`
	}
	if err := c.doJSON(ctx, http.MethodGet, path, nil, true, &response); err != nil {
		return nil, fmt.Errorf("list DingTalk notable fields: %w", err)
	}
	return response.Value, nil
}

// listNotableRecords reads one page of a table's records. The endpoint is
// POST-only — a GET answers 404 — and maxResults belongs in the body, so both
// facts are encoded here rather than at the call site.
//
// operatorId has to be the operator's unionId; a userid is rejected with
// 400 invalidRequest.inputArgs.invalid.
func (c *client) listNotableRecords(
	ctx context.Context,
	baseID, tableID, nextToken string,
) (notableRecordPage, error) {
	query := url.Values{"operatorId": {c.operator}}
	path := "/v1.0/notable/bases/" + url.PathEscape(baseID) +
		"/sheets/" + url.PathEscape(tableID) + "/records/list?" + query.Encode()

	body := map[string]any{"maxResults": notableRecordPageSize}
	if nextToken != "" {
		body["nextToken"] = nextToken
	}

	var page notableRecordPage
	if err := c.doJSON(ctx, http.MethodPost, path, body, true, &page); err != nil {
		// A wrong base id has produced a 403 whose message blames a missing
		// Notable.Base.Read permission. That message is misleading: the app is
		// not necessarily under-permissioned, so it is passed through verbatim
		// and never translated into connector-level permission guidance.
		return notableRecordPage{}, fmt.Errorf("list DingTalk notable records: %w", err)
	}
	return page, nil
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
