package confluence

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/datasource"
	"github.com/Tencent/WeKnora/internal/logger"
)

// Client wraps the Confluence REST API with Basic Auth.
// Supports both Server / Data Center 7.x and Cloud editions.
type Client struct {
	baseURL    string
	username   string
	password   string // Server: user password; Cloud: API token
	isCloud    bool
	httpClient *http.Client
}

// NewClient creates a new Confluence API client.
func NewClient(config *Config) (*Client, error) {
	baseURL := strings.TrimRight(config.BaseURL, "/")
	if err := datasource.ValidateConnectorBaseURL(baseURL); err != nil {
		return nil, err
	}

	// Determine the auth password / token.
	password := config.Password
	if config.IsCloud() {
		password = config.APIToken
	}

	return &Client{
		baseURL:    baseURL,
		username:   config.Username,
		password:   password,
		isCloud:    config.IsCloud(),
		httpClient: datasource.NewConnectorHTTPClient(60 * time.Second),
	}, nil
}

// Ping tests connectivity by fetching the current user or a lightweight endpoint.
func (c *Client) Ping(ctx context.Context) error {
	// Use /rest/api/space with limit=1 as a lightweight connectivity check
	_, err := c.doRequest(ctx, http.MethodGet, "/rest/api/space?limit=1", nil)
	if err != nil {
		return fmt.Errorf("ping %s failed (user=%s): %w", c.baseURL, c.username, err)
	}
	return nil
}

// maxRetries is the number of retry attempts for transient errors (429, 5xx).
const maxRetries = 3

// doRequest performs an authenticated HTTP request to the Confluence API.
// Transient failures (HTTP 429 / 5xx) are retried with exponential backoff.
func (c *Client) doRequest(ctx context.Context, method, path string, result interface{}) ([]byte, error) {
	var fullURL string
	if strings.HasPrefix(path, "http") {
		fullURL = path
	} else {
		fullURL = c.baseURL + path
	}

	var lastErr error
	for attempt := 0; attempt <= maxRetries; attempt++ {
		if attempt > 0 {
			wait := time.Duration(1<<uint(attempt-1)) * time.Second // 1s, 2s, 4s
			logger.Infof(ctx, "[Confluence] retry %d/%d after %v for %s %s", attempt, maxRetries, wait, method, path)
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(wait):
			}
		}

		req, err := http.NewRequestWithContext(ctx, method, fullURL, nil)
		if err != nil {
			return nil, fmt.Errorf("create request: %w", err)
		}

		req.SetBasicAuth(c.username, c.password)
		req.Header.Set("Accept", "application/json")

		logger.Infof(ctx, "[Confluence] %s %s", method, path)

		resp, err := c.httpClient.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("HTTP %s %s failed: %w", method, fullURL, err)
			continue // network error → retry
		}

		body, err := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("read response body: %w", err)
		}

		logger.Infof(ctx, "[Confluence] %s %s → status=%d bodyLen=%d",
			method, path, resp.StatusCode, len(body))

		// Retry on 429 (rate limit) or 5xx (server error)
		if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
			lastErr = fmt.Errorf("confluence API error: status=%d body=%s",
				resp.StatusCode, truncate(string(body), 500))
			continue
		}

		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("confluence API error: status=%d body=%s",
				resp.StatusCode, truncate(string(body), 500))
		}

		if result != nil {
			if err := json.Unmarshal(body, result); err != nil {
				return nil, fmt.Errorf("decode response: %w", err)
			}
		}

		return body, nil
	}

	return nil, fmt.Errorf("confluence API request failed after %d retries: %w", maxRetries, lastErr)
}

// doRequestRaw performs an authenticated HTTP request and returns raw bytes (for PDF download).
func (c *Client) doRequestRaw(ctx context.Context, path string) ([]byte, string, error) {
	fullURL := c.baseURL + path

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fullURL, nil)
	if err != nil {
		return nil, "", fmt.Errorf("create request: %w", err)
	}

	req.SetBasicAuth(c.username, c.password)
	req.Header.Set("Accept", "application/pdf, application/octet-stream, */*")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("HTTP GET %s failed: %w", fullURL, err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", fmt.Errorf("read response body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("confluence API error: status=%d body=%s",
			resp.StatusCode, truncate(string(body), 500))
	}

	contentType := resp.Header.Get("Content-Type")
	return body, contentType, nil
}

// --- Space APIs ---

// ListSpaces returns all accessible Confluence spaces.
// For Server/DC: uses v1 API with offset pagination.
// For Cloud: uses v2 API with cursor pagination.
//
//nolint:revive // returns unexported type by design; Client is package-internal
func (c *Client) ListSpaces(ctx context.Context) ([]confluenceSpace, error) {
	if c.isCloud {
		return c.listSpacesV2(ctx)
	}
	return c.listSpacesV1(ctx)
}

