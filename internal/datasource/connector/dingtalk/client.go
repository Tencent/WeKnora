package dingtalk

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Tencent/WeKnora/internal/datasource"
)

const (
	defaultTimeout        = 30 * time.Second
	workspaceMaxResults   = 30
	nodeMaxResults        = 50
	documentBlockPageSize = 100
	maxResponseBytes      = 16 << 20 // 16 MiB
	maxRetryAfter         = 30 * time.Second
	maxAttempts           = 4
	maxPaginationPages    = 1000
	maxWorkspaceItems     = 10000
	maxNodesPerParent     = 50000
	maxDocumentBlocks     = 100000
	maxAggregatePageBytes = 64 << 20 // 64 MiB across decoded pagination pages
	maxTokenCacheEntries  = 256
	userAgent             = "WeKnora-DingTalk-Connector/1.0"
)

// client wraps the DingTalk Open API.
type client struct {
	cfg        *config
	httpClient *http.Client
}

// newClient constructs a client with SSRF-safe HTTP transport.
func newClient(cfg *config) (*client, error) {
	httpClient := datasource.NewConnectorHTTPClient(defaultTimeout)
	applyDingTalkRedirectPolicy(httpClient)
	return &client{
		cfg:        cfg,
		httpClient: httpClient,
	}, nil
}

// applyDingTalkRedirectPolicy prevents credential-bearing DingTalk requests
// from crossing origins. The shared connector client already strips sensitive
// headers and validates redirect targets, but an HTTP 307/308 can also replay
// the OAuth request body containing appSecret. Returning ErrUseLastResponse
// makes the caller handle the 3xx as an upstream error without contacting the
// redirect target.
func applyDingTalkRedirectPolicy(httpClient *http.Client) {
	basePolicy := httpClient.CheckRedirect
	httpClient.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) > 0 &&
			(!strings.EqualFold(req.URL.Scheme, via[0].URL.Scheme) ||
				!strings.EqualFold(req.URL.Host, via[0].URL.Host)) {
			return http.ErrUseLastResponse
		}
		if basePolicy != nil {
			return basePolicy(req, via)
		}
		if len(via) >= 10 {
			return fmt.Errorf("stopped after 10 redirects")
		}
		return nil
	}
}

// tokenCacheEntry holds a cached access token and its expiry.
type tokenCacheEntry struct {
	token     string
	expiresAt time.Time
	lastUsed  time.Time
	mu        sync.Mutex
	inflight  chan struct{}
}

var (
	tokenCache   = make(map[string]*tokenCacheEntry)
	tokenCacheMu sync.Mutex
)

func getOrCreateTokenCacheEntry(cacheKey string) (*tokenCacheEntry, bool) {
	now := time.Now()
	tokenCacheMu.Lock()
	defer tokenCacheMu.Unlock()

	if entry, ok := tokenCache[cacheKey]; ok {
		return entry, true
	}

	// Preview/validation accepts candidate credentials, so an administrator
	// can legitimately rotate through many cache keys. Opportunistically
	// remove failed/expired entries and cap even still-valid idle entries to
	// keep that process-global cache bounded.
	for key, entry := range tokenCache {
		entry.mu.Lock()
		expired := entry.inflight == nil &&
			(entry.token == "" || !now.Before(entry.expiresAt))
		entry.mu.Unlock()
		if expired {
			delete(tokenCache, key)
		}
	}
	for len(tokenCache) >= maxTokenCacheEntries {
		var oldestKey string
		var oldestUsed time.Time
		for key, entry := range tokenCache {
			entry.mu.Lock()
			idle := entry.inflight == nil
			lastUsed := entry.lastUsed
			entry.mu.Unlock()
			if idle && (oldestKey == "" || lastUsed.Before(oldestUsed)) {
				oldestKey = key
				oldestUsed = lastUsed
			}
		}
		if oldestKey == "" {
			// Every retained entry is refreshing. Use an ephemeral entry for
			// this request rather than exceeding the hard cache bound.
			return &tokenCacheEntry{lastUsed: now}, false
		}
		delete(tokenCache, oldestKey)
	}

	entry := &tokenCacheEntry{lastUsed: now}
	tokenCache[cacheKey] = entry
	return entry, true
}

