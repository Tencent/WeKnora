package wecom

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

// Client wraps the WeCom API for document and WeDrive operations.
type Client struct {
	corpID     string
	corpSecret string
	httpClient *http.Client

	tokenMu    sync.Mutex
	tokenCache string
	tokenExpAt time.Time
}

// NewClient creates a new WeCom API client.
func NewClient(config *Config) *Client {
	return &Client{
		corpID:     config.CorpID,
		corpSecret: config.CorpSecret,
		httpClient: &http.Client{Timeout: 30 * time.Second},
	}
}

// getAccessToken retrieves (or returns cached) access token.
// WeCom tokens expire in 7200s (2 hours); we cache with a 5-minute safety margin.
func (c *Client) getAccessToken(ctx context.Context) (string, error) {
	c.tokenMu.Lock()
	defer c.tokenMu.Unlock()

	if c.tokenCache != "" && time.Now().Before(c.tokenExpAt) {
		return c.tokenCache, nil
	}

	url := fmt.Sprintf("%s/cgi-bin/gettoken?corpid=%s&corpsecret=%s", BaseURL, c.corpID, c.corpSecret)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", fmt.Errorf("create token request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("request token: %w", err)
	}
	defer resp.Body.Close()

	var result tokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decode token response: %w", err)
	}
	if !result.ok() {
		return "", result.wrapError("wecom auth")
	}

	c.tokenCache = result.AccessToken
	ttl := time.Duration(result.ExpiresIn) * time.Second
	if ttl > 5*time.Minute {
		ttl -= 5 * time.Minute
	}
	c.tokenExpAt = time.Now().Add(ttl)

	logger.Infof(ctx, "[WeCom] got access_token: %s...%s expires_in=%ds",
		safePrefix(result.AccessToken, 8), safeSuffix(result.AccessToken, 4), result.ExpiresIn)

	return c.tokenCache, nil
}

// doPost executes an authenticated POST request and decodes the JSON response.
func (c *Client) doPost(ctx context.Context, path string, body interface{}, result interface{}) error {
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

	url := fmt.Sprintf("%s%s?access_token=%s", BaseURL, path, token)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bodyReader)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")

	logger.Infof(ctx, "[WeCom] POST %s", path)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("execute request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read response body: %w", err)
	}

	logger.Infof(ctx, "[WeCom] POST %s → status=%d bodyLen=%d body=%s",
		path, resp.StatusCode, len(respBody), truncate(string(respBody), 1000))

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("wecom api error: status=%d body=%s", resp.StatusCode, string(respBody))
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

// ──────────────────────────────────────────────────────────────────────
// Smart Document APIs (wedoc)
// ──────────────────────────────────────────────────────────────────────

// ListSmartDocuments lists all smart documents accessible to the app.
// docType: 0=all, 3=doc, 4=sheet, 10=form.
func (c *Client) ListSmartDocuments(ctx context.Context, docType int) ([]docListItem, error) {
	var allDocs []docListItem
	cursor := ""

	for {
		body := map[string]interface{}{
			"limit": 50,
		}
		if docType != DocFilterAll {
			body["doc_type"] = docType
		}
		if cursor != "" {
			body["cursor"] = cursor
		}

		var resp docListResponse
		if err := c.doPost(ctx, "/cgi-bin/wedoc/doc_list", body, &resp); err != nil {
			return nil, fmt.Errorf("list smart documents: %w", err)
		}
		if !resp.ok() {
			return nil, resp.wrapError("list smart documents")
		}

		logger.Infof(ctx, "[WeCom] ListSmartDocuments: got %d docs, has_more=%v", len(resp.DocInfoList), resp.HasMore)

		allDocs = append(allDocs, resp.DocInfoList...)

		if !resp.HasMore || resp.NextCursor == "" {
			break
		}
		cursor = resp.NextCursor
	}

	logger.Infof(ctx, "[WeCom] ListSmartDocuments: total %d docs (type=%d)", len(allDocs), docType)
	return allDocs, nil
}

