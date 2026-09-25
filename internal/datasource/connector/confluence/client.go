package confluence

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	urlpath "path"
	"strconv"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/datasource"
)

const (
	maxJSONResponseBytes         int64 = 20 << 20
	maxImageBytes                int64 = 10 << 20
	maxImageRedirects                  = 10
	requestAttempts                    = 4
	maxErrorResponseRunes              = 1000
	maxRetryDelay                      = 60 * time.Second
	maxPaginationHops                  = 10000
	maxTraversalNodes                  = 100000
	maxTransparentTraversalNodes       = 10000
	maxTransparentTraversalDepth       = 32
	// maxEmptyServerPages bounds how far a Server/Data Center page listing
	// follows _links.next through empty pages.
	maxEmptyServerPages = 3
	maxDeletionProbes   = 200
)

var (
	errImageTooLarge        = errors.New("confluence image exceeds size limit")
	errImageNonImageContent = errors.New("confluence image response is not an image")
	// errImageRedirectOffOrigin fires whenever a redirect target leaves the base
	// URL boundary resolveEndpoint enforces — a different origin, or the same host
	// but a path outside the configured context path (e.g. /wiki → /other-app).
	errImageRedirectOffOrigin = errors.New("confluence image redirect left the configured base URL")
)

type apiError struct {
	endpoint string
	status   int
	excerpt  string
}

func (e *apiError) Error() string {
	if e.excerpt == "" {
		return fmt.Sprintf("confluence API %s: status %d", e.endpoint, e.status)
	}
	return fmt.Sprintf("confluence API %s: status %d body=%q", e.endpoint, e.status, e.excerpt)
}

type client struct {
	cfg  config
	http *http.Client
}

func newClient(cfg config) (*client, error) {
	if err := datasource.ValidateConnectorBaseURL(cfg.baseURL); err != nil {
		return nil, err
	}
	return &client{cfg: cfg, http: datasource.NewConnectorHTTPClient(60 * time.Second)}, nil
}

func (c *client) get(ctx context.Context, endpoint string, output interface{}) error {
	fullURL, err := c.resolveEndpoint(endpoint)
	if err != nil {
		return err
	}
	for attempt := 0; attempt < requestAttempts; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, fullURL, nil)
		if err != nil {
			return err
		}
		req.SetBasicAuth(c.cfg.username, c.cfg.secret)
		req.Header.Set("Accept", "application/json")
		resp, err := c.http.Do(req)
		if err == nil {
			body, readErr := io.ReadAll(io.LimitReader(resp.Body, maxJSONResponseBytes+1))
			_ = resp.Body.Close()
			if readErr != nil {
				return readErr
			}
			if int64(len(body)) > maxJSONResponseBytes {
				return fmt.Errorf("confluence response exceeds %d MiB limit", maxJSONResponseBytes>>20)
			}
			if resp.StatusCode >= 200 && resp.StatusCode < 300 {
				if output == nil {
					return nil
				}
				return json.Unmarshal(body, output)
			}
			if resp.StatusCode != http.StatusTooManyRequests && resp.StatusCode < 500 {
				return &apiError{
					endpoint: endpoint,
					status:   resp.StatusCode,
					excerpt:  responseExcerpt(body),
				}
			}
			if attempt == requestAttempts-1 {
				return &apiError{endpoint: endpoint, status: resp.StatusCode}
			}
			if err = waitRetry(ctx, resp.Header.Get("Retry-After"), attempt); err != nil {
				return err
			}
			continue
		}
		if attempt == requestAttempts-1 {
			return fmt.Errorf("confluence API %s: %w", endpoint, err)
		}
		if err = waitRetry(ctx, "", attempt); err != nil {
			return err
		}
	}
	return fmt.Errorf("confluence API %s: retry exhausted", endpoint)
}

func responseExcerpt(body []byte) string {
	value := []rune(strings.TrimSpace(string(body)))
	if len(value) <= maxErrorResponseRunes {
		return string(value)
	}
	return string(value[:maxErrorResponseRunes]) + "..."
}

