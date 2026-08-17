package pds

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/Tencent/WeKnora/internal/datasource"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/utils"
)

const (
	// defaultTimeout is the per-request timeout for PDS API calls.
	defaultTimeout = 60 * time.Second
	// downloadTimeout is the per-request timeout for downloading file
	// bodies from a signed PDS URL. PDS files can be large (multi-MB
	// PDFs), so the default is generous.
	downloadTimeout = 120 * time.Second
	// maxDownloadBytes is the hard cap on a single file body. PDS serves
	// attachments up to a few GB; 200 MB matches the IMA connector's
	// limit and protects sync memory.
	maxDownloadBytes = 200 * 1024 * 1024
	// userAgent is the UA we present to the PDS server. Helps the
	// operator find our traffic in PDS access logs.
	userAgent = "WeKnora-PDS-Connector/1.0"
	// apiBasePath is the PDS OpenAPI 2022-03-01 root.
	apiBasePath = "/v2"
	// maxListDrivePages bounds the drive/list pagination loop so a
	// pathological server can't spin us forever.
	maxListDrivePages = 50
)

// pdsAPIError is a business-level PDS error: a non-2xx response whose
// body carries the standard {"code","message"} shape.
type pdsAPIError struct {
	Status  int
	Code    string
	Message string
}

func (e *pdsAPIError) Error() string {
	return fmt.Sprintf("pds api error: status=%d code=%s message=%s", e.Status, e.Code, e.Message)
}

// cursorExpiredError signals that PDS rejected the persisted delta cursor
// and the connector should re-handshake via GetLastCursor.
type cursorExpiredError struct{ err error }

func (e *cursorExpiredError) Error() string { return e.err.Error() }
func (e *cursorExpiredError) Unwrap() error { return e.err }

// isCursorExpiredErr reports whether err is (or wraps) a cursor-expired
// failure. PDS varies the wire code between deployments, so we match the
// documented codes plus a message fallback.
func isCursorExpiredErr(err error) bool {
	if err == nil {
		return false
	}
	var apiErr *pdsAPIError
	if errors.As(err, &apiErr) {
		switch apiErr.Code {
		case "InvalidCursor", "CursorExpired", "CursorNotFound":
			return true
		}
	}
	var ce *cursorExpiredError
	if errors.As(err, &ce) {
		return true
	}
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "invalidcursor") ||
		strings.Contains(s, "cursor_expired") ||
		strings.Contains(s, "cursor expired") ||
		strings.Contains(s, "invalid cursor")
}

// client is a thin PDS OpenAPI wrapper for the OAuth bearer-token auth
// paths (access_token / refresh_token). AK/SK credentials use the
// official SDK instead (client_sdk.go). A client is bound to one Config;
// the connector builds a fresh client per call, so token state never
// leaks across data sources.
type client struct {
	baseURL        string
	cfg            *Config
	httpClient     *http.Client
	downloadClient *http.Client

	tokenMu     sync.Mutex
	accessToken string
}

// newClient constructs a PDS client from a parsed Config.
func newClient(cfg *Config) *client {
	return &client{
		baseURL:        cfg.GetEndpoint() + apiBasePath,
		cfg:            cfg,
		httpClient:     datasource.NewConnectorHTTPClient(defaultTimeout),
		downloadClient: datasource.NewConnectorHTTPClient(downloadTimeout),
		accessToken:    cfg.AccessToken,
	}
}

