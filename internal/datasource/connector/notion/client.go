package notion

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"golang.org/x/time/rate"

	"github.com/Tencent/WeKnora/internal/datasource"
	"github.com/Tencent/WeKnora/internal/logger"
)

// notionClient wraps the Notion API with rate limiting and retry logic.
type notionClient struct {
	token      string
	httpClient *http.Client
	limiter    *rate.Limiter
	baseURL    string
}

// newClient creates a new Notion API client.
func newClient(token, baseURL string) *notionClient {
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	return &notionClient{
		token:      token,
		httpClient: &http.Client{Timeout: 30 * time.Second},
		limiter:    rate.NewLimiter(rate.Limit(3), 3), // 3 req/s with burst of 3 (matches Notion's actual behavior)
		baseURL:    baseURL,
	}
}

const maxRetries = 3

// sleepWithContext pauses for the given duration, returning early if ctx is cancelled.
func sleepWithContext(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// doRequest performs an authenticated, rate-limited HTTP request to the Notion API.
func (c *notionClient) doRequest(ctx context.Context, method, path string, body interface{}) ([]byte, error) {
	if err := c.limiter.Wait(ctx); err != nil {
		return nil, fmt.Errorf("rate limiter: %w", err)
	}

	var bodyReader io.Reader
	if body != nil {
		bodyBytes, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshal request body: %w", err)
		}
		bodyReader = bytes.NewReader(bodyBytes)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Notion-Version", NotionAPIVersion)
	req.Header.Set("Content-Type", "application/json")

	var lastErr error
	for attempt := 0; attempt <= maxRetries; attempt++ {
		if attempt > 0 {
			if body != nil {
				bodyBytes, _ := json.Marshal(body)
				req.Body = io.NopCloser(bytes.NewReader(bodyBytes))
			}
		}

		resp, err := c.httpClient.Do(req)
		if err != nil {
			lastErr = err
			if attempt < maxRetries {
				if sErr := sleepWithContext(ctx, time.Duration(1<<attempt)*time.Second); sErr != nil {
					return nil, sErr
				}
				continue
			}
			break
		}

		respBody, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("read response: %w", err)
		}

		switch {
		case resp.StatusCode >= 200 && resp.StatusCode < 300:
			return respBody, nil

		case resp.StatusCode == 401 || resp.StatusCode == 403:
			return nil, fmt.Errorf("%w: %s", datasource.ErrInvalidCredentials, string(respBody))

		case resp.StatusCode == 404:
			return nil, fmt.Errorf("%w: %s", datasource.ErrResourceNotFound, path)

		case resp.StatusCode == 429:
			retryAfter := resp.Header.Get("Retry-After")
			wait := 1 * time.Second
			if secs, err := strconv.ParseFloat(retryAfter, 64); err == nil && secs > 0 {
				wait = time.Duration(secs * float64(time.Second))
			}
			logger.Warnf(ctx, "[Notion] rate limited, retry after %v (attempt %d/%d)", wait, attempt+1, maxRetries)
			lastErr = fmt.Errorf("rate limited: %s", string(respBody))
			if attempt < maxRetries {
				if sErr := sleepWithContext(ctx, wait); sErr != nil {
					return nil, sErr
				}
				continue
			}

		case resp.StatusCode >= 500:
			lastErr = fmt.Errorf("server error %d: %s", resp.StatusCode, string(respBody))
			if attempt < maxRetries {
				if sErr := sleepWithContext(ctx, time.Duration(1<<attempt)*time.Second); sErr != nil {
					return nil, sErr
				}
				continue
			}

		default:
			return nil, fmt.Errorf("unexpected status %d: %s", resp.StatusCode, string(respBody))
		}
	}

	if lastErr != nil {
		return nil, fmt.Errorf("%w: %v", datasource.ErrFetchFailed, lastErr)
	}
	return nil, datasource.ErrFetchFailed
}

// Ping verifies the API token is valid by calling GET /v1/users/me.
func (c *notionClient) Ping(ctx context.Context) error {
	_, err := c.doRequest(ctx, http.MethodGet, "/v1/users/me", nil)
	return err
}

