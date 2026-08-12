package ima

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/Tencent/WeKnora/internal/datasource"
	"github.com/Tencent/WeKnora/internal/logger"
)

const (
	defaultTimeout  = 60 * time.Second
	defaultPageSize = 50
	userAgent       = "WeKnora-IMA-Connector/1.0"

	// IMA hard limit: get_knowledge_list max limit=50; search_knowledge_base max=20.
	searchPageSize = 20

	// Default per-item download timeout — IMA COS URLs can be slow for large PDFs.
	downloadTimeout = 120 * time.Second

	// Max body we accept from get_media_info-URLs. 200MB matches IMA's largest
	maxDownloadBytes = 200 * 1024 * 1024
)

// client wraps the IMA OpenAPI. It is safe for concurrent use.
type client struct {
	baseURL    string
	clientID   string
	apiKey     string
	httpClient *http.Client
	// downloadClient is a separate client with a longer timeout for pulling
	// media bodies from COS/CDN URLs returned by get_media_info.
	downloadClient    *http.Client
	logCredentialOnce sync.Once
}

// newClient constructs a client with a normalized base URL.
func newClient(cfg *Config) *client {
	return &client{
		baseURL:        cfg.GetBaseURL(),
		clientID:       cfg.ClientID,
		apiKey:         cfg.APIKey,
		httpClient:     datasource.NewConnectorHTTPClient(defaultTimeout),
		downloadClient: datasource.NewConnectorHTTPClient(downloadTimeout),
	}
}

// callAPI executes an authenticated POST to /openapi/wiki/v1/<action>.
// It parses the standard {code, msg, data} envelope and unmarshals `data`
// into `result`. A non-zero business code is returned as a plain error so the
// caller can surface `msg` to the user
func (c *client) callAPI(ctx context.Context, action string, req interface{}, result interface{}) error {
	const (
		maxRetries    = 3
		max5xxRetries = 1
		retry5xxDelay = 2 * time.Second
	)
	backoff := []time.Duration{2 * time.Second, 4 * time.Second, 8 * time.Second}

	c.logCredentialOnce.Do(func() {
		logger.Infof(ctx, "[IMA] client configured client_id=%s api_key=%s base=%s",
			redact(c.clientID), redact(c.apiKey), c.baseURL)
	})

	var body []byte
	if req != nil {
		b, err := json.Marshal(req)
		if err != nil {
			return fmt.Errorf("marshal request: %w", err)
		}
		body = b
	} else {
		body = []byte("{}")
	}

	url := c.baseURL + apiBasePath + "/" + action

	var lastErr error
	for attempt := 0; attempt <= maxRetries; attempt++ {
		httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
		if err != nil {
			return fmt.Errorf("create request: %w", err)
		}
		httpReq.Header.Set("ima-openapi-clientid", c.clientID)
		httpReq.Header.Set("ima-openapi-apikey", c.apiKey)
		httpReq.Header.Set("Content-Type", "application/json; charset=utf-8")
		httpReq.Header.Set("User-Agent", userAgent)

		if attempt == 0 {
			logger.Infof(ctx, "[IMA] POST %s", action)
		} else {
			logger.Infof(ctx, "[IMA] POST %s (retry %d/%d)", action, attempt, maxRetries)
		}

		resp, err := c.httpClient.Do(httpReq)
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
			lastErr = fmt.Errorf("read response: %w", readErr)
			if attempt < maxRetries {
				if sErr := sleepCtx(ctx, backoff[attempt]); sErr != nil {
					return sErr
				}
				continue
			}
			return lastErr
		}

		bodyPreview := truncate(string(respBody), 500)
		logger.Infof(ctx, "[IMA] POST %s → status=%d bodyLen=%d body=%s",
			action, resp.StatusCode, len(respBody), bodyPreview)

		// HTTP-level auth failures: surface as ErrInvalidCredentials.
		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
			return fmt.Errorf("%w: status=%d body=%s",
				datasource.ErrInvalidCredentials, resp.StatusCode, bodyPreview)
		}

		if resp.StatusCode == http.StatusTooManyRequests {
			wait := backoff[minInt(attempt, len(backoff)-1)]
			lastErr = fmt.Errorf("ima rate limited: status=429 body=%s", bodyPreview)
			if attempt < maxRetries {
				if sErr := sleepCtx(ctx, wait); sErr != nil {
					return sErr
				}
				continue
			}
			return lastErr
		}

		if resp.StatusCode >= 500 && resp.StatusCode < 600 {
			lastErr = fmt.Errorf("ima server error: status=%d body=%s", resp.StatusCode, bodyPreview)
			if attempt < max5xxRetries {
				if sErr := sleepCtx(ctx, retry5xxDelay); sErr != nil {
					return sErr
				}
				continue
			}
			return lastErr
		}

		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return fmt.Errorf("ima api http error: status=%d body=%s", resp.StatusCode, bodyPreview)
		}

		var env apiEnvelope
		if err := json.Unmarshal(respBody, &env); err != nil {
			return fmt.Errorf("decode envelope: %w", err)
		}

		// Business-level errors.
		// 110030 (无权限) / 110001 (参数非法) with common auth hint patterns are
		// mapped to ErrInvalidCredentials so the service marks the source in
		// `error` state and stops scheduling until re-authenticated.
		if env.Code != 0 {
			if env.Code == 110030 {
				return fmt.Errorf("%w: ima code=%d msg=%s",
					datasource.ErrInvalidCredentials, env.Code, env.Msg)
			}
			if env.Code == 110021 && attempt < maxRetries {
				lastErr = fmt.Errorf("ima rate limited: code=%d msg=%s", env.Code, env.Msg)
				if sErr := sleepCtx(ctx, backoff[minInt(attempt, len(backoff)-1)]); sErr != nil {
					return sErr
				}
				continue
			}
			return fmt.Errorf("ima api error: code=%d msg=%s", env.Code, env.Msg)
		}

		if result != nil && len(env.Data) > 0 && string(env.Data) != "null" {
			if err := json.Unmarshal(env.Data, result); err != nil {
				return fmt.Errorf("decode data: %w", err)
			}
		}
		return nil
	}
	return lastErr
}

