package nextcloud

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/datasource"
	"github.com/Tencent/WeKnora/internal/utils"
)

const (
	listingLimit      = int64(32 << 20)
	maxListingEntries = 100000
	downloadTimeout   = 120 * time.Second
	propfindBody      = `<d:propfind xmlns:d="DAV:" xmlns:oc="http://owncloud.org/ns"><d:prop>` +
		`<d:resourcetype/><d:getetag/><d:getlastmodified/><d:getcontentlength/><oc:fileid/>` +
		`</d:prop></d:propfind>`
)

var (
	errInvalidResponse = errors.New("nextcloud returned an invalid or incomplete WebDAV response")
	errSSRFBlocked     = errors.New("nextcloud request blocked by SSRF policy")
	errTooLarge        = errors.New("nextcloud file exceeds the size limit")
	errEmptyFile       = errors.New("nextcloud file is empty")
	errSourceChanged   = errors.New("nextcloud file changed while being fetched")
)

type client struct {
	base                          *url.URL
	davPrefix, username, password string
	http                          *http.Client
}

func newClient(cfg config) *client {
	u, _ := url.Parse(cfg.baseURL)
	policy := utils.DefaultSSRFSafeHTTPClientConfig()
	policy.Timeout = downloadTimeout
	// Basic credentials must never follow redirects to another origin, even
	// when that origin is explicitly allowed by the shared SSRF whitelist.
	policy.SameOriginRedirectsOnly = true
	return &client{
		base: u, davPrefix: u.Path + "/remote.php/dav/files/" + cfg.username,
		username: cfg.username, password: cfg.password, http: utils.NewSSRFSafeHTTPClient(policy),
	}
}

// httpError exposes only an operation and status. Response bodies and URL
// errors can contain credentials and must never reach sync logs or the API.
type httpError struct {
	method string
	status int
}

func (e *httpError) Error() string { return fmt.Sprintf("Nextcloud %s: HTTP %d", e.method, e.status) }
func (e *httpError) Unwrap() error {
	if e.status == http.StatusUnauthorized {
		return datasource.ErrInvalidCredentials
	}
	return datasource.ErrFetchFailed
}

func statusOf(err error) int {
	var e *httpError
	if errors.As(err, &e) {
		return e.status
	}
	return 0
}

func invalidResponse() error {
	return fmt.Errorf("%w: %w", datasource.ErrFetchFailed, errInvalidResponse)
}

func (c *client) request(ctx context.Context, method, p string, depth int, version entry) (*http.Response, error) {
	normalized, err := normalizePath(p)
	if err != nil {
		return nil, err
	}
	u := *c.base
	u.Path, u.RawPath = c.davPrefix+normalized, ""
	if method == "PROPFIND" && (depth < 0 || depth > 1) {
		return nil, fmt.Errorf("%w: PROPFIND depth must be 0 or 1", datasource.ErrInvalidConfig)
	}
	var body io.Reader
	if method == "PROPFIND" {
		body = strings.NewReader(propfindBody)
	}
	req, err := http.NewRequestWithContext(ctx, method, u.String(), body)
	if err != nil {
		return nil, invalidResponse()
	}
	req.SetBasicAuth(c.username, c.password)
	req.Header.Set("User-Agent", "WeKnora-Nextcloud-Connector/1.0")
	if method == "PROPFIND" {
		req.Header.Set("Depth", strconv.Itoa(depth))
		req.Header.Set("Content-Type", "application/xml; charset=utf-8")
	} else if version.ETag != "" && !strings.HasPrefix(version.ETag, "W/") {
		req.Header.Set("If-Match", version.ETag)
	} else if !version.ModifiedAt.IsZero() {
		req.Header.Set("If-Unmodified-Since", version.ModifiedAt.UTC().Format(http.TimeFormat))
	}
	resp, err := c.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if errors.Is(err, utils.ErrSSRFRedirectBlocked) {
			return nil, fmt.Errorf("%w: %w", datasource.ErrFetchFailed, errSSRFBlocked)
		}
		return nil, fmt.Errorf("%w: Nextcloud %s request failed", datasource.ErrFetchFailed, method)
	}
	return resp, nil
}

type davProperties struct {
	Resource *struct {
		Collection *struct{} `xml:"DAV: collection"`
	} `xml:"DAV: resourcetype"`
	ETag     *string `xml:"DAV: getetag"`
	Modified *string `xml:"DAV: getlastmodified"`
	Size     *string `xml:"DAV: getcontentlength"`
	FileID   *string `xml:"http://owncloud.org/ns fileid"`
}

type davResponse struct {
	Href      string `xml:"DAV: href"`
	Status    string `xml:"DAV: status"`
	Propstats []struct {
		Status string        `xml:"DAV: status"`
		Prop   davProperties `xml:"DAV: prop"`
	} `xml:"DAV: propstat"`
}

func (c *client) propfind(ctx context.Context, p string, depth int) ([]entry, error) {
	resp, err := c.request(ctx, "PROPFIND", p, depth, entry{})
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusMultiStatus {
		return nil, &httpError{method: "PROPFIND", status: resp.StatusCode}
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, listingLimit+1))
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, invalidResponse()
	}
	if int64(len(body)) > listingLimit {
		return nil, invalidResponse()
	}
	return c.parseListing(body, p, depth, maxListingEntries)
}

