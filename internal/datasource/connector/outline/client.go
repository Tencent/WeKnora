package outline

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/Tencent/WeKnora/internal/datasource"
	"github.com/Tencent/WeKnora/internal/logger"
)

const (
	defaultTimeout = 30 * time.Second
	// attachmentTimeout is longer than defaultTimeout: an attachment download
	// moves image bytes, not a small JSON body.
	attachmentTimeout = 60 * time.Second
	defaultPageSize   = 100
	userAgent         = "WeKnora-Outline-Connector/1.0"
)

// client wraps the Outline API.
type client struct {
	baseURL    string
	token      string
	httpClient *http.Client
	fileClient *http.Client

	// logTokenOnce keeps the redacted token identity to one line per client
	// lifetime instead of one per request.
	logTokenOnce sync.Once
}

func newClient(cfg *Config) *client {
	return &client{
		baseURL:    cfg.GetBaseURL(),
		token:      cfg.APIToken,
		httpClient: datasource.NewConnectorHTTPClient(defaultTimeout),
		fileClient: datasource.NewConnectorHTTPClient(attachmentTimeout),
	}
}

// doRequest POSTs a JSON body to an Outline RPC endpoint and decodes the JSON
// response, retrying transient failures (429, 5xx, transport errors).
//
// The raw token is never logged; a redacted form is emitted once per client.
func (c *client) doRequest(ctx context.Context, path string, payload interface{}, result interface{}) error {
	const (
		maxRetries    = 3
		max5xxRetries = 1
		retry5xxDelay = 2 * time.Second
	)
	backoff := []time.Duration{2 * time.Second, 4 * time.Second, 8 * time.Second}

	c.logTokenOnce.Do(func() {
		logger.Infof(ctx, "[Outline] client configured token=%s base=%s", redactToken(c.token), c.baseURL)
	})

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal request body: %w", err)
	}

	var lastErr error
	for attempt := 0; attempt <= maxRetries; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(body))
		if err != nil {
			return fmt.Errorf("create request: %w", err)
		}
		req.Header.Set("Authorization", "Bearer "+c.token)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json")
		// Cloudflare answers 403 with error code 1010 when User-Agent is absent,
		// which is a common fronting for self-hosted Outline.
		req.Header.Set("User-Agent", userAgent)

		if attempt == 0 {
			logger.Infof(ctx, "[Outline] POST %s", path)
		} else {
			logger.Infof(ctx, "[Outline] POST %s (retry %d/%d)", path, attempt, maxRetries)
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

		preview := truncate(string(respBody), 500)

		if resp.StatusCode == http.StatusTooManyRequests {
			wait := parseRetryAfter(resp.Header.Get("Retry-After"), backoff[minInt(attempt, len(backoff)-1)])
			lastErr = fmt.Errorf("outline rate limited: status=429 body=%s", preview)
			if attempt < maxRetries {
				if sErr := sleepCtx(ctx, wait); sErr != nil {
					return sErr
				}
				continue
			}
			return lastErr
		}

		if resp.StatusCode >= 500 && resp.StatusCode < 600 {
			lastErr = fmt.Errorf("outline server error: status=%d body=%s", resp.StatusCode, preview)
			if attempt < max5xxRetries {
				if sErr := sleepCtx(ctx, retry5xxDelay); sErr != nil {
					return sErr
				}
				continue
			}
			return lastErr
		}

		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			var apiErr apiErrorBody
			_ = json.Unmarshal(respBody, &apiErr)
			msg := apiErr.Message
			if msg == "" {
				msg = apiErr.Error
			}
			if msg == "" {
				msg = preview
			}
			return fmt.Errorf("outline API %s: status=%d: %s", path, resp.StatusCode, msg)
		}

		if result == nil {
			return nil
		}
		if err := json.Unmarshal(respBody, result); err != nil {
			return fmt.Errorf("decode response from %s: %w", path, err)
		}
		return nil
	}
	return lastErr
}

// Ping verifies the token by asking Outline which team it belongs to. Only the
// team name is logged: auth.info also carries the user's email address, which
// must never be written to logs.
func (c *client) Ping(ctx context.Context) error {
	var out authInfoResponse
	if err := c.doRequest(ctx, "/api/auth.info", map[string]interface{}{}, &out); err != nil {
		return err
	}
	logger.Infof(ctx, "[Outline] authenticated against team %q", out.Data.Team.Name)
	return nil
}

// ListCollections returns every collection the token can see.
func (c *client) ListCollections(ctx context.Context) ([]collection, error) {
	var all []collection
	offset := 0
	for {
		var out collectionsListResponse
		err := c.doRequest(ctx, "/api/collections.list", map[string]interface{}{
			"limit":  defaultPageSize,
			"offset": offset,
		}, &out)
		if err != nil {
			return nil, err
		}
		if len(out.Data) == 0 {
			break
		}
		all = append(all, out.Data...)
		offset += len(out.Data)
		if out.Pagination.Total > 0 && offset >= out.Pagination.Total {
			break
		}
	}
	return all, nil
}

// ListCollectionDocuments returns every document in a collection, including
// nested ones. Outline embeds each document's full Markdown in the list
// response, so this is both the enumeration and the content fetch.
func (c *client) ListCollectionDocuments(ctx context.Context, collectionID string) ([]document, error) {
	var all []document
	offset := 0
	for {
		var out documentsListResponse
		err := c.doRequest(ctx, "/api/documents.list", map[string]interface{}{
			"collectionId": collectionID,
			"limit":        defaultPageSize,
			"offset":       offset,
		}, &out)
		if err != nil {
			return nil, err
		}
		if len(out.Data) == 0 {
			break
		}
		all = append(all, out.Data...)
		offset += len(out.Data)
		if out.Pagination.Total > 0 && offset >= out.Pagination.Total {
			break
		}
	}
	return all, nil
}

// DownloadAttachment fetches an attachment's bytes with the API token.
//
// The endpoint answers 302. Where it points depends on how the instance stores
// files: same-origin /api/files.get for local storage, or an off-origin
// presigned URL for S3-compatible storage. The shared connector HTTP client
// keeps the Authorization header on a same-origin redirect and strips it when
// the redirect crosses origins — which is exactly right, because a presigned
// URL needs no credentials and must not receive ours.
func (c *client) DownloadAttachment(ctx context.Context, attachmentID string) ([]byte, string, error) {
	url := c.baseURL + "/api/attachments.redirect?id=" + attachmentID
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, "", fmt.Errorf("create attachment request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("User-Agent", userAgent)

	resp, err := c.fileClient.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("download attachment %s: %w", attachmentID, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, "", fmt.Errorf("download attachment %s: status=%d", attachmentID, resp.StatusCode)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", fmt.Errorf("read attachment %s: %w", attachmentID, err)
	}
	return data, resp.Header.Get("Content-Type"), nil
}

// sleepCtx sleeps for d unless ctx is canceled first.
func sleepCtx(ctx context.Context, d time.Duration) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(d):
		return nil
	}
}

// parseRetryAfter reads a Retry-After header in seconds, falling back to def.
func parseRetryAfter(h string, def time.Duration) time.Duration {
	if h == "" {
		return def
	}
	secs, err := strconv.Atoi(h)
	if err != nil || secs < 0 {
		return def
	}
	return time.Duration(secs) * time.Second
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
