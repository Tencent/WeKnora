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
	maxJSONResponseBytes  int64 = 20 << 20
	requestAttempts             = 4
	maxErrorResponseRunes       = 1000
	maxRetryDelay               = 60 * time.Second
	maxPaginationHops           = 10000
	// maxEmptyServerPages bounds how far a Server/Data Center page listing
	// follows _links.next through empty pages. Some builds keep returning
	// next past the end of a space (#3596); permission filtering can also
	// empty one page mid-listing, so a single empty page is not the end.
	maxEmptyServerPages = 3
	// maxDeletionProbes caps how many unlisted pages one sync looks up
	// individually after a cut-short listing; the rest wait for the next run.
	maxDeletionProbes = 200
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

// pages lists every current page in a space. complete is false when a Server
// listing was cut short after maxEmptyServerPages empty pages that still
// linked onward; the caller must not treat pages missing from such a listing
// as deleted.
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

// pageInSpace reports whether a Server page is still a current page of the
// space. Confluence answers 404 for a page that was deleted or moved to the
// trash; a page that is no longer current or now belongs to another space is
// gone from this one too, matching what a complete listing would omit.
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