// SearchPages returns all pages and databases accessible to the integration.
func (c *notionClient) SearchPages(ctx context.Context) ([]notionPage, error) {
	return c.paginatePages(ctx, http.MethodPost, "/v1/search")
}

// GetPage retrieves a single page by ID.
func (c *notionClient) GetPage(ctx context.Context, pageID string) (*notionPage, error) {
	respBody, err := c.doRequest(ctx, http.MethodGet, "/v1/pages/"+pageID, nil)
	if err != nil {
		return nil, err
	}

	var page notionPage
	if err := json.Unmarshal(respBody, &page); err != nil {
		return nil, fmt.Errorf("unmarshal page: %w", err)
	}
	page.Title = extractTitle(&page)
	return &page, nil
}

// databaseInfo holds both the metadata and the data source ID from a single API call.
type databaseInfo struct {
	Page         notionPage
	DataSourceID string
}

// GetDatabaseInfo retrieves a database container by ID, returning both
// the page metadata and the primary data source ID in a single API call.
func (c *notionClient) GetDatabaseInfo(ctx context.Context, dbID string) (*databaseInfo, error) {
	respBody, err := c.doRequest(ctx, http.MethodGet, "/v1/databases/"+dbID, nil)
	if err != nil {
		return nil, err
	}

	var db notionPage
	if err := json.Unmarshal(respBody, &db); err != nil {
		return nil, fmt.Errorf("unmarshal database: %w", err)
	}
	db.Title = extractTitle(&db)

	var dsResult struct {
		DataSources []struct {
			ID string `json:"id"`
		} `json:"data_sources"`
	}
	if err := json.Unmarshal(respBody, &dsResult); err != nil {
		return nil, fmt.Errorf("unmarshal data_sources: %w", err)
	}

	dsID := ""
	if len(dsResult.DataSources) > 0 {
		dsID = dsResult.DataSources[0].ID
	}

	return &databaseInfo{Page: db, DataSourceID: dsID}, nil
}

// GetDataSourceInfo retrieves a data source by ID, returning its metadata.
// In API 2025-09-03+, data_source objects hold the schema/properties and are
// the target for record queries. The response includes database_parent to
// locate the database in the workspace hierarchy.
func (c *notionClient) GetDataSourceInfo(ctx context.Context, dsID string) (*notionPage, error) {
	respBody, err := c.doRequest(ctx, http.MethodGet, "/v1/data_sources/"+dsID, nil)
	if err != nil {
		return nil, err
	}
	var ds notionPage
	if err := json.Unmarshal(respBody, &ds); err != nil {
		return nil, fmt.Errorf("unmarshal data_source: %w", err)
	}
	ds.Title = extractTitle(&ds)
	return &ds, nil
}

// GetBlockChildrenFlat fetches only the direct children of a block (no recursion).
// Used by discoverPages to quickly scan for child_page/child_database without
// fetching the full block tree content.
func (c *notionClient) GetBlockChildrenFlat(ctx context.Context, blockID string) ([]notionBlock, error) {
	var allBlocks []notionBlock
	var startCursor string

	for {
		path := fmt.Sprintf("/v1/blocks/%s/children", blockID)
		if startCursor != "" {
			path += "?start_cursor=" + startCursor
		}

		respBody, err := c.doRequest(ctx, http.MethodGet, path, nil)
		if err != nil {
			return nil, fmt.Errorf("get block children for %s: %w", blockID, err)
		}

		var resp paginatedResponse
		if err := json.Unmarshal(respBody, &resp); err != nil {
			return nil, fmt.Errorf("unmarshal block children response: %w", err)
		}

		var blocks []notionBlock
		if err := json.Unmarshal(resp.Results, &blocks); err != nil {
			return nil, fmt.Errorf("unmarshal blocks: %w", err)
		}

		allBlocks = append(allBlocks, blocks...)

		if !resp.HasMore || resp.NextCursor == "" {
			break
		}
		startCursor = resp.NextCursor
	}

	return allBlocks, nil
}

const maxBlockDepth = 5       // Limit recursion depth — deeper content has diminishing value for knowledge bases
const maxBlocksPerPage = 1000 // Limit total blocks fetched per page to prevent runaway API calls

