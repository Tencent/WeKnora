package nutstore

import (
	"context"
	"encoding/xml"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	"golang.org/x/time/rate"
)

// defaultRateLimit is the max requests per second to Nutstore WebDAV.
// Nutstore enforces 18,000 requests per 30 minutes = 10/s; we use 8/s for safety margin.
const defaultRateLimit = 8

// Client is the WebDAV HTTP client for Nutstore.
type Client struct {
	baseURL  string // Without /dav suffix
	username string
	password string
	http     *http.Client
	interval time.Duration // Legacy per-request interval (use limiter instead)
	limiter  *rate.Limiter // Token bucket rate limiter
}

// NewClient creates a new Nutstore WebDAV client.
func NewClient(cfg *Config) *Client {
	interval := time.Duration(cfg.RequestIntervalMs) * time.Millisecond
	return &Client{
		baseURL:  cfg.BaseURL,
		username: cfg.Username,
		password: cfg.Password,
		http:     &http.Client{Timeout: 60 * time.Second},
		interval: interval,
		limiter:  rate.NewLimiter(rate.Limit(defaultRateLimit), defaultRateLimit),
	}
}

// davURL builds the full WebDAV URL for a path.
// path should start with "/" (e.g. "/my-docs/file.pdf").
func (c *Client) davURL(p string) string {
	return c.baseURL + "/dav" + p
}

// doRequest executes an HTTP request with Basic Auth and rate limiting.
func (c *Client) doRequest(ctx context.Context, method, url string, body io.Reader, headers map[string]string) (*http.Response, error) {
	// Token bucket rate limiting (replaces legacy interval-based sleep)
	if c.limiter != nil {
		if err := c.limiter.Wait(ctx); err != nil {
			return nil, fmt.Errorf("rate limiter: %w", err)
		}
	} else if c.interval > 0 {
		time.Sleep(c.interval)
	}

	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.SetBasicAuth(c.username, c.password)
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("execute request: %w", err)
	}

	return resp, nil
}

