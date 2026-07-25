package feishu

import (
	"bytes"
	"context"
	"encoding/json"
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

// Client wraps the Feishu Open Platform API for document/wiki operations.
type Client struct {
	baseURL   string
	appID     string
	appSecret string

	httpClient *http.Client

	// Token cache (thread-safe)
	tokenMu    sync.Mutex
	tokenCache string
	tokenExpAt time.Time
}

type wikiNodeListFailure struct {
	Node wikiNode
	Err  error
}

type partialWikiNodeListError struct {
	Failures []wikiNodeListFailure
}

func (e *partialWikiNodeListError) Error() string {
	if e == nil || len(e.Failures) == 0 {
		return "partial wiki node listing failed"
	}
	parts := make([]string, 0, len(e.Failures))
	for _, failure := range e.Failures {
		parts = append(parts, failure.Err.Error())
	}
	return strings.Join(parts, "; ")
}

// NewClient creates a new Feishu API client.
func NewClient(config *Config) *Client {
	return &Client{
		baseURL:    config.GetBaseURL(),
		appID:      config.AppID,
		appSecret:  config.AppSecret,
		httpClient: datasource.NewConnectorHTTPClient(30 * time.Second),
	}
}

// getTenantAccessToken retrieves (or returns cached) tenant access token.
// Feishu tokens expire in 2 hours; we cache with a 5-minute safety margin.
func (c *Client) getTenantAccessToken(ctx context.Context) (string, error) {
	c.tokenMu.Lock()
	defer c.tokenMu.Unlock()

	if c.tokenCache != "" && time.Now().Before(c.tokenExpAt) {
		return c.tokenCache, nil
	}

	payload, _ := json.Marshal(map[string]string{
		"app_id":     c.appID,
		"app_secret": c.appSecret,
	})

	url := c.baseURL + "/open-apis/auth/v3/tenant_access_token/internal"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return "", fmt.Errorf("create token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("request token: %w", err)
	}
	defer resp.Body.Close()

	var result tokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decode token response: %w", err)
	}
	if result.Code != 0 {
		return "", fmt.Errorf("feishu auth error: code=%d msg=%s", result.Code, result.Msg)
	}

	c.tokenCache = result.TenantAccessToken
	ttl := time.Duration(result.Expire) * time.Second
	if ttl > 5*time.Minute {
		ttl -= 5 * time.Minute
	}
	c.tokenExpAt = time.Now().Add(ttl)

	prefixLen := 8
	if len(result.TenantAccessToken) < prefixLen {
		prefixLen = len(result.TenantAccessToken)
	}
	suffixLen := 4
	if len(result.TenantAccessToken) < suffixLen {
		suffixLen = len(result.TenantAccessToken)
	}
	logger.Infof(ctx, "[Feishu] got tenant_access_token: %s...%s expire=%ds",
		result.TenantAccessToken[:prefixLen], result.TenantAccessToken[len(result.TenantAccessToken)-suffixLen:], result.Expire)

	return c.tokenCache, nil
}

// Retry policy shared by doRequest (JSON API calls) and downloadRawBytes (file
// downloads): 429 honours Retry-After, 5xx retries once, transport errors back off.
const (
	feishuMaxRetries    = 3
	feishuMax5xxRetries = 1
	feishuRetry5xxDelay = 2 * time.Second
)

var feishuRetryBackoff = []time.Duration{2 * time.Second, 4 * time.Second, 8 * time.Second}