// GetDocBaseInfo retrieves metadata for a single document.
func (c *Client) GetDocBaseInfo(ctx context.Context, docID string) (*docBaseInfo, error) {
	body := map[string]string{"docid": docID}

	var resp docBaseInfoResponse
	if err := c.doPost(ctx, "/cgi-bin/wedoc/get_doc_base_info", body, &resp); err != nil {
		return nil, fmt.Errorf("get doc base info: %w", err)
	}
	if !resp.ok() {
		return nil, resp.wrapError("get doc base info")
	}

	return &resp.DocBaseInfo, nil
}

// ──────────────────────────────────────────────────────────────────────
// WeDrive APIs (微盘)
// ──────────────────────────────────────────────────────────────────────

// ListDriveFiles lists all files in a WeDrive space folder recursively.
// If fatherID is empty, lists files in the space root.
func (c *Client) ListDriveFiles(ctx context.Context, spaceID, fatherID string) ([]driveFileItem, error) {
	var allFiles []driveFileItem
	start := 0

	for {
		body := map[string]interface{}{
			"spaceid": spaceID,
			"sort_type": 5, // sort by modify time desc
			"start":     start,
			"limit":     1000,
		}
		if fatherID != "" {
			body["fatherid"] = fatherID
		}

		var resp driveFileListResponse
		if err := c.doPost(ctx, "/cgi-bin/wedrive/file_list", body, &resp); err != nil {
			return nil, fmt.Errorf("list drive files: %w", err)
		}
		if !resp.ok() {
			return nil, resp.wrapError("list drive files")
		}

		allFiles = append(allFiles, resp.FileList.Item...)

		if !resp.FileList.HasMore || len(resp.FileList.Item) == 0 {
			break
		}
		start += len(resp.FileList.Item)
	}

	return allFiles, nil
}

// ListAllDriveFilesRecursive recursively lists all files in a WeDrive space.
func (c *Client) ListAllDriveFilesRecursive(ctx context.Context, spaceID string) ([]driveFileItem, error) {
	topFiles, err := c.ListDriveFiles(ctx, spaceID, "")
	if err != nil {
		return nil, err
	}

	var allFiles []driveFileItem
	var walk func(files []driveFileItem) error

	walk = func(files []driveFileItem) error {
		for _, f := range files {
			allFiles = append(allFiles, f)
			if f.FileType == FileTypeFolder {
				children, err := c.ListDriveFiles(ctx, spaceID, f.FileID)
				if err != nil {
					return fmt.Errorf("list children of %s: %w", f.FileID, err)
				}
				if err := walk(children); err != nil {
					return err
				}
			}
		}
		return nil
	}

	if err := walk(topFiles); err != nil {
		return nil, err
	}
	return allFiles, nil
}

// GetDriveFileDownloadURL gets the temporary download URL for a WeDrive file.
func (c *Client) GetDriveFileDownloadURL(ctx context.Context, fileID, userID string) (*driveFileDownloadResponse, error) {
	body := map[string]string{
		"fileid": fileID,
		"userid": userID,
	}

	var resp driveFileDownloadResponse
	if err := c.doPost(ctx, "/cgi-bin/wedrive/file_download", body, &resp); err != nil {
		return nil, fmt.Errorf("get drive file download url: %w", err)
	}
	if !resp.ok() {
		return nil, resp.wrapError("get drive file download url")
	}

	return &resp, nil
}

// DownloadFromURL downloads the file content from a temporary download URL with cookie auth.
func (c *Client) DownloadFromURL(ctx context.Context, downloadURL, cookieName, cookieValue string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, downloadURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create download request: %w", err)
	}
	if cookieName != "" && cookieValue != "" {
		req.AddCookie(&http.Cookie{Name: cookieName, Value: cookieValue})
	}

	logger.Infof(ctx, "[WeCom] download GET %s", truncate(downloadURL, 100))

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("download request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("download failed: status=%d body=%s", resp.StatusCode, truncate(string(body), 500))
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read download body: %w", err)
	}

	logger.Infof(ctx, "[WeCom] download → OK, %d bytes", len(data))
	return data, nil
}

// --- Helper functions ---

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

func safePrefix(s string, n int) string {
	if len(s) < n {
		return s
	}
	return s[:n]
}

func safeSuffix(s string, n int) string {
	if len(s) < n {
		return s
	}
	return s[len(s)-n:]
}