// resolveToken returns a usable bearer token, exchanging the configured
// refresh_token on first use. The exchanged token is cached until
// invalidateToken is called (on a 401).
func (c *client) resolveToken(ctx context.Context) (string, error) {
	c.tokenMu.Lock()
	defer c.tokenMu.Unlock()
	if c.accessToken != "" {
		return c.accessToken, nil
	}
	if c.cfg.RefreshToken == "" {
		return "", fmt.Errorf("%w: no access_token or refresh_token configured",
			datasource.ErrInvalidCredentials)
	}
	// Exchange refresh_token for a fresh access_token via the PDS OAuth
	// endpoint (POST /v2/oauth/token, grant_type=refresh_token). The
	// response mirrors the standard OAuth 2.0 shape.
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.baseURL+"/oauth/token",
		strings.NewReader("grant_type=refresh_token&refresh_token="+
			url.QueryEscape(c.cfg.RefreshToken)))
	if err != nil {
		return "", fmt.Errorf("build oauth request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", userAgent)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("oauth request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("%w: oauth exchange failed: status=%d body=%s",
			datasource.ErrInvalidCredentials, resp.StatusCode, truncate(string(body), 300))
	}
	var oauthResp struct {
		AccessToken string `json:"access_token"`
		TokenType   string `json:"token_type"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.Unmarshal(body, &oauthResp); err != nil {
		return "", fmt.Errorf("decode oauth response: %w", err)
	}
	if oauthResp.AccessToken == "" {
		return "", fmt.Errorf("%w: oauth response has empty access_token",
			datasource.ErrInvalidCredentials)
	}
	c.accessToken = oauthResp.AccessToken
	logger.Infof(ctx, "[PDS] obtained access_token via refresh_token (expires_in=%d)",
		oauthResp.ExpiresIn)
	return c.accessToken, nil
}

// invalidateToken drops the cached token so the next resolveToken
// re-exchanges the refresh_token. Called after a 401.
func (c *client) invalidateToken() {
	c.tokenMu.Lock()
	c.accessToken = ""
	c.tokenMu.Unlock()
}

// callAPI executes a POST to <basePath>/<action> with the given JSON body
// and a bearer token, decoding the flat 2xx response body into `result`.
//
// PDS success responses are plain JSON objects ({"items": [...],
// "next_marker": "..."}); there is no envelope. Business errors arrive as
// non-2xx with a {"code","message"} body, decoded into *pdsAPIError.
//
// HTTP 401 triggers one token re-exchange + retry when a refresh_token is
// configured (PDS access tokens expire, typically after two hours).
// 401/403 without a usable refresh path surface as ErrInvalidCredentials
// so the service marks the source in `error` state. 429/5xx retry with
// backoff.
func (c *client) callAPI(
	ctx context.Context, action string, body map[string]interface{}, result interface{},
) error {
	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}
	url := c.baseURL + "/" + action
	logger.Infof(ctx, "[PDS] POST %s", action)

	const maxRetries = 3
	// Production backoff for transient 5xx/429. Tests use a tighter
	// policy (env PDS_FAST_RETRY=1) so retry-after-fast failures don't
	// slow CI.
	backoff := defaultBackoff()
	if v := os.Getenv("PDS_FAST_RETRY"); v != "" && v != "0" {
		backoff = []time.Duration{5 * time.Millisecond, 25 * time.Millisecond, 100 * time.Millisecond}
	}

	retriedAfterRefresh := false
	var lastErr error
	for attempt := 0; attempt <= maxRetries; attempt++ {
		token, terr := c.resolveToken(ctx)
		if terr != nil {
			return fmt.Errorf("pds auth: %w", terr)
		}
		req, rerr := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(bodyBytes))
		if rerr != nil {
			return fmt.Errorf("create request: %w", rerr)
		}
		req.Header.Set("Content-Type", "application/json; charset=utf-8")
		req.Header.Set("User-Agent", userAgent)
		req.Header.Set("Authorization", "Bearer "+token)

		resp, derr := c.httpClient.Do(req)
		if derr != nil {
			lastErr = fmt.Errorf("execute request: %w", derr)
			if attempt < maxRetries {
				if sErr := sleepCtx(ctx, backoff[attempt]); sErr != nil {
					return sErr
				}
				continue
			}
			return lastErr
		}
		respBody, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		bodyPreview := truncate(string(respBody), 500)
		logger.Infof(ctx, "[PDS] POST %s -> status=%d bodyLen=%d",
			action, resp.StatusCode, len(respBody))

		switch {
		case resp.StatusCode == http.StatusUnauthorized:
			// Token expired or revoked. When we hold a refresh_token,
			// swap it for a fresh access token and retry once; otherwise
			// this is a terminal credentials failure.
			if c.cfg.RefreshToken != "" && !retriedAfterRefresh {
				logger.Warnf(ctx, "[PDS] 401 on %s: refreshing access_token and retrying", action)
				c.invalidateToken()
				retriedAfterRefresh = true
				continue
			}
			return fmt.Errorf("%w: pds status=401 body=%s",
				datasource.ErrInvalidCredentials, bodyPreview)
		case resp.StatusCode == http.StatusForbidden:
			return fmt.Errorf("%w: pds status=403 body=%s",
				datasource.ErrInvalidCredentials, bodyPreview)
		case resp.StatusCode == http.StatusTooManyRequests,
			resp.StatusCode >= 500 && resp.StatusCode < 600:
			lastErr = fmt.Errorf("pds transient error: status=%d body=%s",
				resp.StatusCode, bodyPreview)
			if attempt < maxRetries {
				if sErr := sleepCtx(ctx, backoff[attempt]); sErr != nil {
					return sErr
				}
				continue
			}
			return lastErr
		case resp.StatusCode < 200 || resp.StatusCode >= 300:
			return newPDSAPIError(resp.StatusCode, respBody)
		}

		// 2xx: flat JSON body straight into result.
		if result != nil && len(respBody) > 0 && string(respBody) != "null" {
			if err := json.Unmarshal(respBody, result); err != nil {
				return fmt.Errorf("decode response: %w (body=%s)", err, bodyPreview)
			}
		}
		return nil
	}
	return lastErr
}

// newPDSAPIError decodes the standard {"code","message"} error body into
// a typed *pdsAPIError, falling back to a raw-body message when the body
// isn't the expected shape.
func newPDSAPIError(status int, body []byte) error {
	var payload struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(body, &payload); err != nil || payload.Code == "" {
		return &pdsAPIError{
			Status:  status,
			Code:    http.StatusText(status),
			Message: truncate(string(body), 300),
		}
	}
	return &pdsAPIError{Status: status, Code: payload.Code, Message: payload.Message}
}

// ListDrive returns every PDS drive the token can read, following the
// marker pagination until drained (bounded by maxListDrivePages).
func (c *client) ListDrive(ctx context.Context) ([]pdsDrive, error) {
	var out []pdsDrive
	marker := ""
	for page := 0; page < maxListDrivePages; page++ {
		body := map[string]interface{}{"limit": 100}
		if marker != "" {
			body["marker"] = marker
		}
		var resp struct {
			Items      []pdsDrive `json:"items"`
			NextMarker string     `json:"next_marker"`
		}
		if err := c.callAPI(ctx, "drive/list", body, &resp); err != nil {
			return nil, err
		}
		out = append(out, resp.Items...)
		if resp.NextMarker == "" {
			return out, nil
		}
		marker = resp.NextMarker
	}
	return out, nil
}

// ListFile returns one page of the direct children of a folder inside a
// drive. parentID == "" means "drive root" (translated to PDS's "root").
// The returned marker is empty when there are no more pages.
func (c *client) ListFile(ctx context.Context, driveID, parentID, marker string) ([]pdsFile, string, error) {
	body := map[string]interface{}{
		"drive_id": driveID,
		"limit":    100,
	}
	if parentID == "" {
		parentID = "root"
	}
	body["parent_file_id"] = parentID
	if marker != "" {
		body["marker"] = marker
	}
	var resp struct {
		Items      []pdsFile `json:"items"`
		NextMarker string    `json:"next_marker"`
	}
	if err := c.callAPI(ctx, "file/list", body, &resp); err != nil {
		return nil, "", err
	}
	return resp.Items, resp.NextMarker, nil
}

// GetFile fetches the metadata of a single file or folder. Used to walk
// up the parent chain when reconstructing readable paths for delta items
// (delta pages don't carry folder context).
func (c *client) GetFile(ctx context.Context, driveID, fileID string) (pdsFile, error) {
	var f pdsFile
	if err := c.callAPI(ctx, "file/get", map[string]interface{}{
		"drive_id": driveID,
		"file_id":  fileID,
	}, &f); err != nil {
		return pdsFile{}, err
	}
	return f, nil
}

// GetDownloadURL returns a signed HTTPS URL for the file's body.
func (c *client) GetDownloadURL(ctx context.Context, driveID, fileID string) (string, error) {
	var resp struct {
		URL string `json:"url"`
	}
	if err := c.callAPI(ctx, "file/get_download_url", map[string]interface{}{
		"drive_id": driveID,
		"file_id":  fileID,
	}, &resp); err != nil {
		return "", err
	}
	return resp.URL, nil
}

// GetLastCursor returns the latest server-side cursor for the drive's
// change feed. Used as the resume point on the first incremental sync.
func (c *client) GetLastCursor(ctx context.Context, driveID string) (string, error) {
	var resp struct {
		Cursor string `json:"cursor"`
	}
	if err := c.callAPI(ctx, "file/get_last_cursor", map[string]interface{}{
		"drive_id": driveID,
	}, &resp); err != nil {
		return "", err
	}
	return resp.Cursor, nil
}

// ListDelta returns one page of the drive's change feed since the given
// cursor: the changed items (each tagged with its op), the next cursor,
// and whether more pages remain. A rejected cursor surfaces as
// *cursorExpiredError so callers can re-handshake.
func (c *client) ListDelta(ctx context.Context, driveID, cursor string) ([]pdsDeltaItem, string, bool, error) {
	var resp struct {
		Items   []pdsDeltaItem `json:"items"`
		Cursor  string         `json:"cursor"`
		HasMore bool           `json:"has_more"`
	}
	err := c.callAPI(ctx, "file/list_delta", map[string]interface{}{
		"drive_id": driveID,
		"cursor":   cursor,
		"limit":    200,
	}, &resp)
	if err != nil {
		if isCursorExpiredErr(err) {
			return nil, "", false, &cursorExpiredError{err: err}
		}
		return nil, "", false, err
	}
	return resp.Items, resp.Cursor, resp.HasMore, nil
}

// Download fetches the bytes of a PDS file from its signed download URL.
// Enforces maxDownloadBytes to avoid a runaway body blowing up sync
// memory.
func (c *client) Download(ctx context.Context, url string) ([]byte, string, error) {
	return downloadWithClient(ctx, c.downloadClient, url)
}

// downloadWithClient is the shared signed-URL download implementation
// used by both transports.
func downloadWithClient(ctx context.Context, hc *http.Client, url string) ([]byte, string, error) {
	if url == "" {
		return nil, "", fmt.Errorf("empty download url")
	}
	if err := utils.ValidateURLForSSRF(url); err != nil {
		return nil, "", fmt.Errorf("download url rejected: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, "", fmt.Errorf("create download request: %w", err)
	}
	req.Header.Set("User-Agent", userAgent)

	resp, err := hc.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("download: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

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

// defaultBackoff returns the production retry delays. Centralized so the
// test harness (via PDS_FAST_RETRY) and any future tweaks stay in one
// place.
func defaultBackoff() []time.Duration {
	return []time.Duration{2 * time.Second, 4 * time.Second, 8 * time.Second}
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