func deleteTokenCacheEntryIfIdle(cacheKey string, target *tokenCacheEntry) {
	tokenCacheMu.Lock()
	defer tokenCacheMu.Unlock()

	entry, ok := tokenCache[cacheKey]
	if !ok || entry != target {
		return
	}
	entry.mu.Lock()
	defer entry.mu.Unlock()
	if entry.inflight == nil &&
		(entry.token == "" || !time.Now().Before(entry.expiresAt)) {
		delete(tokenCache, cacheKey)
	}
}

// getToken fetches an access token, coalescing concurrent requests for the same
// tenant+datasource into a single upstream call (D3: per-tenant cache key prevents
// cross-tenant token sharing).
func (c *client) getToken(ctx context.Context) (string, error) {
	cacheKey := c.cfg.tokenCacheKey()
	entry, retained := getOrCreateTokenCacheEntry(cacheKey)

	entry.mu.Lock()
	for entry.inflight != nil {
		done := entry.inflight
		entry.mu.Unlock()
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-done:
		}
		entry.mu.Lock()
		if time.Now().Before(entry.expiresAt) {
			tok := entry.token
			entry.lastUsed = time.Now()
			entry.mu.Unlock()
			return tok, nil
		}
	}

	if time.Now().Before(entry.expiresAt) {
		tok := entry.token
		entry.lastUsed = time.Now()
		entry.mu.Unlock()
		return tok, nil
	}

	done := make(chan struct{})
	entry.inflight = done
	entry.mu.Unlock()

	refreshSucceeded := false
	defer func() {
		entry.mu.Lock()
		entry.inflight = nil
		close(done)
		entry.mu.Unlock()
		if retained && !refreshSucceeded {
			deleteTokenCacheEntryIfIdle(cacheKey, entry)
		}
	}()

	reqBody := map[string]string{
		"appKey":    c.cfg.AppKey,
		"appSecret": c.cfg.AppSecret,
	}
	body, err := json.Marshal(reqBody)
	if err != nil {
		return "", err
	}

	var tokenResp tokenResponse
	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		req, err := http.NewRequestWithContext(
			ctx,
			"POST",
			c.cfg.BaseURL+"/v1.0/oauth2/accessToken",
			bytes.NewReader(body),
		)
		if err != nil {
			return "", c.sanitizeError(err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("User-Agent", userAgent)

		resp, err := c.httpClient.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				return "", ctx.Err()
			}
			lastErr = fmt.Errorf("token request: %w", c.sanitizeError(err))
			if attempt == maxAttempts {
				break
			}
			if err := waitForRetry(ctx, 0, attempt); err != nil {
				return "", err
			}
			continue
		}

		limited := io.LimitReader(resp.Body, maxResponseBytes+1)
		data, readErr := io.ReadAll(limited)
		resp.Body.Close()
		if readErr != nil {
			lastErr = c.sanitizeError(readErr)
			if attempt == maxAttempts {
				break
			}
			if err := waitForRetry(ctx, 0, attempt); err != nil {
				return "", err
			}
			continue
		}
		if int64(len(data)) > maxResponseBytes {
			return "", fmt.Errorf("token response body exceeds %d bytes", maxResponseBytes)
		}

		if resp.StatusCode == http.StatusOK {
			if err := json.Unmarshal(data, &tokenResp); err != nil {
				return "", fmt.Errorf("parse token response: %w", err)
			}
			if tokenResp.AccessToken == "" || tokenResp.ExpireIn <= 0 {
				return "", fmt.Errorf("DingTalk token response is missing a token or valid lifetime")
			}
			lastErr = nil
			break
		}

		var apiErr apiError
		_ = json.Unmarshal(data, &apiErr)
		apiErr.StatusCode = resp.StatusCode
		c.sanitizeAPIError(&apiErr)
		if resp.StatusCode == http.StatusBadRequest ||
			resp.StatusCode == http.StatusUnauthorized ||
			resp.StatusCode == http.StatusForbidden {
			return "", fmt.Errorf("%w: %v", datasource.ErrInvalidCredentials, &apiErr)
		}
		if resp.StatusCode != http.StatusTooManyRequests && resp.StatusCode < 500 {
			return "", &apiErr
		}

		lastErr = &apiErr
		if attempt == maxAttempts {
			break
		}
		retryAfterSec, _ := strconv.Atoi(resp.Header.Get("Retry-After"))
		if err := waitForRetry(
			ctx,
			time.Duration(retryAfterSec)*time.Second,
			attempt,
		); err != nil {
			return "", err
		}
	}
	if lastErr != nil {
		return "", lastErr
	}
	refreshAfter := time.Duration(tokenResp.ExpireIn) * time.Second
	const tokenRefreshMargin = 60 * time.Second
	if refreshAfter > tokenRefreshMargin {
		refreshAfter -= tokenRefreshMargin
	} else {
		refreshAfter /= 2
	}

	entry.mu.Lock()
	entry.token = tokenResp.AccessToken
	entry.expiresAt = time.Now().Add(refreshAfter)
	entry.lastUsed = time.Now()
	entry.mu.Unlock()
	refreshSucceeded = true

	return tokenResp.AccessToken, nil
}

