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

	// downloadTimeout is deliberately longer than apiTimeout: the JSON calls
	// are small, while an uploaded document can be several megabytes.
	downloadTimeout = 5 * time.Minute

	// maxDocumentBytes bounds a single uploaded file transfer. DingTalk lists a
	// "size" per node that does not match the stored object, so the cap is
	// enforced on the transfer itself instead of on listing metadata.
	maxDocumentBytes = 64 << 20
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

// binaryDocumentTypes maps the extension of an uploaded DingTalk file to the
// MIME type WeKnora stores it under. Only formats WeKnora can parse are listed;
// native ALIDOC files have their own online read APIs and are absent on
// purpose, as are media files that would be pointless to index as text.
var binaryDocumentTypes = map[string]string{
	"docx": "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
	"pptx": "application/vnd.openxmlformats-officedocument.presentationml.presentation",
	"xlsx": "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
	"pdf":  "application/pdf",
}

func (n node) isFolder() bool {
	return strings.EqualFold(n.Type, "FOLDER")
}

// isOnlineDocument reports whether the node is a native DingTalk document whose
// blocks can be read through the doc suites API.
func (n node) isOnlineDocument() bool {
	return strings.EqualFold(n.Type, "FILE") &&
		strings.EqualFold(n.Category, "ALIDOC") &&
		strings.EqualFold(n.Extension, "adoc")
}

// binaryContentType returns the MIME type of an uploaded file node. Uploaded
// files are listed as DOCUMENT; ALIDOC covers DingTalk's own document family,
// which is never downloaded as a file.
func (n node) binaryContentType() (string, bool) {
	if !strings.EqualFold(n.Type, "FILE") || !strings.EqualFold(n.Category, "DOCUMENT") {
		return "", false
	}
	contentType, ok := binaryDocumentTypes[strings.ToLower(strings.TrimSpace(n.Extension))]
	return contentType, ok
}

// isBinaryDocument reports whether the node is an uploaded file this connector
// can download and hand to WeKnora's own parsers.
func (n node) isBinaryDocument() bool {
	_, ok := n.binaryContentType()
	return ok
}

// isDocument reports whether the connector can ingest the node: a native adoc
// document through the blocks API, or an uploaded file through the storage
// download API.
func (n node) isDocument() bool {
	return n.isOnlineDocument() || n.isBinaryDocument()
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
	documentBlocks(context.Context, string) ([]json.RawMessage, error)
	// verifyDocumentDownload proves an uploaded file is downloadable without
	// transferring its body, so validation stays cheap for multi-megabyte files.
	verifyDocumentDownload(context.Context, string) error
	downloadDocument(context.Context, string) ([]byte, error)
}

type client struct {
	baseURL   string
	operator  string
	appKey    string
	appSecret string
	http      *http.Client
	sleep     func(context.Context, time.Duration) error

	// fileHTTP transfers document bytes. It is separate from http so a large
	// download is not bound by the short control-plane timeout.
	fileHTTP *http.Client
	// downloadLimit overrides maxDocumentBytes; zero means the default.
	downloadLimit int64

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
		fileHTTP:  datasource.NewConnectorHTTPClient(downloadTimeout),
		sleep:     sleepContext,
	}
}

// transferHTTP returns the client used for byte transfers.
func (c *client) transferHTTP() *http.Client {
	if c.fileHTTP != nil {
		return c.fileHTTP
	}
	return c.http
}