// GetBlockChildrenAll recursively fetches all blocks under a given block ID,
// building a tree structure with Children populated for blocks with has_children=true.
// child_page and child_database blocks are NOT recursed into (handled by connector layer).
// Recursion is limited to maxBlockDepth to prevent excessive API calls on complex pages.
func (c *notionClient) GetBlockChildrenAll(ctx context.Context, blockID string) ([]notionBlock, error) {
	return c.getBlockChildrenRecursive(ctx, blockID, 0)
}

func (c *notionClient) getBlockChildrenRecursive(ctx context.Context, blockID string, depth int) ([]notionBlock, error) {
	var allBlocks []notionBlock
	var startCursor string

	for {
		path := fmt.Sprintf("/v1/blocks/%s/children", blockID)
		if startCursor != "" {
			path += "?start_cursor=" + startCursor
		}

		respBody, err := c.doRequest(ctx, http.MethodGet, path, nil)
		if err != nil {
			return nil, fmt.Errorf("get block children for %s: %w", blockID, err)
		}

		var resp paginatedResponse
		if err := json.Unmarshal(respBody, &resp); err != nil {
			return nil, fmt.Errorf("unmarshal block children response: %w", err)
		}

		var blocks []notionBlock
		if err := json.Unmarshal(resp.Results, &blocks); err != nil {
			return nil, fmt.Errorf("unmarshal blocks: %w", err)
		}

		allBlocks = append(allBlocks, blocks...)

		if len(allBlocks) >= maxBlocksPerPage || !resp.HasMore || resp.NextCursor == "" {
			break
		}
		startCursor = resp.NextCursor
	}

	if depth >= maxBlockDepth {
		return allBlocks, nil
	}

	for i := range allBlocks {
		if !allBlocks[i].HasChildren {
			continue
		}
		// Skip block types that don't contribute useful content
		switch allBlocks[i].Type {
		case "child_page", "child_database", "unsupported", "template", "breadcrumb", "table_of_contents":
			continue
		}
		children, err := c.getBlockChildrenRecursive(ctx, allBlocks[i].ID, depth+1)
		if err != nil {
			logger.Warnf(ctx, "[Notion] failed to get children for block %s (depth %d): %v", allBlocks[i].ID, depth, err)
			continue
		}
		allBlocks[i].Children = children
	}

	return allBlocks, nil
}

// QueryDatabaseAll retrieves all records from a database via POST /v1/data_sources/{id}/query.
// Accepts either a data_source_id (from search) or a database_id (from child_database blocks).
// For database_ids, resolves to data_source_id via GET /v1/databases/{id}.
func (c *notionClient) QueryDatabaseAll(ctx context.Context, id string) ([]notionPage, error) {
	// Try as data_source_id directly
	records, err := c.paginatePages(ctx, http.MethodPost, fmt.Sprintf("/v1/data_sources/%s/query", id))
	if err == nil {
		return records, nil
	}

	// If 404, id might be a database container ID — resolve to data_source_id
	info, dbErr := c.GetDatabaseInfo(ctx, id)
	if dbErr != nil {
		return nil, fmt.Errorf("query database %s: not a data_source (%v) and not a database (%v)", id, err, dbErr)
	}
	if info.DataSourceID == "" {
		return nil, fmt.Errorf("database %s has no data sources", id)
	}
	return c.paginatePages(ctx, http.MethodPost, fmt.Sprintf("/v1/data_sources/%s/query", info.DataSourceID))
}

// ResolveBlock re-fetches a single block to resolve file_upload URLs.
// When a block contains a file_upload type, re-fetching it returns the resolved
// download URL (temporary S3 signed URL, 1-hour expiry).
func (c *notionClient) ResolveBlock(ctx context.Context, blockID string) (*notionBlock, error) {
	respBody, err := c.doRequest(ctx, http.MethodGet, "/v1/blocks/"+blockID, nil)
	if err != nil {
		return nil, err
	}
	var block notionBlock
	if err := json.Unmarshal(respBody, &block); err != nil {
		return nil, fmt.Errorf("unmarshal block: %w", err)
	}
	return &block, nil
}

const maxDownloadSize = 100 * 1024 * 1024 // 100MB — prevent OOM from oversized files