func waitRetry(ctx context.Context, retryAfter string, attempt int) error {
	backoff := time.Duration(1<<attempt)*time.Second + time.Duration(time.Now().UnixNano()%250)*time.Millisecond
	delay := capRetryDelay(backoff)
	if secs, err := strconv.Atoi(retryAfter); err == nil {
		delay = capRetryDelay(time.Duration(secs) * time.Second)
	} else if t, err := http.ParseTime(retryAfter); err == nil {
		delay = capRetryDelay(time.Until(t))
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func capRetryDelay(delay time.Duration) time.Duration {
	if delay <= 0 {
		return 100 * time.Millisecond
	}
	if delay > maxRetryDelay {
		return maxRetryDelay
	}
	return delay
}

func (c *client) resolveEndpoint(endpoint string) (string, error) {
	base, err := url.Parse(c.cfg.baseURL)
	if err != nil {
		return "", fmt.Errorf("parse Confluence base URL: %w", err)
	}
	next, err := url.Parse(endpoint)
	if err != nil {
		return "", fmt.Errorf("parse Confluence pagination URL: %w", err)
	}
	basePath := strings.TrimRight(urlpath.Clean(base.Path), "/")
	if basePath == "." {
		basePath = ""
	}
	if next.IsAbs() {
		if next.Scheme != base.Scheme || next.Host != base.Host {
			return "", fmt.Errorf("confluence pagination URL leaves configured origin")
		}
		cleaned, err := joinContextPath(basePath, next.Path, false)
		if err != nil {
			return "", err
		}
		next.Path = cleaned
		next.RawPath = ""
		return next.String(), nil
	}
	cleaned, err := joinContextPath(basePath, next.Path, true)
	if err != nil {
		return "", err
	}
	return (&url.URL{Scheme: base.Scheme, Host: base.Host, Path: cleaned, RawQuery: next.RawQuery}).String(), nil
}

func joinContextPath(basePath, rawPath string, prependContext bool) (string, error) {
	joined := rawPath
	if joined == "" {
		joined = basePath
	} else if prependContext && basePath != "" && joined != basePath && !strings.HasPrefix(joined, basePath+"/") {
		joined = basePath + "/" + strings.TrimLeft(joined, "/")
	}
	cleaned := urlpath.Clean(joined)
	if cleaned == "." {
		cleaned = "/"
	}
	if basePath != "" && cleaned != basePath && !strings.HasPrefix(cleaned, basePath+"/") {
		return "", fmt.Errorf("confluence pagination URL leaves configured context path")
	}
	return cleaned, nil
}

func (c *client) paginate(ctx context.Context, start string, step func(pageURL string) (next string, err error)) error {
	next := start
	seen := make(map[string]struct{}, 8)
	for next != "" {
		if err := ctx.Err(); err != nil {
			return err
		}
		resolved, err := c.resolveEndpoint(next)
		if err != nil {
			return err
		}
		if _, dup := seen[resolved]; dup {
			return fmt.Errorf("confluence pagination looped")
		}
		if len(seen) >= maxPaginationHops {
			return fmt.Errorf("confluence pagination exceeded %d pages", maxPaginationHops)
		}
		seen[resolved] = struct{}{}
		next, err = step(next)
		if err != nil {
			return err
		}
	}
	return nil
}

func (c *client) resourceURL(link string) string {
	if strings.TrimSpace(link) == "" {
		return c.cfg.baseURL
	}
	resolved, err := c.resolveEndpoint(link)
	if err != nil {
		return c.cfg.baseURL
	}
	return resolved
}

// sameOrigin reports whether u shares scheme and host with the configured base
// URL. Only same-origin URLs may carry Confluence credentials.
func (c *client) sameOrigin(u *url.URL) bool {
	if u == nil {
		return false
	}
	base, err := url.Parse(c.cfg.baseURL)
	if err != nil {
		return false
	}
	return strings.EqualFold(base.Scheme, u.Scheme) && strings.EqualFold(base.Host, u.Host)
}

// withinImageScope reports whether u stays inside the boundary resolveEndpoint
// enforces for the initial image URL: the same origin AND a path inside the
// configured context path. Redirect targets are validated with this same rule,
// not merely sameOrigin, because Go forwards Basic Auth on a same-host redirect —
// so a hop like /wiki/download → /other-app/image must fail closed too.
func (c *client) withinImageScope(u *url.URL) bool {
	if u == nil {
		return false
	}
	_, err := c.resolveEndpoint(u.String())
	return err == nil
}

// downloadImage fetches a same-origin private image using the connector's
// credentials. It streams with a hard size cap, fails closed if a redirect left
// the Confluence origin, and validates the response really is an image. It is a
// best-effort single attempt: a failed image degrades to keeping its original
// URL rather than retrying or failing the page.
func (c *client) downloadImage(ctx context.Context, absURL string) ([]byte, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, absURL, nil)
	if err != nil {
		return nil, "", err
	}
	req.SetBasicAuth(c.cfg.username, c.cfg.secret)
	req.Header.Set("Accept", "image/*,*/*;q=0.8")
	resp, err := c.imageHTTPClient().Do(req)
	if err != nil {
		return nil, "", err
	}
	defer func() { _ = resp.Body.Close() }()
	// Backstop for the redirect policy: imageHTTPClient's CheckRedirect already
	// refuses any hop that leaves the base before it is followed, so this only
	// re-verifies the final URL when the transport populated resp.Request. A custom
	// RoundTripper may leave it nil, in which case there is no followed redirect to
	// re-check and the download is allowed to proceed.
	if resp.Request != nil && !c.withinImageScope(resp.Request.URL) {
		return nil, "", errImageRedirectOffOrigin
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, "", &apiError{endpoint: "image", status: resp.StatusCode}
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxImageBytes+1))
	if err != nil {
		return nil, "", err
	}
	if int64(len(body)) > maxImageBytes {
		return nil, "", errImageTooLarge
	}
	mime, ok := imageContentType(resp.Header.Get("Content-Type"), body)
	if !ok {
		return nil, "", errImageNonImageContent
	}
	return body, mime, nil
}

