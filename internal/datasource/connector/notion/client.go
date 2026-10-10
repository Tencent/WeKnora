package notion

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"golang.org/x/time/rate"

	"github.com/Tencent/WeKnora/internal/datasource"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/utils"
)

// notionClient wraps the Notion API with rate limiting and retry logic.
type notionClient struct {
	token      string
	httpClient *http.Client
	limiter    *rate.Limiter
	baseURL    string
	// jsonLimit caps each API response body; a field so tests can lower it
	// without materialising the production limit.
	jsonLimit int64
}

// maxJSONResponseBytes bounds every API response body. Page and block payloads
// are small; a larger body means a broken or hostile server.
const maxJSONResponseBytes = 16 << 20

// errResponseTooLarge marks a body over the cap. It is deterministic: the same
// request returns the same oversized body, so callers must not retry it.
var errResponseTooLarge = errors.New("response exceeds maximum size")

// readCapped reads a response body, refusing anything larger than limit instead
// of buffering it. Oversized payloads are reported as an error: a truncated
// body would be indexed as if it were the whole document. A non-positive limit
// (e.g. a zero-value client built in tests) falls back to maxJSONResponseBytes.
func readCapped(body io.Reader, limit int64) ([]byte, error) {
	if limit <= 0 {
		limit = maxJSONResponseBytes
	}
	data, err := io.ReadAll(io.LimitReader(body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("%w (%d bytes)", errResponseTooLarge, limit)
	}
	return data, nil
}

// newClient creates a new Notion API client.
func newClient(token, baseURL string) (*notionClient, error) {
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	if err := datasource.ValidateConnectorBaseURL(baseURL); err != nil {
		return nil, err
	}
	return &notionClient{
		token:      token,
		httpClient: datasource.NewConnectorHTTPClient(30 * time.Second),
		limiter:    rate.NewLimiter(rate.Limit(3), 3),
		baseURL:    baseURL,
		jsonLimit:  maxJSONResponseBytes,
	}, nil
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

		respBody, err := readCapped(resp.Body, c.jsonLimit)
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
	seenCursors := make(map[string]struct{})

	for page := 1; ; page++ {
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
		next, err := advancePaginationCursor(ctx, seenCursors, resp.NextCursor, page)
		if err != nil {
			return nil, fmt.Errorf("get block children for %s: %w", blockID, err)
		}
		startCursor = next
	}

	return allBlocks, nil
}

const maxBlockDepth = 5       // Limit recursion depth — deeper content has diminishing value for knowledge bases
const maxBlocksPerPage = 1000 // Limit total blocks fetched per page to prevent runaway API calls

// blocksTruncated reports whether the maxBlocksPerPage cap stopped pagination
// while the API still offered another page — i.e. whether content was actually
// dropped. Hitting the cap on the last page of a document is not truncation,
// so the caller can warn without a false positive.
//
// hasMore alone decides that: the Notion contract only clears it together with
// next_cursor, so requiring a non-empty cursor here would let the anomalous
// "has_more=true, next_cursor empty" response drop content in silence. That
// anomaly is reported separately by the caller.
func blocksTruncated(currentCount int, hasMore bool) bool {
	return currentCount >= maxBlocksPerPage && hasMore
}

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

		// The cap below stops pagination unconditionally; without this warning
		// the dropped blocks (and any child_page/child_database they contain,
		// which the connector layer never visits) vanish silently.
		if blocksTruncated(len(allBlocks), resp.HasMore) {
			logger.Warnf(ctx, "[Notion] block %s exceeded %d blocks; truncating, remaining blocks are not synced",
				blockID, maxBlocksPerPage)
		}

		// has_more promises another page, but an empty next_cursor means there is
		// no way to ask for it: pagination stops right here even below the cap,
		// so everything after this page would be dropped without a trace.
		if resp.HasMore && resp.NextCursor == "" {
			logger.Warnf(ctx, "[Notion] block %s returned has_more=true without a next_cursor after %d blocks; "+
				"cannot fetch further pages, remaining blocks are not synced", blockID, len(allBlocks))
		}

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
// The record set is read through created_time windows, so a data source holding
// more rows than one vendor query can return is still read in full.
func (c *notionClient) QueryDatabaseAll(ctx context.Context, id string) ([]notionPage, error) {
	// Try as data_source_id directly
	records, err := c.queryDataSourceAll(ctx, fmt.Sprintf("/v1/data_sources/%s/query", id))
	if err == nil {
		return records, nil
	}

	// An incomplete walk reached the data source successfully. Resolving the ID as
	// a database container would only repeat the same capped query and would bury
	// the truncation error behind a misleading lookup failure, so surface it here
	// together with the rows fetched so far.
	if errors.Is(err, errQueryResultTruncated) {
		return records, err
	}

	// If 404, id might be a database container ID — resolve to data_source_id
	info, dbErr := c.GetDatabaseInfo(ctx, id)
	if dbErr != nil {
		return nil, fmt.Errorf("query database %s: not a data_source (%v) and not a database (%v)", id, err, dbErr)
	}
	if info.DataSourceID == "" {
		return nil, fmt.Errorf("database %s has no data sources", id)
	}
	return c.queryDataSourceAll(ctx, fmt.Sprintf("/v1/data_sources/%s/query", info.DataSourceID))
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
	if err := utils.ValidateURLForSSRF(fileURL); err != nil {
		return nil, fmt.Errorf("attachment URL rejected: %w", err)
	}
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

// maxPaginationHops bounds every cursor-paginated loop in this file. A vendor
// (or gateway) that keeps answering has_more=true would otherwise keep the
// client paging until the sync task hits its deadline; the value mirrors the
// guard Confluence and DingTalk already carry.
const maxPaginationHops = 10000

// advancePaginationCursor validates the progress of a cursor-paginated loop
// after page `page` (1-based) has been fetched: a cursor handed back twice
// means the vendor is repeating a page, and page >= maxPaginationHops means
// the listing is unbounded. Both are reported instead of being followed
// forever, and the hop cap is also logged because it is the one failure that
// looks like a healthy, still-running sync from the outside.
func advancePaginationCursor(
	ctx context.Context, seen map[string]struct{}, cursor string, page int,
) (string, error) {
	if page >= maxPaginationHops {
		logger.Warnf(ctx, "[Notion] pagination exceeded %d pages; aborting", maxPaginationHops)
		return "", fmt.Errorf("pagination exceeded %d pages", maxPaginationHops)
	}
	if _, exists := seen[cursor]; exists {
		return "", fmt.Errorf("pagination repeated next_cursor %q", cursor)
	}
	seen[cursor] = struct{}{}
	return cursor, nil
}

// errQueryResultTruncated reports that a paginated response was cut short by a
// vendor-side limit, so the rows collected so far are a prefix of the result set
// rather than all of it. Notion signals this with has_more=false plus
// request_status.type="incomplete" (for a data source query at the 10,000-row
// limit: incomplete_reason="query_result_limit_reached"), which without this
// check is indistinguishable from a complete result set. Callers must not treat
// rows missing from such a result as deleted at source.
var errQueryResultTruncated = errors.New("notion paginated response incomplete: vendor limit reached")

// paginatePages fetches all pages from a cursor-paginated Notion API endpoint
// (POST /v1/search). Endpoints that can return more rows than a single query
// allows — data source queries — go through queryDataSourceAll instead, which
// splits the walk into created_time windows when the vendor caps a query.
func (c *notionClient) paginatePages(ctx context.Context, method, path string) ([]notionPage, error) {
	var allPages []notionPage
	var startCursor string
	seenCursors := make(map[string]struct{})

	for page := 1; ; page++ {
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

		// The vendor marks a capped page as incomplete while still reporting
		// has_more=false, so this per-page check is the only way to tell "cut
		// short" from "read to the end". Keep the rows this page did return, then
		// surface the sentinel so callers can use the partial data without reading
		// absence from it as a source-side deletion.
		if resp.isIncomplete() {
			return allPages, fmt.Errorf("%w: %s (path %s, %d records fetched)",
				errQueryResultTruncated, resp.RequestStatus.IncompleteReason, path, len(allPages))
		}

		if !resp.HasMore || resp.NextCursor == "" {
			break
		}
		next, err := advancePaginationCursor(ctx, seenCursors, resp.NextCursor, page)
		if err != nil {
			return nil, fmt.Errorf("paginate %s: %w", path, err)
		}
		startCursor = next
	}

	return allPages, nil
}

// --- Data source query: read past the per-query result limit ---

// dataSourceQueryPageSize is the page size requested for every data source query page.
const dataSourceQueryPageSize = 100

// errWindowNotAdvancing reports that an incomplete window could not be followed
// by a later one: no returned row carried a created_time, or every row in the
// window shares the timestamp the window started at. Both mean the remaining
// rows cannot be reached by moving the created_time boundary, so the walk stops
// instead of re-querying the same window forever.
var errWindowNotAdvancing = errors.New("created_time window cannot advance")

// queryDataSourceAll reads every row of a data source query, splitting the walk
// into created_time windows whenever the vendor caps a query at its per-query
// result limit.
//
// A capped query answers has_more=false together with
// request_status.type="incomplete", so a caller that only follows next_cursor
// stops at the limit without noticing. The vendor's recipe for reading past it is
// to sort by created_time ascending and, each time a window comes back
// incomplete, to re-query with filter.created_time.on_or_after set to the
// created_time of the last row seen; rows sitting exactly on that boundary are
// returned by both windows and are de-duplicated here by row ID. created_time is
// used deliberately instead of last_edited_time: it never changes, so a window
// boundary stays valid while the walk is in progress, whereas last_edited_time
// moves rows between windows as they are edited.
// See https://developers.notion.com/guides/data-apis/query-large-data-sources
//
// When a window cannot advance, the walk stops and the rows collected so far are
// returned together with errQueryResultTruncated, so callers still learn that the
// result set is incomplete instead of reading absence from it as deletion.
func (c *notionClient) queryDataSourceAll(ctx context.Context, path string) ([]notionPage, error) {
	var allRows []notionPage
	seen := make(map[string]bool, dataSourceQueryPageSize)
	var windowStart *time.Time

	for {
		rows, lastCreatedTime, incompleteReason, err := c.queryDataSourceWindow(ctx, path, windowStart)
		if err != nil {
			return nil, err
		}
		for i := range rows {
			if id := rows[i].ID; id != "" {
				if seen[id] {
					continue
				}
				seen[id] = true
			}
			allRows = append(allRows, rows[i])
		}

		if incompleteReason == "" {
			return allRows, nil
		}

		// The vendor capped this window, so the rows past the limit are only
		// reachable from a later window. The next window must start strictly after
		// the current one, otherwise it would return the same rows again.
		if lastCreatedTime.IsZero() {
			logger.Warnf(ctx, "[Notion] query %s is incomplete (%s) but returned no row carrying created_time; "+
				"stopping with %d records instead of re-querying the same window",
				path, incompleteReason, len(allRows))
			return allRows, fmt.Errorf("%w: %s: %w: %s returned an incomplete window "+
				"without any created_time (%d records fetched)",
				errQueryResultTruncated, incompleteReason, errWindowNotAdvancing, path, len(allRows))
		}
		if windowStart != nil && !lastCreatedTime.After(*windowStart) {
			logger.Warnf(ctx, "[Notion] query %s is still incomplete (%s) at created_time %s, which does not advance "+
				"the window start; stopping with %d records: more rows than the vendor per-query limit share "+
				"one created_time", path, incompleteReason,
				lastCreatedTime.UTC().Format(time.RFC3339Nano), len(allRows))
			return allRows, fmt.Errorf("%w: %s: %w: %s did not advance past created_time %s (%d records fetched)",
				errQueryResultTruncated, incompleteReason, errWindowNotAdvancing, path,
				lastCreatedTime.UTC().Format(time.RFC3339Nano), len(allRows))
		}

		// Keep the truncation visible: the first window hit the vendor limit, and
		// the log line records where the next window starts.
		logger.Warnf(ctx, "[Notion] query %s hit the vendor per-query result limit (%s); "+
			"continuing from created_time %s (%d records fetched so far)", path, incompleteReason,
			lastCreatedTime.UTC().Format(time.RFC3339Nano), len(allRows))
		start := lastCreatedTime
		windowStart = &start
	}
}

// queryDataSourceWindow drains one created_time window: it follows next_cursor
// until the window is exhausted, and reports the created_time of the last row it
// saw plus the vendor's incomplete_reason when any page of the window was marked
// incomplete (an empty reason means the window was read to the end).
// A nil windowStart reads the first, unfiltered window.
func (c *notionClient) queryDataSourceWindow(
	ctx context.Context, path string, windowStart *time.Time,
) (rows []notionPage, lastCreatedTime time.Time, incompleteReason string, err error) {
	var startCursor string
	seenCursors := make(map[string]struct{})

	for page := 1; ; page++ {
		body := map[string]interface{}{
			"page_size": dataSourceQueryPageSize,
			"sorts": []map[string]string{
				{"timestamp": "created_time", "direction": "ascending"},
			},
		}
		if windowStart != nil {
			body["filter"] = map[string]interface{}{
				"timestamp": "created_time",
				"created_time": map[string]string{
					"on_or_after": windowStart.UTC().Format(time.RFC3339Nano),
				},
			}
		}
		if startCursor != "" {
			body["start_cursor"] = startCursor
		}

		respBody, doErr := c.doRequest(ctx, http.MethodPost, path, body)
		if doErr != nil {
			return nil, time.Time{}, "", fmt.Errorf("paginate %s: %w", path, doErr)
		}

		var resp paginatedResponse
		if unmarshalErr := json.Unmarshal(respBody, &resp); unmarshalErr != nil {
			return nil, time.Time{}, "", fmt.Errorf("unmarshal paginated response: %w", unmarshalErr)
		}

		var pageRows []notionPage
		if unmarshalErr := json.Unmarshal(resp.Results, &pageRows); unmarshalErr != nil {
			return nil, time.Time{}, "", fmt.Errorf("unmarshal page results: %w", unmarshalErr)
		}
		for i := range pageRows {
			pageRows[i].Title = extractTitle(&pageRows[i])
			// Rows arrive sorted by created_time ascending, so the last row
			// carrying a timestamp is this window's boundary.
			if !pageRows[i].CreatedTime.IsZero() {
				lastCreatedTime = pageRows[i].CreatedTime
			}
		}
		rows = append(rows, pageRows...)

		// The marker is latched for the whole window: it can appear on any page
		// of a capped query, including before the last page received.
		if resp.isIncomplete() {
			incompleteReason = resp.RequestStatus.IncompleteReason
			if incompleteReason == "" {
				incompleteReason = "incomplete"
			}
		}

		if !resp.HasMore || resp.NextCursor == "" {
			return rows, lastCreatedTime, incompleteReason, nil
		}
		next, cursorErr := advancePaginationCursor(ctx, seenCursors, resp.NextCursor, page)
		if cursorErr != nil {
			return nil, time.Time{}, "", fmt.Errorf("paginate %s: %w", path, cursorErr)
		}
		startCursor = next
	}
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