// listSpacesV1 lists spaces using the v1 REST API (Server / DC).
func (c *Client) listSpacesV1(ctx context.Context) ([]confluenceSpace, error) {
	var allSpaces []confluenceSpace
	start := 0
	limit := 50

	for {
		path := fmt.Sprintf("/rest/api/space?start=%d&limit=%d", start, limit)
		var resp confluenceSpaceListResponse
		_, err := c.doRequest(ctx, http.MethodGet, path, &resp)
		if err != nil {
			return nil, fmt.Errorf("list spaces v1 (start=%d): %w", start, err)
		}

		// Convert v1 int IDs to the common string ID format.
		for _, v1 := range resp.Results {
			allSpaces = append(allSpaces, confluenceSpace{
				ID:   strconv.Itoa(v1.ID),
				Key:  v1.Key,
				Name: v1.Name,
				Type: v1.Type,
				Homepage: struct {
					ID string `json:"id"`
				}{
					ID: strconv.Itoa(v1.Homepage.ID),
				},
				Description: v1.Description,
				Links:       v1.Links,
			})
		}

		if resp.Links.Next == "" || len(resp.Results) < limit {
			break
		}
		start += len(resp.Results)
	}

	return allSpaces, nil
}

// listSpacesV2 lists spaces using the v2 API (Cloud).
// Cloud uses cursor-based pagination. The v2 space ID is a string,
// so we convert it to the common confluenceSpace type for compatibility.
func (c *Client) listSpacesV2(ctx context.Context) ([]confluenceSpace, error) {
	var allSpaces []confluenceSpace
	cursor := ""
	limit := 50

	for {
		path := fmt.Sprintf("/api/v2/spaces?limit=%d", limit)
		if cursor != "" {
			path += "&cursor=" + cursor
		}

		var resp confluenceSpaceV2ListResponse
		_, err := c.doRequest(ctx, http.MethodGet, path, &resp)
		if err != nil {
			return nil, fmt.Errorf("list spaces v2: %w", err)
		}

		// Convert v2 spaces to the common confluenceSpace type
		for _, v2 := range resp.Results {
			allSpaces = append(allSpaces, confluenceSpace{
				ID:   v2.ID,
				Key:  v2.Key,
				Name: v2.Name,
				Type: v2.Type,
				Links: struct {
					WebUI string `json:"webui"`
					Self  string `json:"self"`
				}{
					WebUI: v2.Links.WebUI,
					Self:  v2.Links.Self,
				},
			})
		}

		if resp.Links.Next == "" || len(resp.Results) < limit {
			break
		}
		// Extract cursor from the next link
		// Next looks like: /api/v2/spaces?cursor=xxxxx&limit=50
		nextLink := resp.Links.Next
		if idx := strings.Index(nextLink, "cursor="); idx >= 0 {
			cursor = nextLink[idx+7:]
			if end := strings.Index(cursor, "&"); end >= 0 {
				cursor = cursor[:end]
			}
		} else {
			break
		}
	}

	return allSpaces, nil
}

// --- Content APIs ---

// GetPage retrieves a single page by ID with version and space info.
//
//nolint:revive // returns unexported type by design; Client is package-internal
func (c *Client) GetPage(ctx context.Context, pageID string) (*confluencePage, error) {
	path := fmt.Sprintf("/rest/api/content/%s?expand=version,space", pageID)
	var page confluencePage
	_, err := c.doRequest(ctx, http.MethodGet, path, &page)
	if err != nil {
		return nil, err
	}
	return &page, nil
}

// GetAllPagesInSpace returns ALL current pages in a space using CQL (v1 API).
// Used by Server / Data Center edition.
// For Cloud, use GetAllPagesInSpaceV2 instead.
//
//nolint:revive // returns unexported type by design; Client is package-internal
func (c *Client) GetAllPagesInSpace(ctx context.Context, spaceKey string) ([]confluencePage, error) {
	var allPages []confluencePage
	start := 0
	limit := 100

	cql := fmt.Sprintf(`type=page AND space="%s" order by lastModified desc`, spaceKey)

	for {
		path := fmt.Sprintf("/rest/api/content/search?cql=%s&start=%d&limit=%d&expand=version,space",
			url.QueryEscape(cql), start, limit)
		var resp confluenceSearchResponse
		_, err := c.doRequest(ctx, http.MethodGet, path, &resp)
		if err != nil {
			return nil, fmt.Errorf("search all pages in space %s (start=%d): %w", spaceKey, start, err)
		}

		allPages = append(allPages, resp.Results...)

		if resp.Links.Next == "" || len(resp.Results) < limit {
			break
		}
		start += len(resp.Results)
	}

	return allPages, nil
}