// authorizedGet executes a GET request with access token, honoring 401 (refresh
// token once) and 429/5xx (retry with backoff). D4: errors never leak tokens,
// secrets, or the operator UnionID.
func (c *client) authorizedGet(
	ctx context.Context,
	path string,
	query url.Values,
	result interface{},
	privatePathValues ...string,
) error {
	if query == nil {
		query = url.Values{}
	}
	query.Set("operatorId", c.cfg.OperatorID)

	token, err := c.getToken(ctx)
	if err != nil {
		return err
	}

	attempt := 0
	refreshedAfterUnauthorized := false
	var lastErr error

	for attempt < maxAttempts {
		redactions := append([]string{token, path}, privatePathValues...)
		for key, values := range query {
			// maxResults is a public protocol constant. Every other query value
			// can identify a workspace, node, operator, or pagination position.
			if key == "maxResults" {
				continue
			}
			redactions = append(redactions, values...)
		}
		fullURL := c.cfg.BaseURL + path + "?" + query.Encode()
		req, err := http.NewRequestWithContext(ctx, "GET", fullURL, nil)
		if err != nil {
			return c.sanitizeError(err, redactions...)
		}
		req.Header.Set("x-acs-dingtalk-access-token", token)
		req.Header.Set("User-Agent", userAgent)

		resp, err := c.httpClient.Do(req)
		if err != nil {
			return c.sanitizeError(err, redactions...)
		}

		limited := io.LimitReader(resp.Body, maxResponseBytes+1)
		data, err := io.ReadAll(limited)
		resp.Body.Close()
		if err != nil {
			return c.sanitizeError(err, redactions...)
		}
		if int64(len(data)) > maxResponseBytes {
			return fmt.Errorf("response body exceeds %d bytes", maxResponseBytes)
		}

		if resp.StatusCode == http.StatusOK {
			if err := json.Unmarshal(data, result); err != nil {
				return fmt.Errorf("parse response: %w", err)
			}
			return nil
		}

		var apiErr apiError
		_ = json.Unmarshal(data, &apiErr)
		apiErr.StatusCode = resp.StatusCode
		c.sanitizeAPIError(&apiErr, redactions...)

		if resp.StatusCode == http.StatusUnauthorized {
			if !refreshedAfterUnauthorized {
				refreshedAfterUnauthorized = true
				tokenCacheMu.Lock()
				cacheKey := c.cfg.tokenCacheKey()
				delete(tokenCache, cacheKey)
				tokenCacheMu.Unlock()
				token, err = c.getToken(ctx)
				if err != nil {
					return err
				}
				attempt++
				continue
			}
			return datasource.ErrInvalidCredentials
		}

		if resp.StatusCode == http.StatusForbidden {
			return &apiErr
		}

		if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
			attempt++
			if attempt >= maxAttempts {
				lastErr = &apiErr
				break
			}
			retryAfterSec, _ := strconv.Atoi(resp.Header.Get("Retry-After"))
			delay := retryDelay(time.Duration(retryAfterSec)*time.Second, attempt)
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(delay):
			}
			continue
		}

		return &apiErr
	}

	return lastErr
}

// retryDelay computes exponential backoff, capping hostile Retry-After values.
func retryDelay(retryAfter time.Duration, attempt int) time.Duration {
	if retryAfter > maxRetryAfter {
		retryAfter = maxRetryAfter
	}
	if retryAfter > 0 {
		return retryAfter
	}
	base := time.Duration(1<<uint(attempt-1)) * time.Second
	if base > maxRetryAfter {
		base = maxRetryAfter
	}
	return base
}

