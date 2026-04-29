package feishu

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

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

// NewClient creates a new Feishu API client.
func NewClient(config *Config) *Client {
	return &Client{
		baseURL:    config.GetBaseURL(),
		appID:      config.AppID,
		appSecret:  config.AppSecret,
		httpClient: &http.Client{Timeout: 30 * time.Second},
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

// doRequest executes an authenticated API request and decodes the JSON response.
func (c *Client) doRequest(ctx context.Context, method, path string, body interface{}, result interface{}) error {
	token, err := c.getTenantAccessToken(ctx)
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

	url := c.baseURL + path
	req, err := http.NewRequestWithContext(ctx, method, url, bodyReader)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	req.Header.Set("Authorization", "Bearer "+token)

	logger.Infof(ctx, "[Feishu] %s %s", method, path)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("execute request: %w", err)
	}
	defer resp.Body.Close()

	// Read body once for logging + decoding
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read response body: %w", err)
	}

	logger.Infof(ctx, "[Feishu] %s %s → status=%d bodyLen=%d body=%s",
		method, path, resp.StatusCode, len(respBody), truncate(string(respBody), 1000))

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

		allNodes = append(allNodes, resp.Data.Items...)

		if !resp.Data.HasMore || resp.Data.PageToken == "" {
			break
		}
		pageToken = resp.Data.PageToken
	}

	return allNodes, nil
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
	var walk func(nodes []wikiNode) error

	walk = func(nodes []wikiNode) error {
		for _, node := range nodes {
			allNodes = append(allNodes, node)

			// Recurse into child nodes if this node has children
			if node.HasChild {
				children, err := c.ListWikiNodes(ctx, spaceID, node.NodeToken)
				if err != nil {
					return fmt.Errorf("list children of %s: %w", node.NodeToken, err)
				}
				if err := walk(children); err != nil {
					return err
				}
			}
		}
		return nil
	}

	if err := walk(topNodes); err != nil {
		return nil, err
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
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("create download request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)

	logger.Infof(ctx, "[Feishu] download GET %s", path)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("download request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		logger.Errorf(ctx, "[Feishu] download GET %s → status=%d body=%s", path, resp.StatusCode, truncate(string(body), 500))
		return nil, fmt.Errorf("download failed: status=%d body=%s", resp.StatusCode, string(body))
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read download body: %w", err)
	}

	logger.Infof(ctx, "[Feishu] download GET %s → OK, %d bytes", path, len(data))
	return data, nil
}