// GetAllPagesInSpaceV2 lists all current pages in a space using the Cloud v2 API
// (GET /api/v2/spaces/{spaceId}/pages). The v2 response already includes
// version info, so no additional GetPage calls are needed.
//
//nolint:revive // returns unexported type by design; Client is package-internal
func (c *Client) GetAllPagesInSpaceV2(
	ctx context.Context, spaceID, spaceKey, spaceName string,
) ([]confluencePage, error) {
	var allPages []confluencePage
	cursor := ""
	limit := 25

	// Parse spaceID to int for confluencePage.Space.ID
	spaceIDInt, _ := strconv.Atoi(spaceID)

	for {
		path := fmt.Sprintf("/api/v2/spaces/%s/pages?limit=%d&status=current", spaceID, limit)
		if cursor != "" {
			path += "&cursor=" + cursor
		}

		var resp confluencePageV2ListResponse
		_, err := c.doRequest(ctx, http.MethodGet, path, &resp)
		if err != nil {
			return nil, fmt.Errorf("list v2 pages in space %s: %w", spaceID, err)
		}

		for _, p := range resp.Results {
			// Use version.createdAt as the change timestamp (v2 doesn't have version.when)
			versionWhen := p.Version.CreatedAt
			if versionWhen == "" {
				versionWhen = p.CreatedAt
			}
			allPages = append(allPages, confluencePage{
				ID:     p.ID,
				Title:  p.Title,
				Type:   "page",
				Status: p.Status,
				Space: struct {
					ID   int    `json:"id"`
					Key  string `json:"key"`
					Name string `json:"name"`
				}{
					ID:   spaceIDInt,
					Key:  spaceKey,
					Name: spaceName,
				},
				Version: struct {
					By struct {
						DisplayName string `json:"displayName"`
					} `json:"by"`
					When string `json:"when"`
				}{
					When: versionWhen,
				},
				Links: struct {
					WebUI string `json:"webui"`
					Self  string `json:"self"`
				}{
					WebUI: p.Links.WebUI,
				},
			})
		}

		if resp.Links.Next == "" || len(resp.Results) < limit {
			break
		}
		// Extract cursor from next link
		nextLink := resp.Links.Next
		if idx := strings.Index(nextLink, "cursor="); idx >= 0 {
			cursor = nextLink[idx+7:]
			if end := strings.Index(cursor, "&"); end >= 0 {
				cursor = cursor[:end]
			}
		} else {
			break
		}
	}

	return allPages, nil
}

// GetTrashedPagesBySpace returns all pages in the trash for a given space.
//
//nolint:revive // returns unexported type by design; Client is package-internal
func (c *Client) GetTrashedPagesBySpace(ctx context.Context, spaceKey string) ([]confluencePage, error) {
	var allPages []confluencePage
	start := 0
	limit := 100

	for {
		path := fmt.Sprintf("/rest/api/content?spaceKey=%s&status=trashed&type=page&start=%d&limit=%d",
			url.QueryEscape(spaceKey), start, limit)
		var resp confluenceContentListResponse
		_, err := c.doRequest(ctx, http.MethodGet, path, &resp)
		if err != nil {
			// If the space doesn't exist or we get an error, return what we have
			logger.Warnf(ctx, "[Confluence] failed to list trashed pages for space %s: %v", spaceKey, err)
			break
		}

		allPages = append(allPages, resp.Results...)

		if resp.Links.Next == "" || len(resp.Results) < limit {
			break
		}
		start += len(resp.Results)
	}

	return allPages, nil
}

// --- PDF Export ---

// ExportPageAsPDF exports a Confluence page as PDF using the built-in export action.
// Returns the PDF bytes and a suggested filename.
// Only works for Server / Data Center edition.
func (c *Client) ExportPageAsPDF(ctx context.Context, pageID string, pageTitle string) ([]byte, string, error) {
	path := fmt.Sprintf("/spaces/flyingpdf/pdfpageexport.action?pageId=%s", pageID)

	data, contentType, err := c.doRequestRaw(ctx, path)
	if err != nil {
		return nil, "", fmt.Errorf("export page %s as PDF: %w", pageID, err)
	}

	// Validate that we actually got a PDF
	if !strings.Contains(strings.ToLower(contentType), "pdf") || len(data) < 100 {
		return nil, "", fmt.Errorf("export page %s: response is not a PDF (content-type: %s, size: %d)",
			pageID, contentType, len(data))
	}

	// Build filename
	filename := safeFilename(pageTitle) + ".pdf"

	logger.Infof(ctx, "[Confluence] exported page %s (%s) as PDF: %d bytes",
		pageID, pageTitle, len(data))

	return data, filename, nil
}

// ExportPageAsPDFViaExportView fetches a Confluence Cloud page's body.export_view
// HTML, inlines images, and renders the result to PDF via headless Chrome.
// Used for Cloud edition where the legacy PDF export endpoint is unavailable.
func (c *Client) ExportPageAsPDFViaExportView(
	ctx context.Context, pageID string, pageTitle string,
) ([]byte, string, error) {
	return exportViewPageToPDF(ctx, c, pageID, pageTitle)
}

// truncate shortens a string to maxLen, appending "..." if truncated.
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
