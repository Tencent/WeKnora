package opds

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/utils"
)

const (
	// requestTimeout bounds a single catalog or book fetch.
	requestTimeout = 60 * time.Second

	// maxFeedSize caps a catalog document to avoid memory blowups on hostile feeds.
	maxFeedSize = 10 * 1024 * 1024 // 10 MB

	// maxPages bounds rel="next" pagination so a looping or hostile catalog
	// cannot spin forever.
	maxPages = 20

	// defaultUserAgent is sent on every request; some catalogs reject empty UAs.
	defaultUserAgent = "Mozilla/5.0 (compatible; WeKnora-OPDS/1.0; +https://weknora.weixin.qq.com)"

	// acceptHeader advertises the OPDS catalog and common acquisition formats.
	acceptHeader = "application/atom+xml;profile=opds-catalog, application/atom+xml, " +
		"application/epub+zip, application/pdf, text/html;q=0.9, */*;q=0.8"
)

// client performs SSRF-safe HTTP fetches with host-scoped credentials.
type client struct {
	httpClient   *http.Client
	basicUser    string
	basicPass    string
	hasBasicAuth bool
	headers      map[string]string

	// credentialedHosts is the set of hosts (hostname + effective port) derived
	// from the configured catalog URLs. Credentials are attached ONLY to these
	// hosts: a private catalog's Basic credentials must never be sent to a
	// third-party acquisition or CDN host that happens to serve a book file.
	credentialedHosts map[string]struct{}
}

// newClient builds a fetch client from the connector config.
func newClient(cfg *Config) *client {
	clientCfg := utils.DefaultSSRFSafeHTTPClientConfig()
	clientCfg.Timeout = requestTimeout

	c := &client{
		httpClient:        utils.NewSSRFSafeHTTPClient(clientCfg),
		headers:           cfg.parseHeaders(),
		credentialedHosts: make(map[string]struct{}),
	}
	if cfg != nil {
		c.basicUser = strings.TrimSpace(cfg.Username)
		c.basicPass = cfg.Password
		c.hasBasicAuth = cfg.hasBasicAuth()
		for _, raw := range cfg.catalogURLList() {
			if u, err := url.Parse(raw); err == nil && u.Host != "" {
				c.credentialedHosts[hostKey(u)] = struct{}{}
			}
		}
	}
	return c
}

// hostKey normalizes a URL's host to "hostname:effective-port" so the
// comparison is exact (never a suffix match, which would let evil-example.com
// match example.com).
func hostKey(u *url.URL) string {
	host := strings.ToLower(u.Hostname())
	port := u.Port()
	if port == "" {
		if strings.EqualFold(u.Scheme, "https") {
			port = "443"
		} else {
			port = "80"
		}
	}
	return host + ":" + port
}

// maySendCredentials reports whether rawURL is on a host the user configured as
// a catalog, and therefore may receive the configured credentials.
func (c *client) maySendCredentials(rawURL string) bool {
	u, err := url.Parse(rawURL)
	if err != nil || u.Host == "" {
		return false
	}
	_, ok := c.credentialedHosts[hostKey(u)]
	return ok
}

// fetch retrieves rawURL with SSRF validation and size limiting. Credentials
// are attached only when the target host is one of the configured catalogs.
func (c *client) fetch(ctx context.Context, rawURL string, maxSize int64) ([]byte, error) {
	if err := utils.ValidateURLForSSRF(rawURL); err != nil {
		return nil, fmt.Errorf("URL rejected: %w", err)
	}
	if _, err := url.Parse(rawURL); err != nil {
		return nil, fmt.Errorf("invalid URL: %w", err)
	}

	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}

	if c.maySendCredentials(rawURL) {
		if c.hasBasicAuth {
			req.SetBasicAuth(c.basicUser, c.basicPass)
		}
		for k, v := range c.headers {
			req.Header.Set(k, v)
		}
	}
	if req.Header.Get("User-Agent") == "" {
		req.Header.Set("User-Agent", defaultUserAgent)
	}
	if req.Header.Get("Accept") == "" {
		req.Header.Set("Accept", acceptHeader)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("HTTP %d %s", resp.StatusCode, resp.Status)
	}

	// Reject oversized bodies early when the server declares a length.
	if maxSize > 0 && resp.ContentLength > maxSize {
		return nil, fmt.Errorf("response exceeds maximum size (%d bytes)", maxSize)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxSize+1))
	if err != nil {
		return nil, fmt.Errorf("read body failed: %w", err)
	}
	if int64(len(body)) > maxSize {
		return nil, fmt.Errorf("response exceeds maximum size (%d bytes)", maxSize)
	}
	return body, nil
}

// fetchFeed retrieves and parses one catalog document.
func (c *client) fetchFeed(ctx context.Context, feedURL string) (*opdsFeed, error) {
	body, err := c.fetch(ctx, feedURL, maxFeedSize)
	if err != nil {
		return nil, err
	}
	return parseFeed(body)
}

// fetchBook downloads an acquisition file, capped at maxSize bytes.
func (c *client) fetchBook(ctx context.Context, bookURL string, maxSize int64) ([]byte, error) {
	return c.fetch(ctx, bookURL, maxSize)
}

// walkPages fetches a feed and follows its rel="next" pagination links,
// invoking fn for every page. It stops at maxPages and on a repeated URL so a
// catalog that points its next link back at itself cannot loop forever.
func (c *client) walkPages(
	ctx context.Context, feedURL string, fn func(*opdsFeed, *url.URL) error,
) error {
	seen := make(map[string]struct{})
	current := feedURL

	for page := 0; page < maxPages; page++ {
		if _, ok := seen[current]; ok {
			return nil
		}
		seen[current] = struct{}{}

		feed, err := c.fetchFeed(ctx, current)
		if err != nil {
			return err
		}
		base, _ := url.Parse(current)
		if err := fn(feed, base); err != nil {
			return err
		}

		next := nextLink(feed)
		if next == nil {
			return nil
		}
		resolved, err := resolveHref(base, next.Href)
		if err != nil {
			return nil // an unresolvable next link ends pagination
		}
		current = resolved
	}
	return nil
}