// imageHTTPClient reuses the connector's SSRF-safe transport and timeout but
// pins redirects to the configured Confluence base, so a private-image fetch can
// never be bounced to a third party or to another app on the same host. The
// redirect check fails closed: any hop that resolveEndpoint would reject — a
// foreign origin, or a same-host path outside the context path (which Go would
// still send Basic Auth to) — is refused outright rather than merely stripped of
// credentials.
func (c *client) imageHTTPClient() *http.Client {
	return &http.Client{
		Transport: c.http.Transport,
		Timeout:   c.http.Timeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= maxImageRedirects {
				return errors.New("confluence image stopped after too many redirects")
			}
			if !c.withinImageScope(req.URL) {
				return errImageRedirectOffOrigin
			}
			return nil
		},
	}
}

// imageContentType normalizes a response Content-Type to an image MIME type. An
// empty or generic binary type falls back to content sniffing; any explicit
// non-image type (for example the text/html login page Confluence returns when a
// token expired) is rejected so it is never stored as an image.
func imageContentType(header string, body []byte) (string, bool) {
	ct := strings.ToLower(strings.TrimSpace(header))
	if i := strings.Index(ct, ";"); i >= 0 {
		ct = strings.TrimSpace(ct[:i])
	}
	switch {
	case strings.HasPrefix(ct, "image/"):
		return ct, true
	case ct != "" && ct != "application/octet-stream":
		return "", false
	}
	if sniff := strings.ToLower(http.DetectContentType(body)); strings.HasPrefix(sniff, "image/") {
		if i := strings.Index(sniff, ";"); i >= 0 {
			sniff = strings.TrimSpace(sniff[:i])
		}
		return sniff, true
	}
	return "", false
}

func (c *client) ping(ctx context.Context) error {
	if c.cfg.cloud() {
		return c.get(ctx, "/api/v2/spaces?limit=1", nil)
	}
	return c.get(ctx, "/rest/api/space?limit=1", nil)
}

func (c *client) spaces(ctx context.Context) ([]space, error) {
	if c.cfg.cloud() {
		var all []space
		err := c.paginate(ctx, "/api/v2/spaces?limit=250", func(pageURL string) (string, error) {
			var result spaceList
			if err := c.get(ctx, pageURL, &result); err != nil {
				return "", err
			}
			all = append(all, result.Results...)
			return result.Links.Next, nil
		})
		return all, err
	}
	var all []space
	err := c.paginate(ctx, "/rest/api/space?limit=100", func(pageURL string) (string, error) {
		var result serverSpaceList
		if err := c.get(ctx, pageURL, &result); err != nil {
			return "", err
		}
		for _, v := range result.Results {
			all = append(all, space{ID: v.ID.String(), Key: v.Key, Name: v.Name, Links: v.Links})
		}
		return result.Links.Next, nil
	})
	return all, err
}