func waitForRetry(ctx context.Context, retryAfter time.Duration, attempt int) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(retryDelay(retryAfter, attempt)):
		return nil
	}
}

var absoluteURLPattern = regexp.MustCompile(`https?://[^\s"<>]+`)

// sanitizeError strips runtime credentials, personal identifiers, and complete
// URLs from errors. Transport errors routinely embed the request URL, including
// operatorId and other query parameters, so sanitization must use the current
// client values rather than a fixture-specific literal.
func (c *client) sanitizeError(err error, additional ...string) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	values := []string{
		c.cfg.AppKey,
		c.cfg.AppSecret,
		c.cfg.OperatorID,
		c.cfg.BaseURL,
	}
	values = append(values, additional...)
	for _, value := range values {
		if value == "" {
			continue
		}
		for _, variant := range []string{value, url.QueryEscape(value), url.PathEscape(value)} {
			if variant != "" {
				msg = strings.ReplaceAll(msg, variant, "[REDACTED]")
			}
		}
	}
	msg = absoluteURLPattern.ReplaceAllString(msg, "[REDACTED_URL]")
	return fmt.Errorf("%s", msg)
}

func (c *client) sanitizeAPIError(apiErr *apiError, additional ...string) {
	if apiErr == nil {
		return
	}
	if apiErr.Code != "" {
		apiErr.Code = c.sanitizeError(fmt.Errorf("%s", apiErr.Code), additional...).Error()
	}
	if apiErr.Message != "" {
		apiErr.Message = c.sanitizeError(fmt.Errorf("%s", apiErr.Message), additional...).Error()
	}
	if apiErr.RequestID != "" {
		apiErr.RequestID = c.sanitizeError(fmt.Errorf("%s", apiErr.RequestID), additional...).Error()
	}
}

// appendBoundedPage validates both the upstream page contract and the aggregate
// memory budget before retaining another decoded page. maxResponseBytes bounds a
// single response body; these cumulative limits prevent many individually valid
// pages (or a server returning more items than requested) from growing memory
// without bound.
func appendBoundedPage[T any](
	all []T,
	page []T,
	pageLimit int,
	totalItemLimit int,
	aggregateBytes *int,
	label string,
) ([]T, error) {
	if len(page) > pageLimit {
		return nil, fmt.Errorf(
			"DingTalk %s response exceeded requested page size %d",
			label,
			pageLimit,
		)
	}
	if len(page) > totalItemLimit || len(all) > totalItemLimit-len(page) {
		return nil, fmt.Errorf(
			"DingTalk %s pagination item limit %d exceeded",
			label,
			totalItemLimit,
		)
	}
	encoded, err := json.Marshal(page)
	if err != nil {
		return nil, fmt.Errorf("encode DingTalk %s page for size accounting: %w", label, err)
	}
	if len(encoded) > maxAggregatePageBytes ||
		*aggregateBytes > maxAggregatePageBytes-len(encoded) {
		return nil, fmt.Errorf(
			"DingTalk %s pagination aggregate size limit %d exceeded",
			label,
			maxAggregatePageBytes,
		)
	}
	*aggregateBytes += len(encoded)
	return append(all, page...), nil
}

// listWorkspaces fetches a single page of wiki workspaces.
func (c *client) listWorkspaces(ctx context.Context, nextToken string) (*workspacesPage, error) {
	q := url.Values{}
	q.Set("maxResults", strconv.Itoa(workspaceMaxResults))
	if nextToken != "" {
		q.Set("nextToken", nextToken)
	}
	var page workspacesPage
	if err := c.authorizedGet(ctx, "/v2.0/wiki/workspaces", q, &page); err != nil {
		return nil, err
	}
	return &page, nil
}