// maxDownloadBytes returns the effective per-document size cap.
func (c *client) maxDownloadBytes() int64 {
	if c.downloadLimit > 0 {
		return c.downloadLimit
	}
	return maxDocumentBytes
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

// dentryRef identifies one file inside 钉盘. Wiki node IDs are UUIDs, but the
// storage APIs accept only the numeric spaceId/dentryId pair, so every upload
// is translated first. Response shape:
//
//	{"dentryId":"123456789012","spaceId":"9876543210","dentryUuid":"Qnp9...l"}
type dentryRef struct {
	DentryID   string `json:"dentryId"`
	SpaceID    string `json:"spaceId"`
	DentryUUID string `json:"dentryUuid"`
}

// resolveDentry maps a wiki node UUID to its 钉盘 storage identifiers.
func (c *client) resolveDentry(ctx context.Context, documentID string) (dentryRef, error) {
	query := url.Values{"operatorId": {c.operator}}
	path := "/v2.0/doc/dentries/" + url.PathEscape(documentID) +
		"/queryDentryId?" + query.Encode()

	var ref dentryRef
	if err := c.doJSON(ctx, http.MethodGet, path, nil, true, &ref); err != nil {
		return dentryRef{}, fmt.Errorf("resolve DingTalk document location: %w", err)
	}
	ref.DentryID = strings.TrimSpace(ref.DentryID)
	ref.SpaceID = strings.TrimSpace(ref.SpaceID)
	if ref.DentryID == "" || ref.SpaceID == "" {
		return dentryRef{}, errors.New("DingTalk returned no storage location for the document")
	}
	return ref, nil
}

// downloadTarget is a short-lived pre-signed URL for one document version.
type downloadTarget struct {
	URL     string
	Headers map[string]string
}

type downloadInfoResponse struct {
	Protocol            string `json:"protocol"`
	HeaderSignatureInfo *struct {
		Headers           map[string]string `json:"headers"`
		ResourceURLs      []string          `json:"resourceUrls"`
		ExpirationSeconds int64             `json:"expirationSeconds"`
	} `json:"headerSignatureInfo"`
}

// requestDownloadURL asks the storage API for a pre-signed download URL.
// preferIntranet stays false so the URL is reachable from outside DingTalk's
// own network.
func (c *client) requestDownloadURL(ctx context.Context, ref dentryRef) (downloadTarget, error) {
	query := url.Values{"unionId": {c.operator}}
	path := "/v1.0/storage/spaces/" + url.PathEscape(ref.SpaceID) +
		"/dentries/" + url.PathEscape(ref.DentryID) + "/downloadInfos/query?" + query.Encode()
	body := map[string]interface{}{
		"option": map[string]interface{}{"version": 1, "preferIntranet": false},
	}

	var response downloadInfoResponse
	if err := c.doJSON(ctx, http.MethodPost, path, body, true, &response); err != nil {
		return downloadTarget{}, fmt.Errorf("query DingTalk download URL: %w", err)
	}
	info := response.HeaderSignatureInfo
	if info == nil || len(info.ResourceURLs) == 0 {
		return downloadTarget{}, fmt.Errorf(
			"DingTalk returned no download URL for the document (protocol %q)",
			strings.TrimSpace(response.Protocol))
	}
	target := downloadTarget{
		URL:     strings.TrimSpace(info.ResourceURLs[0]),
		Headers: info.Headers,
	}
	if target.URL == "" {
		return downloadTarget{}, errors.New("DingTalk returned an empty download URL")
	}
	return target, nil
}

// verifyDocumentDownload proves an uploaded file is reachable end to end
// without transferring its body: the node must resolve to a storage location
// and yield a pre-signed URL. Validate uses this so credentials can be checked
// without downloading several megabytes per attempt.
func (c *client) verifyDocumentDownload(ctx context.Context, documentID string) error {
	ref, err := c.resolveDentry(ctx, documentID)
	if err != nil {
		return err
	}
	_, err = c.requestDownloadURL(ctx, ref)
	return err
}

// downloadDocument fetches the raw bytes of an uploaded document. The pre-signed
// URL is versioned and short lived, so it is re-requested whenever a transfer
// attempt fails in a way a fresh signature could fix.
func (c *client) downloadDocument(ctx context.Context, documentID string) ([]byte, error) {
	ref, err := c.resolveDentry(ctx, documentID)
	if err != nil {
		return nil, err
	}

	var lastErr error
	for attempt := 0; attempt < maxAttempts; attempt++ {
		if attempt > 0 {
			if err := c.wait(ctx, retryDelay(attempt-1)); err != nil {
				return nil, err
			}
		}
		target, err := c.requestDownloadURL(ctx, ref)
		if err != nil {
			return nil, err
		}
		data, retryable, err := c.fetchSigned(ctx, target)
		if err == nil {
			return data, nil
		}
		lastErr = err
		if !retryable || isContextError(err) {
			return nil, err
		}
	}
	return nil, lastErr
}

// fetchSigned transfers a document body from a pre-signed URL. It reports
// whether the failure is worth retrying with a freshly signed URL.
func (c *client) fetchSigned(ctx context.Context, target downloadTarget) ([]byte, bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target.URL, nil)
	if err != nil {
		return nil, false, fmt.Errorf("create DingTalk download request: %w", err)
	}
	for name, value := range target.Headers {
		req.Header.Set(name, value)
	}
	// The signature covers the object as stored. Asking for an identity
	// encoding keeps a proxy from re-encoding the bytes into an unparseable
	// document.
	req.Header.Set("Accept-Encoding", "identity")

	resp, err := c.transferHTTP().Do(req)
	if err != nil {
		return nil, true, fmt.Errorf("download DingTalk document: %w", redactRequestError(err))
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		// Object-store failures are small XML bodies. Only a bounded prefix is
		// read, and decodeAPIError never echoes the body verbatim.
		detail, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		retryable := isTransient(resp.StatusCode) ||
			resp.StatusCode == http.StatusUnauthorized ||
			resp.StatusCode == http.StatusForbidden ||
			resp.StatusCode == http.StatusNotFound
		return nil, retryable, fmt.Errorf(
			"download DingTalk document: %w", decodeAPIError(resp.StatusCode, detail))
	}

	limit := c.maxDownloadBytes()
	if resp.ContentLength > limit {
		return nil, false, fmt.Errorf(
			"DingTalk document is %d bytes, which exceeds the %d byte download limit",
			resp.ContentLength, limit)
	}
	data, err := readLimited(resp.Body, limit)
	if err != nil {
		return nil, true, fmt.Errorf("read DingTalk document: %w", err)
	}
	return data, false, nil
}

// readLimited reads at most limit bytes and fails when the body is longer, so
// an over-sized document is reported instead of silently truncated.
func readLimited(body io.Reader, limit int64) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("document exceeds the %d byte download limit", limit)
	}
	return data, nil
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