const serverPageExpand = "version,space"

func serverSpacePagesEndpoint(spaceKey string) string {
	query := url.Values{
		"expand": []string{serverPageExpand},
		"limit":  []string{"100"},
	}
	return "/rest/api/space/" + url.PathEscape(spaceKey) + "/content/page?" + query.Encode()
}

// withServerPageExpand restores expand=version,space on pagination URLs.
// Confluence _links.next is typically ?limit=&start= and drops expand, which
// would make later pages look versionless ("t:") and skip real edits.
func withServerPageExpand(next string) string {
	parsed, err := url.Parse(next)
	if err != nil {
		return next
	}
	query := parsed.Query()
	if strings.TrimSpace(query.Get("expand")) == "" {
		query.Set("expand", serverPageExpand)
		parsed.RawQuery = query.Encode()
	}
	return parsed.String()
}

func withCloudPageQuery(next string) string {
	parsed, err := url.Parse(next)
	if err != nil {
		return next
	}
	query := parsed.Query()
	if strings.TrimSpace(query.Get("status")) == "" {
		query.Set("status", "current")
	}
	if strings.TrimSpace(query.Get("depth")) == "" {
		query.Set("depth", "all")
	}
	parsed.RawQuery = query.Encode()
	return parsed.String()
}

func (c *client) pages(ctx context.Context, s space) (pages []page, complete bool, err error) {
	if c.cfg.cloud() {
		var all []page
		start := "/api/v2/spaces/" + url.PathEscape(s.ID) + "/pages?status=current&depth=all&limit=250"
		err := c.paginate(ctx, start, func(pageURL string) (string, error) {
			var result cloudPageList
			if err := c.get(ctx, pageURL, &result); err != nil {
				return "", err
			}
			for _, value := range result.Results {
				p := page{ID: value.ID, Title: value.Title, Status: value.Status, Links: value.Links}
				p.Space.Key, p.Space.Name = s.Key, s.Name
				p.Version.Number, p.Version.CreatedAt = value.Version.Number, value.Version.CreatedAt
				if p.Version.CreatedAt == "" {
					p.Version.CreatedAt = value.CreatedAt
				}
				all = append(all, p)
			}
			next := result.Links.Next
			if next != "" {
				next = withCloudPageQuery(next)
			}
			return next, nil
		})
		return all, true, err
	}
	var all []page
	complete = true
	emptyPages := 0
	err = c.paginate(ctx, serverSpacePagesEndpoint(s.Key), func(pageURL string) (string, error) {
		var result serverSpacePageList
		if err := c.get(ctx, pageURL, &result); err != nil {
			return "", err
		}
		listedPages := result.pages()
		for _, listed := range listedPages {
			if listed.Space.Key == "" {
				listed.Space.Key, listed.Space.Name = s.Key, s.Name
			}
			all = append(all, listed)
		}
		next := result.nextLink()
		if next == "" {
			return "", nil
		}
		if len(listedPages) > 0 {
			emptyPages = 0
		} else {
			emptyPages++
			if emptyPages >= maxEmptyServerPages {
				complete = false
				return "", nil
			}
		}
		return withServerPageExpand(next), nil
	})
	return all, complete, err
}

