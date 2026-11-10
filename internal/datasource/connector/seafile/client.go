package seafile

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/datasource"
	"github.com/Tencent/WeKnora/internal/utils"
)

const (
	userAgent       = "WeKnora-Seafile-Connector/1.0"
	apiTimeout      = 30 * time.Second
	downloadTimeout = 120 * time.Second

	// maxAttempts is the initial request plus two retries.
	maxAttempts = 3
	// maxRetryAfter bounds an honoured Retry-After; anything longer aborts the run.
	maxRetryAfter = 60 * time.Second

	listingJSONLimit = int64(32 << 20)
	smallJSONLimit   = int64(64 << 10)
)

var (
	errRetryAfterTooLong = errors.New("seafile asked to retry after more than 60 seconds")
	errInvalidResponse   = errors.New("seafile returned an unexpected response")
	errSSRFBlocked       = errors.New("seafile download URL was blocked by the SSRF policy")
	errTooLarge          = errors.New("seafile file exceeds the size limit")
	errEmptyFile         = errors.New("seafile file is empty")
	errSourceChanged     = errors.New("seafile file changed while it was being fetched")

	// sleep performs every backoff wait; tests replace it to avoid real waits.
	sleep = sleepCtx
)

// client talks to one Seafile deployment. API requests carry the token; the
// fileserver client never does, and both keep the shared SSRF redirect policy.
type client struct {
	baseURL string
	token   string
	api     *http.Client
	files   *http.Client
}

func newClient(cfg config) *client {
	return &client{
		baseURL: cfg.baseURL,
		token:   cfg.token,
		api:     datasource.NewConnectorHTTPClient(apiTimeout),
		files:   datasource.NewConnectorHTTPClient(downloadTimeout),
	}
}

// httpError reports a non-success status. Op is a fixed label, never a URL,
// path or response body, so the message is safe to surface.
type httpError struct {
	Op         string
	Status     int
	retryAfter string
}

func (e *httpError) Error() string { return fmt.Sprintf("seafile %s: HTTP %d", e.Op, e.Status) }
func (e *httpError) Unwrap() error { return datasource.ErrFetchFailed }

// statusOf returns the HTTP status carried by err, or 0.
func statusOf(err error) int {
	var statusErr *httpError
	if errors.As(err, &statusErr) {
		return statusErr.Status
	}
	return 0
}

// transportError hides the request URL (a *url.Error would echo a signed
// download link) while remembering whether a retry is worthwhile.
type transportError struct {
	Op        string
	transient bool
}

func (e *transportError) Error() string { return "seafile " + e.Op + ": request failed" }
func (e *transportError) Unwrap() error { return datasource.ErrFetchFailed }

func invalidResponse(op string) error {
	return fmt.Errorf("%w: seafile %s: %w", datasource.ErrFetchFailed, op, errInvalidResponse)
}

func isTransientNetworkError(err error) bool {
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return true
	}
	var opErr *net.OpError
	return errors.As(err, &opErr) && opErr.Op == "dial"
}

func retryableStatus(status int) bool {
	return status == http.StatusTooManyRequests || status == http.StatusBadGateway ||
		status == http.StatusServiceUnavailable || status == http.StatusGatewayTimeout
}

func backoff(attempt int) time.Duration {
	return time.Second << attempt
}

// retryAfter parses a Retry-After header (delta seconds or HTTP date). Values
// above maxRetryAfter, including unparsable huge numbers, are run-fatal.
func retryAfter(value string, now time.Time) (time.Duration, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, nil
	}
	if strings.Trim(value, "0123456789") == "" {
		seconds, err := strconv.ParseInt(value, 10, 64)
		if err != nil || seconds > int64(maxRetryAfter/time.Second) {
			return 0, errRetryAfterTooLong
		}
		return time.Duration(seconds) * time.Second, nil
	}
	date, err := http.ParseTime(value)
	if err != nil {
		return 0, nil
	}
	if wait := date.Sub(now); wait > maxRetryAfter {
		return 0, errRetryAfterTooLong
	} else if wait > 0 {
		return wait, nil
	}
	return 0, nil
}

// get performs a GET with retries on throttling, upstream 5xx and transient
// network errors. authenticated selects the API client and token; otherwise
// the fileserver client is used without credentials and only 200 is accepted.
func (c *client) get(
	ctx context.Context, target, op string, authenticated bool, limit int64,
) ([]byte, http.Header, error) {
	for attempt := 0; ; attempt++ {
		body, headers, err := c.once(ctx, target, op, authenticated, limit)
		if err == nil {
			return body, headers, nil
		}
		delay, retry, err := classify(err, attempt)
		if !retry || attempt == maxAttempts-1 {
			return nil, nil, err
		}
		if err := sleep(ctx, delay); err != nil {
			return nil, nil, err
		}
	}
}

// classify decides whether err is worth retrying and how long to wait.
func classify(err error, attempt int) (time.Duration, bool, error) {
	var statusErr *httpError
	var netErr *transportError
	switch {
	case errors.As(err, &statusErr):
		if !retryableStatus(statusErr.Status) {
			return 0, false, err
		}
		wait, raErr := retryAfter(statusErr.retryAfter, time.Now())
		if raErr != nil {
			return 0, false, fmt.Errorf("%w: %w", statusErr, raErr)
		}
		return max(backoff(attempt), wait), true, err
	case errors.As(err, &netErr):
		return backoff(attempt), netErr.transient, err
	default:
		return 0, false, err
	}
}