// doRequest executes an authenticated API request and decodes the JSON response,
// retrying transient failures (transport errors, HTTP 429, 5xx). Feishu's drive
// export/wiki APIs are aggressively rate limited, and a thousand-document sync
// issues tens of thousands of calls; without backoff a single 429 burst used to
// fail whole swathes of documents silently. 429 responses honour Retry-After;
// 5xx is retried once; other non-2xx statuses fail fast (no point retrying 4xx).
func (c *Client) doRequest(ctx context.Context, method, path string, body interface{}, result interface{}) error {
	const (
		maxRetries    = feishuMaxRetries
		max5xxRetries = feishuMax5xxRetries
		retry5xxDelay = feishuRetry5xxDelay
	)
	backoff := feishuRetryBackoff

	token, err := c.getTenantAccessToken(ctx)
	if err != nil {
		return err
	}

	var bodyBytes []byte
	if body != nil {
		bodyBytes, err = json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshal request body: %w", err)
		}
	}

	url := c.baseURL + path
	var lastErr error

	for attempt := 0; attempt <= maxRetries; attempt++ {
		var bodyReader io.Reader
		if bodyBytes != nil {
			bodyReader = bytes.NewReader(bodyBytes)
		}
		req, err := http.NewRequestWithContext(ctx, method, url, bodyReader)
		if err != nil {
			return fmt.Errorf("create request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json; charset=utf-8")
		req.Header.Set("Authorization", "Bearer "+token)

		if attempt == 0 {
			logger.Infof(ctx, "[Feishu] %s %s", method, path)
		} else {
			logger.Infof(ctx, "[Feishu] %s %s (retry %d/%d)", method, path, attempt, maxRetries)
		}

		resp, err := c.httpClient.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("execute request: %w", err)
			if attempt < maxRetries {
				if sErr := sleepCtx(ctx, backoff[attempt]); sErr != nil {
					return sErr
				}
				continue
			}
			return lastErr
		}

		respBody, readErr := io.ReadAll(resp.Body)
		resp.Body.Close()
		if readErr != nil {
			lastErr = fmt.Errorf("read response body: %w", readErr)
			if attempt < maxRetries {
				if sErr := sleepCtx(ctx, backoff[attempt]); sErr != nil {
					return sErr
				}
				continue
			}
			return lastErr
		}

		logger.Infof(ctx, "[Feishu] %s %s → status=%d bodyLen=%d body=%s",
			method, path, resp.StatusCode, len(respBody), truncate(string(respBody), 1000))

		if resp.StatusCode == http.StatusTooManyRequests {
			wait := parseRetryAfter(resp.Header.Get("Retry-After"), backoff[min(attempt, len(backoff)-1)])
			lastErr = fmt.Errorf("feishu rate limited: status=429 body=%s", truncate(string(respBody), 500))
			if attempt < maxRetries {
				if sErr := sleepCtx(ctx, wait); sErr != nil {
					return sErr
				}
				continue
			}
			return lastErr
		}

		if resp.StatusCode >= 500 && resp.StatusCode < 600 {
			lastErr = fmt.Errorf("feishu server error: status=%d body=%s", resp.StatusCode, truncate(string(respBody), 500))
			if attempt < max5xxRetries {
				if sErr := sleepCtx(ctx, retry5xxDelay); sErr != nil {
					return sErr
				}
				continue
			}
			return lastErr
		}

		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("feishu api error: status=%d body=%s", resp.StatusCode, string(respBody))
		}

		if result != nil {
			if err := json.Unmarshal(respBody, result); err != nil {
				return fmt.Errorf("decode response: %w", err)
			}
		}
		return nil
	}

	return lastErr
}

// parseRetryAfter interprets a Retry-After header value (seconds) into a wait
// duration, coercing 0/negative to a short delay and falling back when absent
// or unparseable.
func parseRetryAfter(header string, fallback time.Duration) time.Duration {
	if header == "" {
		return fallback
	}
	secs, err := strconv.ParseFloat(strings.TrimSpace(header), 64)
	if err != nil {
		return fallback
	}
	if secs <= 0 {
		return 100 * time.Millisecond
	}
	return time.Duration(secs * float64(time.Second))
}