func (c *client) pageInSpace(ctx context.Context, id, spaceKey string) (bool, error) {
	var result struct {
		Status string `json:"status"`
		Space  struct {
			Key string `json:"key"`
		} `json:"space"`
	}
	err := c.get(ctx, "/rest/api/content/"+url.PathEscape(id)+"?expand=space", &result)
	var apiErr *apiError
	if errors.As(err, &apiErr) && apiErr.status == http.StatusNotFound {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if result.Status != "" && result.Status != "current" {
		return false, nil
	}
	return result.Space.Key == "" || result.Space.Key == spaceKey, nil
}

func (c *client) body(ctx context.Context, id string) (pageBody, error) {
	if c.cfg.cloud() {
		var result pageBody
		err := c.get(ctx, "/api/v2/pages/"+url.PathEscape(id)+"?body-format=view", &result)
		return result, err
	}
	var result pageBody
	err := c.get(ctx, "/rest/api/content/"+url.PathEscape(id)+"?expand=body.view,version,space", &result)
	return result, err
}

// pageDetail obtains identity, ownership, and version without requiring a
// rendered body. Scope enumeration must remain complete when a body is
// temporarily unavailable, otherwise a transient content error would be
// indistinguishable from a missing page during deletion reconciliation.
func (c *client) pageDetail(ctx context.Context, id string) (page, error) {
	var result page
	if c.cfg.cloud() {
		err := c.get(ctx, "/api/v2/pages/"+url.PathEscape(id), &result)
		return result, err
	}
	err := c.get(ctx, "/rest/api/content/"+url.PathEscape(id)+"?expand=version,space", &result)
	return result, err
}

// validatePageOwnership performs the constant-cost check required before a
// picker expansion. Ancestors are deliberately not fetched here: they are only
// needed by ResolveResourceAncestors and buildSyncPlan.
func (c *client) validatePageOwnership(ctx context.Context, s space, pageID string) error {
	full, err := c.pageDetail(ctx, pageID)
	if err != nil {
		return err
	}
	if c.cfg.cloud() {
		if full.SpaceID != s.ID {
			return fmt.Errorf("page %s belongs to space %s, not %s", pageID, full.SpaceID, s.ID)
		}
		return nil
	}
	if full.Space.Key == "" || full.Space.Key != s.Key {
		return fmt.Errorf("page %s does not belong to space %s", pageID, s.ID)
	}
	return nil
}

func serverChildPagesEndpoint(pageID string) string {
	query := url.Values{"expand": []string{serverPageExpand}, "limit": []string{"100"}}
	return "/rest/api/content/" + url.PathEscape(pageID) + "/child/page?" + query.Encode()
}

func serverTopLevelPagesEndpoint(spaceKey string) string {
	query := url.Values{"depth": []string{"root"}, "expand": []string{serverPageExpand}, "limit": []string{"100"}}
	return "/rest/api/space/" + url.PathEscape(spaceKey) + "/content/page?" + query.Encode()
}

func (c *client) listServerPages(ctx context.Context, endpoint string) ([]page, error) {
	start, _ := url.Parse(endpoint)
	startQuery := start.Query()
	var all []page
	err := c.paginate(ctx, endpoint, func(pageURL string) (string, error) {
		var result pageList
		if err := c.get(ctx, pageURL, &result); err != nil {
			return "", err
		}
		all = append(all, result.Results...)
		next := result.Links.Next
		if next != "" {
			next = withServerListQuery(next, startQuery)
		}
		return next, nil
	})
	return all, err
}

// Server pagination links commonly retain only start/limit. Restore the query
// that defines the listing scope (notably depth=root) as well as expand.
func withServerListQuery(next string, required url.Values) string {
	parsed, err := url.Parse(next)
	if err != nil {
		return next
	}
	query := parsed.Query()
	for key, values := range required {
		if query.Get(key) == "" && len(values) > 0 {
			query[key] = values
		}
	}
	parsed.RawQuery = query.Encode()
	return parsed.String()
}

func (c *client) listCloudHierarchy(ctx context.Context, endpoint string) ([]page, error) {
	var all []page
	err := c.paginate(ctx, endpoint, func(pageURL string) (string, error) {
		var result cloudHierarchyList
		if err := c.get(ctx, pageURL, &result); err != nil {
			return "", err
		}
		for _, item := range result.Results {
			all = append(
				all,
				page{
					ID:      item.ID,
					Title:   item.Title,
					Status:  item.Status,
					Kind:    item.Type,
					SpaceID: item.SpaceID,
					Links:   item.Links,
				},
			)
		}
		return result.Links.Next, nil
	})
	return all, err
}

// topLevelPages retrieves one level only. The Server/DC depth=root endpoint
// is paginated by the server; it deliberately never falls back to scanning all
// pages because that would defeat lazy selection for large spaces.
//
// Cloud applies the same lazy contract: depth=0 returns only root-level pages,
// and Cloud exposes no endpoint to enumerate a space's top-level folders
// (CONFCLOUD-84275), so pages nested directly under those containers stay
// unselectable in the picker for now. Recovering them would require a
// whole-space scan per expand, which is exactly what lazy loading forbids;
// selecting the whole space still syncs them. The connector flags this
// limitation through resource metadata so the picker can surface it.
func (c *client) topLevelPages(ctx context.Context, s space) ([]page, error) {
	var pages []page
	var err error
	if c.cfg.cloud() {
		pages, err = c.listCloudHierarchy(
			ctx,
			"/api/v2/spaces/"+url.PathEscape(s.ID)+"/pages?status=current&depth=0&limit=250",
		)
		if err == nil {
			visible := pages[:0]
			for _, page := range pages {
				if page.Kind == "" || page.Kind == "page" {
					visible = append(visible, page)
				}
			}
			pages = visible
		}
	} else {
		pages, err = c.listServerPages(ctx, serverTopLevelPagesEndpoint(s.Key))
	}
	if err == nil {
		return pages, nil
	}
	// The depth=root navigation hint is Server/DC-specific; a Cloud 400/404 has
	// different causes and must surface the raw API error for diagnosis.
	if !c.cfg.cloud() {
		var api *apiError
		if errors.As(err, &api) && (api.status == http.StatusBadRequest || api.status == http.StatusNotFound) {
			return nil, fmt.Errorf(
				"confluence server does not support complete top-level page navigation for this space; "+
					"select the Space to sync all pages: %w",
				err,
			)
		}
	}
	return nil, err
}

func (c *client) directChildPages(ctx context.Context, _ space, pageID string) ([]page, error) {
	if c.cfg.cloud() {
		return c.visibleCloudPageChildren(ctx, pageID)
	}
	return c.listServerPages(ctx, serverChildPagesEndpoint(pageID))
}

func cloudDirectChildrenEndpoint(kind, id string) (string, error) {
	plural := map[string]string{
		"page": "pages", "folder": "folders", "database": "databases", "whiteboard": "whiteboards", "embed": "embeds",
	}[kind]
	if plural == "" {
		return "", fmt.Errorf("unsupported Confluence Cloud hierarchy type %q", kind)
	}
	return "/api/v2/" + plural + "/" + url.PathEscape(id) + "/direct-children?limit=250", nil
}

// visibleCloudPageChildren projects Cloud's mixed-content hierarchy onto a
// Page-only picker. Containers remain connector-internal and are traversed
// until the next visible Page is found.
func (c *client) visibleCloudPageChildren(ctx context.Context, pageID string) ([]page, error) {
	return c.walkCloudPageHierarchy(ctx, pageID, false)
}

// walkCloudPageHierarchy traverses Cloud's mixed content tree through
// direct-children endpoints. Picker expansion stops at visible pages, while a
// sync walk continues through them to return the complete page subtree.
func (c *client) walkCloudPageHierarchy(ctx context.Context, pageID string, descendPages bool) ([]page, error) {
	type queuedNode struct {
		id, kind string
		depth    int
	}
	queue := []queuedNode{{id: pageID, kind: "page"}}
	visited := map[string]struct{}{"page:" + pageID: {}}
	pages := make([]page, 0)
	nodeLimit := maxTransparentTraversalNodes
	if descendPages {
		nodeLimit = maxTraversalNodes
	}
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		endpoint, err := cloudDirectChildrenEndpoint(current.kind, current.id)
		if err != nil {
			return nil, err
		}
		children, err := c.listCloudHierarchy(ctx, endpoint)
		if err != nil {
			return nil, err
		}
		for _, child := range children {
			kind := child.Kind
			if kind == "" {
				kind = "page"
			}
			key := kind + ":" + child.ID
			if child.ID == "" || (kind == "page" && child.ID == pageID) {
				continue
			}
			if _, exists := visited[key]; exists {
				continue
			}
			if len(visited) >= nodeLimit {
				return nil, fmt.Errorf(
					"confluence Cloud hierarchy traversal exceeds %d nodes",
					nodeLimit,
				)
			}
			visited[key] = struct{}{}
			if kind == "page" {
				pages = append(pages, child)
				if !descendPages {
					continue
				}
			}
			depth := 0
			if kind != "page" {
				depth = current.depth + 1
				if current.kind == "page" {
					depth = 1
				}
			}
			if depth > maxTransparentTraversalDepth {
				return nil, fmt.Errorf(
					"confluence Cloud transparent traversal exceeds depth %d",
					maxTransparentTraversalDepth,
				)
			}
			if _, err := cloudDirectChildrenEndpoint(kind, child.ID); err != nil {
				return nil, err
			}
			queue = append(queue, queuedNode{id: child.ID, kind: kind, depth: depth})
		}
	}
	return pages, nil
}