// SearchKnowledgeBase — POST /search_knowledge_base. Empty query returns the
// list of every knowledge base visible to the current token (see api.md §7).
func (c *client) SearchKnowledgeBase(
	ctx context.Context, query, cursor string, limit int,
) (*searchKnowledgeBaseResp, error) {
	if limit <= 0 || limit > searchPageSize {
		limit = searchPageSize
	}
	req := map[string]interface{}{
		"query":  query,
		"cursor": cursor,
		"limit":  uint64(limit),
	}
	var resp searchKnowledgeBaseResp
	if err := c.callAPI(ctx, "search_knowledge_base", req, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// GetKnowledgeBase — POST /get_knowledge_base for a batch of ids (1-20).
func (c *client) GetKnowledgeBase(ctx context.Context, ids []string) (map[string]knowledgeBaseInfo, error) {
	if len(ids) == 0 {
		return map[string]knowledgeBaseInfo{}, nil
	}
	req := map[string]interface{}{"ids": ids}
	var resp getKnowledgeBaseResp
	if err := c.callAPI(ctx, "get_knowledge_base", req, &resp); err != nil {
		return nil, err
	}
	if resp.Infos == nil {
		resp.Infos = map[string]knowledgeBaseInfo{}
	}
	return resp.Infos, nil
}

// GetAddableKnowledgeBaseList — POST /get_addable_knowledge_base_list.
// Lists knowledge bases the current token has permission to add content to.
// This is the authoritative "what KBs can this credential see?" endpoint
// (see api.md §7). Used as the primary enumeration source in ListResources —
// search_knowledge_base with empty query has been observed to return an empty
// list even when the token owns KBs, so we prefer this endpoint.
func (c *client) GetAddableKnowledgeBaseList(
	ctx context.Context, cursor string, limit int,
) (*getAddableKnowledgeBaseListResp, error) {
	if limit <= 0 || limit > defaultPageSize {
		limit = defaultPageSize
	}
	req := map[string]interface{}{
		"cursor": cursor,
		"limit":  uint64(limit),
	}
	var resp getAddableKnowledgeBaseListResp
	if err := c.callAPI(ctx, "get_addable_knowledge_base_list", req, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// GetKnowledgeList — POST /get_knowledge_list. folderID may be empty for root.
func (c *client) GetKnowledgeList(
	ctx context.Context, kbID, folderID, cursor string, limit int,
) (*getKnowledgeListResp, error) {
	if limit <= 0 || limit > defaultPageSize {
		limit = defaultPageSize
	}
	req := map[string]interface{}{
		"cursor":            cursor,
		"limit":             uint64(limit),
		"knowledge_base_id": kbID,
	}
	if folderID != "" {
		req["folder_id"] = folderID
	}
	var resp getKnowledgeListResp
	if err := c.callAPI(ctx, "get_knowledge_list", req, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// GetMediaInfo — POST /get_media_info. Returns URL access info and (for notes)
// the notebook_id under notebook_ext_info.
func (c *client) GetMediaInfo(ctx context.Context, mediaID string) (*getMediaInfoResp, error) {
	req := map[string]interface{}{"media_id": mediaID}
	var resp getMediaInfoResp
	if err := c.callAPI(ctx, "get_media_info", req, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// DownloadURL fetches a URL returned by GetMediaInfo, respecting the auth
// headers IMA may return alongside it. Enforces maxDownloadBytes to avoid a
// runaway body blowing up sync memory.
func (c *client) DownloadURL(ctx context.Context, u urlInfo) ([]byte, string, error) {
	if u.URL == "" {
		return nil, "", fmt.Errorf("empty url")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.URL, nil)
	if err != nil {
		return nil, "", fmt.Errorf("create download request: %w", err)
	}
	for k, v := range u.Headers {
		req.Header.Set(k, v)
	}
	req.Header.Set("User-Agent", userAgent)

	resp, err := c.downloadClient.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("download: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, "", fmt.Errorf("download http error: status=%d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxDownloadBytes+1))
	if err != nil {
		return nil, "", fmt.Errorf("read download body: %w", err)
	}
	if int64(len(body)) > maxDownloadBytes {
		return nil, "", fmt.Errorf("download body exceeds %d bytes", maxDownloadBytes)
	}
	return body, resp.Header.Get("Content-Type"), nil
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

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// redact returns a masked form of a credential for logging (never log full).
func redact(t string) string {
	if len(t) < 12 {
		return "***"
	}
	return t[:6] + "..." + t[len(t)-4:]
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