// DownloadFile downloads a file from the given URL (typically an S3 signed URL).
// Does not go through the rate limiter since it's not a Notion API call.
func (c *notionClient) DownloadFile(ctx context.Context, fileURL string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fileURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create download request: %w", err)
	}

	var lastErr error
	for attempt := 0; attempt <= maxRetries; attempt++ {
		resp, err := c.httpClient.Do(req)
		if err != nil {
			lastErr = err
			if attempt < maxRetries {
				if sErr := sleepWithContext(ctx, time.Duration(1<<attempt)*time.Second); sErr != nil {
					return nil, sErr
				}
				continue
			}
			break
		}

		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			lastErr = fmt.Errorf("download failed with status %d", resp.StatusCode)
			if resp.StatusCode >= 500 && attempt < maxRetries {
				if sErr := sleepWithContext(ctx, time.Duration(1<<attempt)*time.Second); sErr != nil {
					return nil, sErr
				}
				continue
			}
			break
		}

		data, err := io.ReadAll(io.LimitReader(resp.Body, maxDownloadSize+1))
		resp.Body.Close()
		if err != nil {
			return nil, err
		}
		if int64(len(data)) > maxDownloadSize {
			return nil, fmt.Errorf("file exceeds maximum download size (%d MB)", maxDownloadSize/(1024*1024))
		}
		return data, nil
	}

	return nil, fmt.Errorf("download file: %w", lastErr)
}

// --- Shared pagination helper ---

// paginatePages fetches all pages from a paginated Notion API endpoint.
func (c *notionClient) paginatePages(ctx context.Context, method, path string) ([]notionPage, error) {
	var allPages []notionPage
	var startCursor string

	for {
		body := map[string]interface{}{
			"page_size": 100,
		}
		if startCursor != "" {
			body["start_cursor"] = startCursor
		}

		var respBody []byte
		var err error
		if method == http.MethodPost {
			respBody, err = c.doRequest(ctx, method, path, body)
		} else {
			p := path
			if startCursor != "" {
				p += "?start_cursor=" + startCursor + "&page_size=100"
			}
			respBody, err = c.doRequest(ctx, method, p, nil)
		}
		if err != nil {
			return nil, fmt.Errorf("paginate %s: %w", path, err)
		}

		var resp paginatedResponse
		if err := json.Unmarshal(respBody, &resp); err != nil {
			return nil, fmt.Errorf("unmarshal paginated response: %w", err)
		}

		var pages []notionPage
		if err := json.Unmarshal(resp.Results, &pages); err != nil {
			return nil, fmt.Errorf("unmarshal page results: %w", err)
		}

		for i := range pages {
			pages[i].Title = extractTitle(&pages[i])
		}

		allPages = append(allPages, pages...)

		if !resp.HasMore || resp.NextCursor == "" {
			break
		}
		startCursor = resp.NextCursor
	}

	return allPages, nil
}

// --- Title extraction helpers ---

func extractTitle(page *notionPage) string {
	// Try extracting from properties (works for pages)
	if page.RawProperties != nil {
		var props map[string]json.RawMessage
		if err := json.Unmarshal(page.RawProperties, &props); err == nil {
			for _, propRaw := range props {
				var prop struct {
					Type  string `json:"type"`
					Title []struct {
						PlainText string `json:"plain_text"`
					} `json:"title"`
				}
				if err := json.Unmarshal(propRaw, &prop); err != nil {
					continue
				}
				if prop.Type == "title" && len(prop.Title) > 0 {
					return joinPlainText(prop.Title)
				}
			}
		}
	}

	// Fallback: top-level title array (works for databases)
	if page.RawTitle != nil {
		var titleSegments []struct {
			PlainText string `json:"plain_text"`
		}
		if err := json.Unmarshal(page.RawTitle, &titleSegments); err == nil && len(titleSegments) > 0 {
			return joinPlainText(titleSegments)
		}
	}

	return ""
}

func joinPlainText(segments []struct {
	PlainText string `json:"plain_text"`
}) string {
	var sb strings.Builder
	for _, s := range segments {
		sb.WriteString(s.PlainText)
	}
	return sb.String()
}