// WebDAV PROPFIND has no standard pagination. Read a complete, bounded level
// and reject truncation, partial statuses and duplicate/out-of-scope hrefs.
func (c *client) parseListing(body []byte, p string, depth, limit int) ([]entry, error) {
	var multi struct {
		XMLName   xml.Name      `xml:"DAV: multistatus"`
		Responses []davResponse `xml:"DAV: response"`
	}
	d := xml.NewDecoder(bytes.NewReader(body))
	if err := d.Decode(&multi); err != nil || len(multi.Responses) == 0 || len(multi.Responses) > limit {
		return nil, invalidResponse()
	}
	for {
		token, err := d.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, invalidResponse()
		}
		if data, ok := token.(xml.CharData); !ok || strings.TrimSpace(string(data)) != "" {
			return nil, invalidResponse()
		}
	}
	seen := make(map[string]bool, len(multi.Responses))
	out := make([]entry, 0, len(multi.Responses))
	for _, response := range multi.Responses {
		e, err := c.parseEntry(response)
		if err != nil || seen[e.Path] || (e.Path != p && (depth == 0 || path.Dir(e.Path) != p)) {
			return nil, invalidResponse()
		}
		seen[e.Path] = true
		out = append(out, e)
	}
	if !seen[p] {
		return nil, invalidResponse()
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

func (c *client) parseEntry(response davResponse) (entry, error) {
	u, err := url.Parse(response.Href)
	if err != nil || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" ||
		(u.Host != "" && (!strings.EqualFold(u.Host, c.base.Host) || !strings.EqualFold(u.Scheme, c.base.Scheme))) ||
		!strings.HasPrefix(u.Path, c.davPrefix+"/") {
		return entry{}, invalidResponse()
	}
	escaped := strings.ToLower(u.EscapedPath())
	if strings.Contains(escaped, "%2f") || strings.Contains(escaped, "%5c") {
		return entry{}, invalidResponse()
	}
	p, err := normalizePath(strings.TrimPrefix(u.Path, c.davPrefix))
	if err != nil || (response.Status != "" && !statusOK(response.Status)) {
		return entry{}, invalidResponse()
	}
	var props davProperties
	for _, stat := range response.Propstats {
		if !statusOK(stat.Status) {
			continue // Optional properties may be unsupported (404).
		}
		if stat.Prop.Resource != nil {
			props.Resource = stat.Prop.Resource
		}
		for _, pair := range [][2]**string{
			{&props.ETag, &stat.Prop.ETag},
			{&props.Modified, &stat.Prop.Modified},
			{&props.Size, &stat.Prop.Size},
			{&props.FileID, &stat.Prop.FileID},
		} {
			if *pair[1] != nil {
				if *pair[0] != nil && **pair[0] != **pair[1] {
					return entry{}, invalidResponse()
				}
				*pair[0] = *pair[1]
			}
		}
	}
	if props.Resource == nil {
		return entry{}, invalidResponse()
	}
	e := entry{Path: p, IsDir: props.Resource.Collection != nil}
	if props.ETag != nil {
		e.ETag = strings.TrimSpace(*props.ETag)
	}
	if strings.ContainsAny(e.ETag, "\r\n") {
		return entry{}, invalidResponse()
	}
	if props.FileID != nil {
		e.FileID = *props.FileID
	}
	if props.Modified != nil && *props.Modified != "" {
		e.ModifiedAt, err = http.ParseTime(*props.Modified)
		if err != nil {
			return entry{}, invalidResponse()
		}
	}
	if !e.IsDir {
		if props.Size == nil || (e.ETag == "" && e.ModifiedAt.IsZero()) {
			return entry{}, invalidResponse()
		}
		e.Size, err = strconv.ParseInt(*props.Size, 10, 64)
		if err != nil || e.Size < 0 {
			return entry{}, invalidResponse()
		}
	}
	return e, nil
}

func statusOK(status string) bool {
	fields := strings.Fields(status)
	return len(fields) >= 2 && strings.HasPrefix(fields[0], "HTTP/") && fields[1] == "200"
}

// download buffers only one file, reads at most limit+1 bytes and rejects a
// partial or changing representation instead of ingesting truncated content.
func (c *client) download(ctx context.Context, e entry, limit int64) ([]byte, error) {
	if e.Size > limit {
		return nil, errTooLarge
	}
	resp, err := c.request(ctx, http.MethodGet, e.Path, 0, e)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusPreconditionFailed {
		return nil, errSourceChanged
	}
	if resp.StatusCode != http.StatusOK {
		return nil, &httpError{method: http.MethodGet, status: resp.StatusCode}
	}
	if resp.ContentLength > limit {
		return nil, errTooLarge
	}
	if tag := resp.Header.Get("ETag"); e.ETag != "" && tag != "" && tag != e.ETag {
		return nil, errSourceChanged
	}
	if modified := resp.Header.Get("Last-Modified"); !e.ModifiedAt.IsZero() && modified != "" {
		t, err := http.ParseTime(modified)
		if err != nil || !t.Equal(e.ModifiedAt) {
			return nil, errSourceChanged
		}
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("%w: Nextcloud download interrupted", datasource.ErrFetchFailed)
	}
	switch {
	case int64(len(body)) > limit:
		return nil, errTooLarge
	case len(body) == 0:
		return nil, errEmptyFile
	case int64(len(body)) != e.Size:
		return nil, errSourceChanged
	}
	// Some proxies omit GET validators, and weak ETags cannot enforce If-Match.
	// Confirm the full fingerprint again before acknowledging this version.
	if strings.HasPrefix(e.ETag, "W/") || (e.ETag != "" && resp.Header.Get("ETag") == "") ||
		(!e.ModifiedAt.IsZero() && resp.Header.Get("Last-Modified") == "") {
		current, err := c.propfind(ctx, e.Path, 0)
		if err != nil {
			return nil, err
		}
		if current[0].IsDir || fingerprint(current[0]) != fingerprint(e) {
			return nil, errSourceChanged
		}
	}
	return body, nil
}