func (c *client) once(
	ctx context.Context, target, op string, authenticated bool, limit int64,
) ([]byte, http.Header, error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, nil, invalidResponse(op)
	}
	req.Header.Set("User-Agent", userAgent)
	httpClient := c.files
	if authenticated {
		httpClient = c.api
		req.Header.Set("Authorization", "Token "+c.token)
		req.Header.Set("Accept", "application/json")
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, nil, c.transport(ctx, op, err)
	}
	defer func() { _ = resp.Body.Close() }()

	ok := resp.StatusCode == http.StatusOK
	if authenticated {
		ok = resp.StatusCode >= 200 && resp.StatusCode < 300
	}
	if !ok {
		return nil, nil, &httpError{Op: op, Status: resp.StatusCode, retryAfter: resp.Header.Get("Retry-After")}
	}
	if !authenticated && resp.ContentLength > limit {
		return nil, nil, errTooLarge
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, nil, c.transport(ctx, op, err)
	}
	if int64(len(body)) > limit {
		if authenticated {
			return nil, nil, invalidResponse(op)
		}
		return nil, nil, errTooLarge
	}
	return body, resp.Header, nil
}

func (c *client) transport(ctx context.Context, op string, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if errors.Is(err, utils.ErrSSRFRedirectBlocked) {
		return fmt.Errorf("%w: %w", datasource.ErrFetchFailed, errSSRFBlocked)
	}
	return &transportError{Op: op, transient: isTransientNetworkError(err)}
}

func (c *client) getJSON(ctx context.Context, endpoint, op string, limit int64, out any) (http.Header, error) {
	body, headers, err := c.get(ctx, c.baseURL+endpoint, op, true, limit)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(body, out); err != nil {
		return nil, invalidResponse(op)
	}
	return headers, nil
}

// credentialError maps 401/403 on account-level endpoints to
// ErrInvalidCredentials. Directory-level 403s are scoping problems and are
// left to the connector.
func credentialError(err error) error {
	if status := statusOf(err); status == http.StatusUnauthorized || status == http.StatusForbidden {
		return fmt.Errorf("%w: %w", datasource.ErrInvalidCredentials, err)
	}
	return err
}

func (c *client) ping(ctx context.Context) error {
	var pong string
	if _, err := c.getJSON(ctx, "/api2/auth/ping/", "ping", smallJSONLimit, &pong); err != nil {
		return credentialError(err)
	}
	if pong != "pong" {
		return invalidResponse("ping")
	}
	return nil
}

func (c *client) listRepos(ctx context.Context) ([]repoInfo, error) {
	var repos []repoInfo
	if _, err := c.getJSON(ctx, "/api2/repos/", "repos", listingJSONLimit, &repos); err != nil {
		return nil, credentialError(err)
	}
	if repos == nil {
		return nil, invalidResponse("repos")
	}
	return repos, nil
}

func repoEndpoint(repoID, kind string, query url.Values) string {
	return "/api2/repos/" + url.PathEscape(repoID) + "/" + kind + "/?" + query.Encode()
}

// listDir returns one directory level. Non-2xx statuses come back as
// *httpError so the connector can tell 403/404 apart.
func (c *client) listDir(ctx context.Context, repoID, path string) ([]dirent, error) {
	var entries []dirent
	endpoint := repoEndpoint(repoID, "dir", url.Values{"p": {path}})
	if _, err := c.getJSON(ctx, endpoint, "dir", listingJSONLimit, &entries); err != nil {
		return nil, err
	}
	if entries == nil {
		return nil, invalidResponse("dir")
	}
	return entries, nil
}

// downloadLink resolves a reusable fileserver URL and the file's object id
// from the response header (empty when the server omits it).
func (c *client) downloadLink(ctx context.Context, repoID, path string) (link, oid string, err error) {
	endpoint := repoEndpoint(repoID, "file", url.Values{"p": {path}, "reuse": {"1"}})
	headers, err := c.getJSON(ctx, endpoint, "file", smallJSONLimit, &link)
	if err != nil {
		return "", "", err
	}
	if !validDownloadURL(link) {
		return "", "", invalidResponse("file")
	}
	return link, headers.Get("oid"), nil
}

// validDownloadURL accepts absolute http(s) URLs without userinfo, which Go
// would otherwise turn into a Basic Authorization header.
func validDownloadURL(link string) bool {
	u, err := url.Parse(link)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Hostname() != "" && u.User == nil
}

// download fetches a fileserver link without credentials. The body must be
// exactly expectedSize bytes and at most limit bytes.
func (c *client) download(ctx context.Context, link string, expectedSize, limit int64) ([]byte, error) {
	if !validDownloadURL(link) {
		return nil, invalidResponse("download")
	}
	if err := utils.ValidateURLForSSRF(link); err != nil {
		return nil, fmt.Errorf("%w: %w", datasource.ErrFetchFailed, errSSRFBlocked)
	}
	body, _, err := c.get(ctx, link, "download", false, limit)
	if err != nil {
		return nil, err
	}
	if len(body) == 0 {
		return nil, errEmptyFile
	}
	if int64(len(body)) != expectedSize {
		return nil, errSourceChanged
	}
	return body, nil
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