// listAllWorkspaces fetches all wiki workspaces, following pagination.
func (c *client) listAllWorkspaces(ctx context.Context) ([]workspace, error) {
	var all []workspace
	aggregateBytes := 0
	nextToken := ""
	seen := make(map[string]bool)
	for pageNumber := 0; ; pageNumber++ {
		if pageNumber >= maxPaginationPages {
			return nil, fmt.Errorf("workspace pagination page limit %d exceeded", maxPaginationPages)
		}
		page, err := c.listWorkspaces(ctx, nextToken)
		if err != nil {
			return nil, err
		}
		all, err = appendBoundedPage(
			all,
			page.Workspaces,
			workspaceMaxResults,
			maxWorkspaceItems,
			&aggregateBytes,
			"workspace",
		)
		if err != nil {
			return nil, err
		}
		if page.NextToken == "" {
			break
		}
		if seen[page.NextToken] {
			return nil, fmt.Errorf("pagination loop detected")
		}
		seen[page.NextToken] = true
		nextToken = page.NextToken
	}
	return all, nil
}

// listChildren fetches a single page of child nodes under a parent.
func (c *client) listChildren(ctx context.Context, workspaceID, parentNodeID, nextToken string) (*nodesPage, error) {
	q := url.Values{}
	q.Set("parentNodeId", parentNodeID)
	q.Set("maxResults", strconv.Itoa(nodeMaxResults))
	if nextToken != "" {
		q.Set("nextToken", nextToken)
	}
	var page nodesPage
	if err := c.authorizedGet(ctx, "/v2.0/wiki/nodes", q, &page, workspaceID); err != nil {
		return nil, err
	}
	return &page, nil
}

// listAllChildren fetches all child nodes under a parent, following pagination.
func (c *client) listAllChildren(ctx context.Context, workspaceID, parentNodeID string) ([]node, error) {
	var all []node
	aggregateBytes := 0
	nextToken := ""
	seen := make(map[string]bool)
	for pageNumber := 0; ; pageNumber++ {
		if pageNumber >= maxPaginationPages {
			return nil, fmt.Errorf("node pagination page limit %d exceeded", maxPaginationPages)
		}
		page, err := c.listChildren(ctx, workspaceID, parentNodeID, nextToken)
		if err != nil {
			return nil, err
		}
		all, err = appendBoundedPage(
			all,
			page.Nodes,
			nodeMaxResults,
			maxNodesPerParent,
			&aggregateBytes,
			"node",
		)
		if err != nil {
			return nil, err
		}
		if page.NextToken == "" {
			break
		}
		if seen[page.NextToken] {
			return nil, fmt.Errorf("pagination loop detected")
		}
		seen[page.NextToken] = true
		nextToken = page.NextToken
	}
	return all, nil
}

// getNodeDetail fetches metadata for a single node.
func (c *client) getNodeDetail(ctx context.Context, nodeID string) (*node, error) {
	var detail nodeDetail
	if err := c.authorizedGet(
		ctx,
		"/v2.0/wiki/nodes/"+url.PathEscape(nodeID),
		nil,
		&detail,
		nodeID,
	); err != nil {
		return nil, err
	}
	return &detail.Node, nil
}

// listDocumentBlocks fetches all content blocks for a DingTalk online document.
// The blocks endpoint uses inclusive startIndex/endIndex pagination rather than
// nextToken. Omitting these parameters silently returns only the default page.
func (c *client) listDocumentBlocks(ctx context.Context, docKey string) ([]block, error) {
	all := make([]block, 0, documentBlockPageSize)
	aggregateBytes := 0
	path := "/v1.0/doc/suites/documents/" + url.PathEscape(docKey) + "/blocks"
	for pageNumber := 0; pageNumber < maxPaginationPages; pageNumber++ {
		start := pageNumber * documentBlockPageSize
		query := url.Values{}
		query.Set("startIndex", strconv.Itoa(start))
		query.Set("endIndex", strconv.Itoa(start+documentBlockPageSize-1))

		var response blocksResponse
		if err := c.authorizedGet(ctx, path, query, &response, docKey); err != nil {
			return nil, err
		}
		if !response.Success {
			return nil, fmt.Errorf("DingTalk blocks response indicated failure")
		}
		var appendErr error
		all, appendErr = appendBoundedPage(
			all,
			response.Result.Data,
			documentBlockPageSize,
			maxDocumentBlocks,
			&aggregateBytes,
			"blocks",
		)
		if appendErr != nil {
			return nil, appendErr
		}
		if len(response.Result.Data) < documentBlockPageSize {
			return all, nil
		}
	}
	return nil, fmt.Errorf("document block pagination page limit %d exceeded", maxPaginationPages)
}
