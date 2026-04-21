package yuque

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/Tencent/WeKnora/internal/datasource"
	"github.com/Tencent/WeKnora/internal/logger"
)

const (
	defaultTimeout  = 30 * time.Second
	defaultPageSize = 100
	userAgent       = "WeKnora-Yuque-Connector/1.0"
)

// client wraps the Yuque Open API.
type client struct {
	baseURL    string
	token      string
	httpClient *http.Client

	// logTokenOnce ensures the redacted token identity is logged at most once
	// per client lifetime (first real request), rather than on every call.
	logTokenOnce sync.Once
}

// newClient constructs a client with a normalized base URL.
func newClient(cfg *Config) *client {
	return &client{
		baseURL:    cfg.GetBaseURL(),
		token:      cfg.APIToken,
		httpClient: &http.Client{Timeout: defaultTimeout},
	}
}

// doRequest executes an authenticated request and decodes JSON, with retry logic
// for transient errors (429, 5xx, transport failures).
// The raw X-Auth-Token is never logged; only a redacted form is emitted once per
// client lifetime (not per request) to keep sync logs readable at thousand-doc scale.
func (c *client) doRequest(ctx context.Context, method, path string, result interface{}) error {
	const (
		maxRetries    = 3
		max5xxRetries = 1
		retry5xxDelay = 2 * time.Second
	)
	var lastErr error
	backoff := []time.Duration{2 * time.Second, 4 * time.Second, 8 * time.Second}

	c.logTokenOnce.Do(func() {
		logger.Infof(ctx, "[Yuque] client configured token=%s base=%s", redactToken(c.token), c.baseURL)
	})

	for attempt := 0; attempt <= maxRetries; attempt++ {
		reqURL := c.baseURL + path
		req, err := http.NewRequestWithContext(ctx, method, reqURL, nil)
		if err != nil {
			return fmt.Errorf("create request: %w", err)
		}
		req.Header.Set("X-Auth-Token", c.token)
		req.Header.Set("User-Agent", userAgent)
		req.Header.Set("Content-Type", "application/json; charset=utf-8")

		if attempt == 0 {
			logger.Infof(ctx, "[Yuque] %s %s", method, path)
		} else {
			logger.Infof(ctx, "[Yuque] %s %s (retry %d/%d)", method, path, attempt, maxRetries)
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

		body, readErr := io.ReadAll(resp.Body)
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

		bodyPreview := truncate(string(body), 500)
		logger.Infof(ctx, "[Yuque] %s %s → status=%d bodyLen=%d body=%s",
			method, path, resp.StatusCode, len(body), bodyPreview)

		if resp.StatusCode == 429 {
			wait := parseRetryAfter(resp.Header.Get("Retry-After"), backoff[min(attempt, len(backoff)-1)])
			lastErr = fmt.Errorf("yuque rate limited: status=429 body=%s", bodyPreview)
			if attempt < maxRetries {
				if sErr := sleepCtx(ctx, wait); sErr != nil {
					return sErr
				}
				continue
			}
			return lastErr
		}

		if resp.StatusCode >= 500 && resp.StatusCode < 600 {
			lastErr = fmt.Errorf("yuque server error: status=%d body=%s", resp.StatusCode, bodyPreview)
			if attempt < max5xxRetries {
				if sErr := sleepCtx(ctx, retry5xxDelay); sErr != nil {
					return sErr
				}
				continue
			}
			return lastErr
		}

		// 401/403 → surface as ErrInvalidCredentials so DataSourceService can
		// distinguish bad-token from transient failures and auto-flag the source.
		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
			return fmt.Errorf("%w: status=%d body=%s", datasource.ErrInvalidCredentials, resp.StatusCode, bodyPreview)
		}

		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			var apiErr apiErrorBody
			_ = json.Unmarshal(body, &apiErr)
			if apiErr.Message != "" {
				return fmt.Errorf("yuque api error: status=%d msg=%s", resp.StatusCode, apiErr.Message)
			}
			return fmt.Errorf("yuque api error: status=%d body=%s", resp.StatusCode, bodyPreview)
		}

		if result != nil {
			if err := json.Unmarshal(body, result); err != nil {
				return fmt.Errorf("decode response: %w", err)
			}
		}
		return nil
	}
	return lastErr
}