// sleepCtx waits for d or until ctx is cancelled, returning ctx.Err() if the
// context ends first so retries abort promptly on task cancellation/timeout.
func sleepCtx(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// truncate truncates a string to maxLen and appends "..." if truncated.
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// ListWikiSpaces returns all wiki spaces accessible to the app.
func (c *Client) ListWikiSpaces(ctx context.Context) ([]wikiSpace, error) {
	var allSpaces []wikiSpace
	pageToken := ""

	for {
		path := "/open-apis/wiki/v2/spaces?page_size=50"
		if pageToken != "" {
			path += "&page_token=" + pageToken
		}

		var resp wikiSpaceListResponse
		if err := c.doRequest(ctx, http.MethodGet, path, nil, &resp); err != nil {
			return nil, fmt.Errorf("list wiki spaces: %w", err)
		}
		if resp.Code != 0 {
			logger.Errorf(ctx, "[Feishu] ListWikiSpaces error: code=%d msg=%s", resp.Code, resp.Msg)
			return nil, fmt.Errorf("list wiki spaces error: code=%d msg=%s", resp.Code, resp.Msg)
		}

		logger.Infof(ctx, "[Feishu] ListWikiSpaces: got %d spaces, has_more=%v", len(resp.Data.Items), resp.Data.HasMore)
		for i, s := range resp.Data.Items {
			logger.Infof(ctx, "[Feishu]   space[%d]: id=%s name=%q visibility=%s", i, s.SpaceID, s.Name, s.Visibility)
		}

		allSpaces = append(allSpaces, resp.Data.Items...)

		if !resp.Data.HasMore || resp.Data.PageToken == "" {
			break
		}
		pageToken = resp.Data.PageToken
	}

	logger.Infof(ctx, "[Feishu] ListWikiSpaces: total %d spaces", len(allSpaces))
	return allSpaces, nil
}

// ListWikiNodes returns all nodes (documents) under a wiki space.
// If parentNodeToken is empty, returns top-level nodes.
func (c *Client) ListWikiNodes(ctx context.Context, spaceID string, parentNodeToken string) ([]wikiNode, error) {
	var allNodes []wikiNode
	pageToken := ""

	for {
		path := fmt.Sprintf("/open-apis/wiki/v2/spaces/%s/nodes?page_size=50", spaceID)
		if parentNodeToken != "" {
			path += "&parent_node_token=" + parentNodeToken
		}
		if pageToken != "" {
			path += "&page_token=" + pageToken
		}

		var resp wikiNodeListResponse
		if err := c.doRequest(ctx, http.MethodGet, path, nil, &resp); err != nil {
			return nil, fmt.Errorf("list wiki nodes: %w", err)
		}
		if resp.Code != 0 {
			return nil, fmt.Errorf("list wiki nodes error: code=%d msg=%s", resp.Code, resp.Msg)
		}

		for _, node := range resp.Data.Items {
			if parentNodeToken != "" && node.ParentNodeID == "" {
				node.ParentNodeID = parentNodeToken
			}
			if node.SpaceID == "" {
				node.SpaceID = spaceID
			}
			allNodes = append(allNodes, node)
		}

		if !resp.Data.HasMore || resp.Data.PageToken == "" {
			break
		}
		pageToken = resp.Data.PageToken
	}

	return allNodes, nil
}

// GetWikiNode returns metadata for a single wiki node.
func (c *Client) GetWikiNode(ctx context.Context, spaceID string, nodeToken string) (wikiNode, error) {
	path := fmt.Sprintf("/open-apis/wiki/v2/spaces/get_node?token=%s", url.QueryEscape(nodeToken))

	var resp wikiNodeInfoResponse
	if err := c.doRequest(ctx, http.MethodGet, path, nil, &resp); err != nil {
		return wikiNode{}, fmt.Errorf("get wiki node: %w", err)
	}
	if resp.Code != 0 {
		return wikiNode{}, fmt.Errorf("get wiki node error: code=%d msg=%s", resp.Code, resp.Msg)
	}

	node := resp.Data.Node
	if node.SpaceID == "" {
		node.SpaceID = spaceID
	}
	return node, nil
}

// ListAllWikiNodesRecursive recursively lists all nodes under a wiki space.
// It walks the tree depth-first to discover all nested documents.
func (c *Client) ListAllWikiNodesRecursive(ctx context.Context, spaceID string) ([]wikiNode, error) {
	// Start with top-level nodes
	topNodes, err := c.ListWikiNodes(ctx, spaceID, "")
	if err != nil {
		return nil, err
	}

	var allNodes []wikiNode
	var failures []wikiNodeListFailure
	var walk func(nodes []wikiNode)

	walk = func(nodes []wikiNode) {
		for _, node := range nodes {
			allNodes = append(allNodes, node)

			// Recurse into child nodes if this node has children
			if node.HasChild {
				children, err := c.ListWikiNodes(ctx, spaceID, node.NodeToken)
				if err != nil {
					wrappedErr := fmt.Errorf("list children of %s: %w", node.NodeToken, err)
					failures = append(failures, wikiNodeListFailure{
						Node: node,
						Err:  wrappedErr,
					})
					logger.Warnf(ctx, "[Feishu] partial wiki node listing failure: space=%s node=%s err=%v",
						spaceID, node.NodeToken, err)
					continue
				}
				walk(children)
			}
		}
	}

	walk(topNodes)
	if len(failures) > 0 {
		return allNodes, &partialWikiNodeListError{Failures: failures}
	}

	return allNodes, nil
}

// ListWikiNodesRecursiveFrom returns a wiki node and all descendants below it.
func (c *Client) ListWikiNodesRecursiveFrom(ctx context.Context, spaceID string, nodeToken string) ([]wikiNode, error) {
	if nodeToken == "" {
		return c.ListAllWikiNodesRecursive(ctx, spaceID)
	}

	root, err := c.GetWikiNode(ctx, spaceID, nodeToken)
	if err != nil {
		return nil, err
	}

	nodes, err := c.listWikiNodeDescendants(ctx, spaceID, root)
	if err != nil {
		return append([]wikiNode{root}, nodes...), err
	}
	return append([]wikiNode{root}, nodes...), nil
}

func (c *Client) listWikiNodeDescendants(ctx context.Context, spaceID string, root wikiNode) ([]wikiNode, error) {
	if !root.HasChild {
		return nil, nil
	}

	children, err := c.ListWikiNodes(ctx, spaceID, root.NodeToken)
	if err != nil {
		wrappedErr := fmt.Errorf("list children of %s: %w", root.NodeToken, err)
		logger.Warnf(ctx, "[Feishu] partial wiki node listing failure: space=%s node=%s err=%v",
			spaceID, root.NodeToken, err)
		return nil, &partialWikiNodeListError{
			Failures: []wikiNodeListFailure{{
				Node: root,
				Err:  wrappedErr,
			}},
		}
	}

	var allNodes []wikiNode
	var failures []wikiNodeListFailure
	var walk func(nodes []wikiNode)

	walk = func(nodes []wikiNode) {
		for _, node := range nodes {
			allNodes = append(allNodes, node)
			if !node.HasChild {
				continue
			}

			grandChildren, err := c.ListWikiNodes(ctx, spaceID, node.NodeToken)
			if err != nil {
				wrappedErr := fmt.Errorf("list children of %s: %w", node.NodeToken, err)
				failures = append(failures, wikiNodeListFailure{
					Node: node,
					Err:  wrappedErr,
				})
				logger.Warnf(ctx, "[Feishu] partial wiki node listing failure: space=%s node=%s err=%v",
					spaceID, node.NodeToken, err)
				continue
			}
			walk(grandChildren)
		}
	}

	walk(children)
	if len(failures) > 0 {
		return allNodes, &partialWikiNodeListError{Failures: failures}
	}
	return allNodes, nil
}

// GetDocumentRawContent retrieves the raw text content of a Feishu docx document.
// This returns plain text (not rich text / block structure).
// Deprecated: prefer ExportAndDownload which preserves formatting.
func (c *Client) GetDocumentRawContent(ctx context.Context, documentID string) (string, error) {
	path := fmt.Sprintf("/open-apis/docx/v1/documents/%s/raw_content", documentID)

	var resp docRawContentResponse
	if err := c.doRequest(ctx, http.MethodGet, path, nil, &resp); err != nil {
		return "", fmt.Errorf("get document raw content: %w", err)
	}
	if resp.Code != 0 {
		return "", fmt.Errorf("get document raw content error: code=%d msg=%s", resp.Code, resp.Msg)
	}

	return resp.Data.Content, nil
}

// Ping verifies the credentials by attempting to get a tenant access token.
func (c *Client) Ping(ctx context.Context) error {
	_, err := c.getTenantAccessToken(ctx)
	return err
}

// ──────────────────────────────────────────────────────────────────────
// Export task API: export docx/sheet/bitable to downloadable files
//
// Flow:
//  1. POST  /drive/v1/export_tasks             → create export task, get ticket
//  2. GET   /drive/v1/export_tasks/:ticket      → poll until status=0 (success)
//  3. GET   /drive/v1/export_tasks/file/:ticket/download → download file bytes
// ──────────────────────────────────────────────────────────────────────

// CreateExportTask creates an async export task for a Feishu document.
//   - token:         the obj_token of the document (e.g. docx token, sheet token)
//   - objType:       the Feishu obj_type ("docx", "doc", "sheet", "bitable")
//   - fileExtension: desired output format ("docx", "xlsx", "pdf")
func (c *Client) CreateExportTask(ctx context.Context, token, objType, fileExtension string) (string, error) {
	body := map[string]string{
		"file_extension": fileExtension,
		"token":          token,
		"type":           objType,
	}

	var resp exportTaskCreateResponse
	if err := c.doRequest(ctx, http.MethodPost, "/open-apis/drive/v1/export_tasks", body, &resp); err != nil {
		return "", fmt.Errorf("create export task: %w", err)
	}
	if resp.Code != 0 {
		return "", fmt.Errorf("create export task error: code=%d msg=%s", resp.Code, resp.Msg)
	}

	return resp.Data.Ticket, nil
}

// GetExportTaskStatus polls the status of an export task.
// Returns (fileToken, fileName, error). fileToken is non-empty only when the job succeeds.
// The token parameter is the obj_token of the document being exported (required by the API).
func (c *Client) GetExportTaskStatus(ctx context.Context, ticket string, token string) (string, string, error) {
	path := fmt.Sprintf("/open-apis/drive/v1/export_tasks/%s?token=%s", ticket, token)

	var resp exportTaskStatusResponse
	if err := c.doRequest(ctx, http.MethodGet, path, nil, &resp); err != nil {
		return "", "", fmt.Errorf("get export task status: %w", err)
	}
	if resp.Code != 0 {
		return "", "", fmt.Errorf("get export task status error: code=%d msg=%s", resp.Code, resp.Msg)
	}

	r := resp.Data.Result
	switch r.JobStatus {
	case 0: // success
		return r.FileToken, r.FileName, nil
	case 1, 2: // initializing, processing
		return "", "", nil // not ready yet
	default:
		return "", "", fmt.Errorf("export task failed: status=%d msg=%s", r.JobStatus, r.JobErrorMsg)
	}
}

// DownloadExportFile downloads the exported file by its file_token.
// The file_token is returned by GetExportTaskStatus when the export job completes.
// The file must be downloaded within 10 minutes of export completion.
func (c *Client) DownloadExportFile(ctx context.Context, fileToken string) ([]byte, error) {
	path := fmt.Sprintf("/open-apis/drive/v1/export_tasks/file/%s/download", fileToken)
	return c.downloadRawBytes(ctx, path)
}

// ExportAndDownload is a high-level helper that creates an export task, polls until
// completion, and downloads the resulting file. Returns (fileBytes, fileName, error).
//
// Timeout: 60 seconds. Poll interval: 2 seconds.
func (c *Client) ExportAndDownload(ctx context.Context, objToken, objType string) ([]byte, string, error) {
	// Determine export format
	fileExt, ok := objTypeToExportFileExtension[objType]
	if !ok {
		return nil, "", fmt.Errorf("unsupported obj_type for export: %s", objType)
	}

	exportType, ok := objTypeToExportType[objType]
	if !ok {
		return nil, "", fmt.Errorf("unsupported obj_type for export: %s", objType)
	}

	// Step 1: create export task
	ticket, err := c.CreateExportTask(ctx, objToken, exportType, fileExt)
	if err != nil {
		return nil, "", err
	}

	// Step 2: poll until ready (max 60s, every 2s)
	deadline := time.Now().Add(60 * time.Second)
	var fileToken, fileName string

	for time.Now().Before(deadline) {
		fileToken, fileName, err = c.GetExportTaskStatus(ctx, ticket, objToken)
		if err != nil {
			return nil, "", err
		}
		if fileToken != "" {
			break // export ready
		}
		select {
		case <-ctx.Done():
			return nil, "", ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}

	if fileToken == "" {
		return nil, "", fmt.Errorf("export task timed out after 60s (ticket=%s)", ticket)
	}

	// Step 3: download file using file_token (NOT ticket)
	data, err := c.DownloadExportFile(ctx, fileToken)
	if err != nil {
		return nil, "", err
	}

	// Build a sensible file name
	if fileName == "" {
		fileName = "export" + exportFileExtToSuffix[fileExt]
	}

	return data, fileName, nil
}

// ──────────────────────────────────────────────────────────────────────
// Drive file download: for "file" type wiki nodes (uploaded PDF/Word/etc.)
// ──────────────────────────────────────────────────────────────────────

// DownloadDriveFile downloads a file from Feishu Drive by its file token.
// Used for wiki nodes with obj_type="file" (user-uploaded PDF, Word, images, etc.).
func (c *Client) DownloadDriveFile(ctx context.Context, fileToken string) ([]byte, error) {
	path := fmt.Sprintf("/open-apis/drive/v1/files/%s/download", fileToken)
	return c.downloadRawBytes(ctx, path)
}

// downloadRawBytes performs an authenticated GET and returns the raw response body.
func (c *Client) downloadRawBytes(ctx context.Context, path string) ([]byte, error) {
	token, err := c.getTenantAccessToken(ctx)
	if err != nil {
		return nil, err
	}

	url := c.baseURL + path
	var lastErr error

	for attempt := 0; attempt <= feishuMaxRetries; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return nil, fmt.Errorf("create download request: %w", err)
		}
		req.Header.Set("Authorization", "Bearer "+token)

		if attempt == 0 {
			logger.Infof(ctx, "[Feishu] download GET %s", path)
		} else {
			logger.Infof(ctx, "[Feishu] download GET %s (retry %d/%d)", path, attempt, feishuMaxRetries)
		}

		resp, err := c.httpClient.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("download request: %w", err)
			if attempt < feishuMaxRetries {
				if sErr := sleepCtx(ctx, feishuRetryBackoff[attempt]); sErr != nil {
					return nil, sErr
				}
				continue
			}
			return nil, lastErr
		}

		if resp.StatusCode == http.StatusTooManyRequests {
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			wait := parseRetryAfter(resp.Header.Get("Retry-After"), feishuRetryBackoff[min(attempt, len(feishuRetryBackoff)-1)])
			lastErr = fmt.Errorf("download rate limited: status=429 body=%s", truncate(string(body), 500))
			if attempt < feishuMaxRetries {
				if sErr := sleepCtx(ctx, wait); sErr != nil {
					return nil, sErr
				}
				continue
			}
			return nil, lastErr
		}

		if resp.StatusCode >= 500 && resp.StatusCode < 600 {
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			lastErr = fmt.Errorf("download server error: status=%d body=%s", resp.StatusCode, truncate(string(body), 500))
			if attempt < feishuMax5xxRetries {
				if sErr := sleepCtx(ctx, feishuRetry5xxDelay); sErr != nil {
					return nil, sErr
				}
				continue
			}
			return nil, lastErr
		}

		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			logger.Errorf(ctx, "[Feishu] download GET %s → status=%d body=%s", path, resp.StatusCode, truncate(string(body), 500))
			return nil, fmt.Errorf("download failed: status=%d body=%s", resp.StatusCode, string(body))
		}

		data, readErr := io.ReadAll(resp.Body)
		resp.Body.Close()
		if readErr != nil {
			lastErr = fmt.Errorf("read download body: %w", readErr)
			if attempt < feishuMaxRetries {
				if sErr := sleepCtx(ctx, feishuRetryBackoff[attempt]); sErr != nil {
					return nil, sErr
				}
				continue
			}
			return nil, lastErr
		}

		logger.Infof(ctx, "[Feishu] download GET %s → OK, %d bytes", path, len(data))
		return data, nil
	}

	return nil, lastErr
}