// pageAncestors validates that pageID belongs to s and returns its path from
// the space root. A selected page is fetched even if it is top-level, so a
// forged page:{space}:{page} ID cannot cross a space boundary.
func (c *client) pageAncestors(ctx context.Context, s space, pageID string) ([]page, error) {
	if c.cfg.cloud() {
		full, err := c.pageDetail(ctx, pageID)
		if err != nil {
			return nil, err
		}
		if full.SpaceID != s.ID {
			return nil, fmt.Errorf("page %s belongs to space %s, not %s", pageID, full.SpaceID, s.ID)
		}
		ancestors, err := c.listCloudHierarchy(ctx, "/api/v2/pages/"+url.PathEscape(pageID)+"/ancestors?limit=250")
		if err != nil {
			return nil, err
		}
		return ancestors, nil
	}
	var full page
	if err := c.get(ctx, "/rest/api/content/"+url.PathEscape(pageID)+"?expand=ancestors,space", &full); err != nil {
		return nil, err
	}
	if full.Space.Key == "" || full.Space.Key != s.Key {
		return nil, fmt.Errorf("page %s does not belong to space %s", pageID, s.ID)
	}
	return full.Ancestors, nil
}

// pageSubtree uses direct-child traversal for Server/DC because that endpoint
// is stable across supported versions. Cloud uses the same complete
// mixed-content hierarchy walk as the picker, but continues through visible
// pages to include every page in the selected subtree.
func (c *client) pageSubtree(ctx context.Context, s space, pageID string) ([]page, error) {
	full, err := c.pageDetail(ctx, pageID)
	if err != nil {
		return nil, err
	}
	if c.cfg.cloud() {
		if full.SpaceID != s.ID {
			return nil, fmt.Errorf("page %s does not belong to space %s", pageID, s.ID)
		}
	} else if full.Space.Key == "" || full.Space.Key != s.Key {
		return nil, fmt.Errorf("page %s does not belong to space %s", pageID, s.ID)
	}
	root := full
	if c.cfg.cloud() {
		descendants, err := c.walkCloudPageHierarchy(ctx, pageID, true)
		if err != nil {
			return nil, err
		}
		out := []page{root}
		for _, descendant := range descendants {
			// Direct-child responses are intentionally compact. Hydrate each page
			// before version comparison; an unknown version must never be treated
			// as an unchanged stable token.
			detail, err := c.pageDetail(ctx, descendant.ID)
			if err != nil {
				return nil, err
			}
			if detail.SpaceID != s.ID {
				return nil, fmt.Errorf("page %s does not belong to space %s", descendant.ID, s.ID)
			}
			out = append(out, detail)
		}
		return out, nil
	}
	queue, out := []page{root}, []page{root}
	seen := map[string]bool{root.ID: true}
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		children, err := c.directChildPages(ctx, s, current.ID)
		if err != nil {
			return nil, err
		}
		for _, child := range children {
			if child.ID == "" || seen[child.ID] {
				continue
			}
			if len(seen) >= maxTraversalNodes {
				return nil, fmt.Errorf("confluence page subtree exceeds %d nodes", maxTraversalNodes)
			}
			seen[child.ID] = true
			if child.SpaceID != "" && child.SpaceID != s.ID {
				return nil, fmt.Errorf("page %s does not belong to space %s", child.ID, s.ID)
			}
			if !c.cfg.cloud() && child.Space.Key != "" && child.Space.Key != s.Key {
				return nil, fmt.Errorf("page %s does not belong to space %s", child.ID, s.ID)
			}
			queue, out = append(queue, child), append(out, child)
		}
	}
	return out, nil
}