// parseRetryAfter returns the Retry-After duration from the header, or fallback if unparseable.
// Retry-After: "0" (or negative) is coerced to 100ms so we still yield and don't busy-retry.
// Note: only integer-seconds form is supported (RFC 7231 also allows HTTP-date — not seen from Yuque).
func parseRetryAfter(header string, fallback time.Duration) time.Duration {
	if header == "" {
		return fallback
	}
	if secs, err := time.ParseDuration(header + "s"); err == nil {
		if secs <= 0 {
			return 100 * time.Millisecond
		}
		return secs
	}
	return fallback
}

// sleepCtx pauses for d, returning early if ctx is cancelled.
func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Ping verifies the credentials by calling GET /api/v2/user.
func (c *client) Ping(ctx context.Context) error {
	var resp v2UserResponse
	return c.doRequest(ctx, http.MethodGet, "/api/v2/user", &resp)
}

// truncate returns s truncated to maxLen with "..." appended if longer.
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// buildQuery encodes query parameters, omitting empty values.
func buildQuery(params map[string]string) string {
	values := url.Values{}
	for k, v := range params {
		if v != "" {
			values.Set(k, v)
		}
	}
	if len(values) == 0 {
		return ""
	}
	return "?" + values.Encode()
}

// GetCurrentUser returns the user associated with the current token.
func (c *client) GetCurrentUser(ctx context.Context) (v2User, error) {
	var resp v2UserResponse
	if err := c.doRequest(ctx, http.MethodGet, "/api/v2/user", &resp); err != nil {
		return v2User{}, err
	}
	return resp.Data, nil
}

// ListUserGroups returns the groups the given user belongs to.
// Note: userID is Yuque's numeric user ID (not the login) — the /users/{id}/groups
// endpoint requires the integer ID form.
func (c *client) ListUserGroups(ctx context.Context, userID int64) ([]v2Group, error) {
	path := fmt.Sprintf("/api/v2/users/%d/groups", userID)
	var resp v2GroupListResponse
	if err := c.doRequest(ctx, http.MethodGet, path, &resp); err != nil {
		return nil, err
	}
	return resp.Data, nil
}

// ListUserRepos returns document-type (Book) repos owned by the given user login.
func (c *client) ListUserRepos(ctx context.Context, login string) ([]v2Repo, error) {
	return c.listReposPaginated(ctx, fmt.Sprintf("/api/v2/users/%s/repos", login))
}

// ListGroupRepos returns document-type (Book) repos owned by the given group login.
func (c *client) ListGroupRepos(ctx context.Context, login string) ([]v2Repo, error) {
	return c.listReposPaginated(ctx, fmt.Sprintf("/api/v2/groups/%s/repos", login))
}

// listReposPaginated walks the pagination for a repo listing endpoint.
// Filters to type=Book (document-type knowledge bases only; design/sheet/resource skipped).
func (c *client) listReposPaginated(ctx context.Context, basePath string) ([]v2Repo, error) {
	var all []v2Repo
	offset := 0
	for {
		q := buildQuery(map[string]string{
			"type":   "Book",
			"offset": fmt.Sprintf("%d", offset),
			"limit":  fmt.Sprintf("%d", defaultPageSize),
		})
		var resp v2RepoListResponse
		if err := c.doRequest(ctx, http.MethodGet, basePath+q, &resp); err != nil {
			return nil, err
		}
		all = append(all, resp.Data...)
		if len(resp.Data) < defaultPageSize {
			break
		}
		offset += defaultPageSize
	}
	return all, nil
}

// ListBookDocs lists all documents in a book, handling pagination.
// Returns summaries only (body not included — use GetDocDetail to fetch body per doc).
func (c *client) ListBookDocs(ctx context.Context, bookID int64) ([]v2Doc, error) {
	basePath := fmt.Sprintf("/api/v2/repos/%d/docs", bookID)
	var all []v2Doc
	offset := 0
	for {
		q := buildQuery(map[string]string{
			"offset": fmt.Sprintf("%d", offset),
			"limit":  fmt.Sprintf("%d", defaultPageSize),
		})
		var resp v2DocListResponse
		if err := c.doRequest(ctx, http.MethodGet, basePath+q, &resp); err != nil {
			return nil, err
		}
		all = append(all, resp.Data...)
		if len(resp.Data) < defaultPageSize {
			break
		}
		offset += defaultPageSize
	}
	return all, nil
}

// GetDocDetail fetches the full document detail (including body) by doc ID.
func (c *client) GetDocDetail(ctx context.Context, docID int64) (v2DocDetail, error) {
	path := fmt.Sprintf("/api/v2/repos/docs/%d", docID)
	var resp v2DocDetailResponse
	if err := c.doRequest(ctx, http.MethodGet, path, &resp); err != nil {
		return v2DocDetail{}, err
	}
	return resp.Data, nil
}