// Ping tests connectivity with an OPTIONS request.
func (c *Client) Ping(ctx context.Context) error {
	resp, err := c.doRequest(ctx, "OPTIONS", c.davURL("/"), nil, nil)
	if err != nil {
		return fmt.Errorf("ping failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		return fmt.Errorf("authentication failed: invalid username or password")
	}
	if resp.StatusCode >= 400 {
		return fmt.Errorf("ping failed with status %d", resp.StatusCode)
	}
	return nil
}

// ListDirectory lists the contents of a directory using PROPFIND.
// depth: 1 for current level, 0 for just the item itself.
// For recursive listing, use depth "infinity" via ListDirectoryRecursive.
func (c *Client) ListDirectory(ctx context.Context, dirPath string, depth string) ([]FileInfo, error) {
	propfindBody := `<?xml version="1.0" encoding="utf-8"?>
<D:propfind xmlns:D="DAV:">
  <D:prop>
    <D:displayname/>
    <D:getcontentlength/>
    <D:getcontenttype/>
    <D:getlastmodified/>
    <D:getetag/>
    <D:resourcetype/>
  </D:prop>
</D:propfind>`

	headers := map[string]string{
		"Content-Type": "application/xml",
		"Depth":        depth,
	}

	resp, err := c.doRequest(ctx, "PROPFIND", c.davURL(dirPath), strings.NewReader(propfindBody), headers)
	if err != nil {
		return nil, fmt.Errorf("PROPFIND %s: %w", dirPath, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("directory not found: %s", dirPath)
	}
	if resp.StatusCode != 207 { // 207 Multi-Status
		return nil, fmt.Errorf("PROPFIND %s returned status %d", dirPath, resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read PROPFIND response: %w", err)
	}

	var ms multistatusResponse
	if err := xml.Unmarshal(body, &ms); err != nil {
		return nil, fmt.Errorf("parse PROPFIND XML: %w", err)
	}

	var files []FileInfo
	for _, r := range ms.Responses {
		fi := c.parseResponse(r, dirPath)
		if fi != nil {
			files = append(files, *fi)
		}
	}

	return files, nil
}

// listDirectoryWithRetry wraps ListDirectory with retry on 503 (rate limit).
// Uses exponential backoff: 2s, 4s, 8s (3 retries max).
func (c *Client) listDirectoryWithRetry(ctx context.Context, dirPath, depth string) ([]FileInfo, error) {
	const maxRetries = 3
	backoff := 2 * time.Second

	for attempt := 0; ; attempt++ {
		files, err := c.ListDirectory(ctx, dirPath, depth)
		if err == nil {
			return files, nil
		}
		if attempt >= maxRetries || !strings.Contains(err.Error(), "status 503") {
			return nil, err
		}
		logger.Warnf(ctx, "ListDirectory %s returned 503 (rate limited), retrying in %v (attempt %d/%d)", dirPath, backoff, attempt+1, maxRetries)
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(backoff):
		}
		backoff *= 2
	}
}

// ListDirectoryRecursive lists all files and directories under a path recursively.
// Uses manual BFS with Depth:1 PROPFIND calls because Nutstore's WebDAV server
// does not support Depth:infinity (it silently degrades to Depth:1).
// Automatically retries on 503 (rate limit) with exponential backoff.
func (c *Client) ListDirectoryRecursive(ctx context.Context, dirPath string) ([]FileInfo, error) {
	var allFiles []FileInfo
	queue := []string{dirPath}

	dirsScanned := 0
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		dirsScanned++

		if dirsScanned%100 == 1 {
			logger.Infof(ctx, "ListDirectoryRecursive: scanning %s (%d items found, %d dirs pending)", current, len(allFiles), len(queue))
		}

		entries, err := c.listDirectoryWithRetry(ctx, current, "1")
		if err != nil {
			return nil, fmt.Errorf("list %s: %w", current, err)
		}

		for _, entry := range entries {
			allFiles = append(allFiles, entry)
			if entry.IsDir {
				childPath := entry.Path
				if !strings.HasSuffix(childPath, "/") {
					childPath += "/"
				}
				queue = append(queue, childPath)
			}
		}
	}

	return allFiles, nil
}

// parseResponse converts a PROPFIND XML response entry to FileInfo.
func (c *Client) parseResponse(r response, basePath string) *FileInfo {
	// Decode URL-encoded href
	href, err := url.PathUnescape(r.Href)
	if err != nil {
		href = r.Href
	}

	// Strip the /dav prefix to get the logical path
	filePath := href
	if idx := strings.Index(filePath, "/dav/"); idx >= 0 {
		filePath = filePath[idx+4:] // keep the leading /
	} else if strings.HasPrefix(filePath, "/dav") {
		filePath = filePath[4:]
	}
	if filePath == "" {
		filePath = "/"
	}
	// Remove trailing slash for consistency (except root)
	if len(filePath) > 1 {
		filePath = strings.TrimRight(filePath, "/")
	}

	isDir := r.Propstat.Prop.ResourceType.Collection != nil

	// Skip the directory itself (self-referencing entry)
	cleanBase := strings.TrimRight(basePath, "/")
	if cleanBase == "" {
		cleanBase = "/"
	}
	if filePath == cleanBase || filePath+"/" == basePath {
		// This is the directory itself, only skip if it's the queried directory
		if isDir {
			return nil
		}
	}

	name := r.Propstat.Prop.DisplayName
	if name == "" {
		name = path.Base(filePath)
	}

	return &FileInfo{
		Path:         filePath,
		Name:         name,
		IsDir:        isDir,
		Size:         r.Propstat.Prop.ContentLen,
		LastModified: parseLastModified(r.Propstat.Prop.LastModified),
		ContentType:  r.Propstat.Prop.ContentType,
		ETag:         r.Propstat.Prop.ETag,
	}
}

// DownloadFile downloads a file's content.
// Returns file bytes, content-type, and error.
func (c *Client) DownloadFile(ctx context.Context, filePath string) ([]byte, string, error) {
	resp, err := c.doRequest(ctx, "GET", c.davURL(filePath), nil, nil)
	if err != nil {
		return nil, "", fmt.Errorf("download %s: %w", filePath, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, "", fmt.Errorf("file not found: %s", filePath)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("download %s returned status %d", filePath, resp.StatusCode)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", fmt.Errorf("read file content: %w", err)
	}

	contentType := resp.Header.Get("Content-Type")
	return data, contentType, nil
}

// GetShareURL gets a share link for a file using the Nutstore enterprise API.
// Endpoint: POST {baseURL}/nsdav/pubObject
// This may not be available on public Nutstore (dav.jianguoyun.com).
func (c *Client) GetShareURL(ctx context.Context, filePath string) (string, error) {
	// Preprocess path: handle special characters
	cleanPath := strings.ReplaceAll(filePath, "%U00A0", " ")
	cleanPath = strings.ReplaceAll(cleanPath, "\u00a0", " ")

	xmlBody := fmt.Sprintf(
		`<?xml version="1.0" encoding="utf-8"?>
<s:publish xmlns:s="http://ns.jianguoyun.com">
    <s:href>/dav/%s</s:href>
</s:publish>`, html.EscapeString(strings.TrimPrefix(cleanPath, "/")))

	headers := map[string]string{
		"Content-Type": "application/xml",
	}

	resp, err := c.doRequest(ctx, "POST", c.baseURL+"/nsdav/pubObject", strings.NewReader(xmlBody), headers)
	if err != nil {
		return "", fmt.Errorf("get share URL for %s: %w", filePath, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		// Share link API may not be available (public Nutstore)
		logger.Warnf(ctx, "GetShareURL for %s returned status %d, share links may not be supported", filePath, resp.StatusCode)
		return "", nil
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read share response: %w", err)
	}

	var pubResp publishResponse
	if err := xml.Unmarshal(body, &pubResp); err != nil {
		return "", fmt.Errorf("parse share response XML: %w", err)
	}

	return pubResp.ShareLink, nil
}
