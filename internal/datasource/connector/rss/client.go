package rss

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	readability "codeberg.org/readeck/go-readability/v2"
	"github.com/Tencent/WeKnora/internal/utils"
)

const (
	// requestTimeout bounds a single feed or article fetch.
	requestTimeout = 20 * time.Second

	// maxFeedSize caps the feed body to avoid memory blowups on hostile feeds.
	maxFeedSize = 10 * 1024 * 1024 // 10 MB

	// maxArticleSize caps a single article page body.
	maxArticleSize = 5 * 1024 * 1024 // 5 MB

	// defaultUserAgent is sent on every request; some feeds reject empty UAs.
	defaultUserAgent = "Mozilla/5.0 (compatible; WeKnora-RSS/1.0; +https://weknora.weixin.qq.com)"
)

// client performs SSRF-safe HTTP fetches with optional custom headers.
type client struct {
	httpClient *http.Client
	headers    map[string]string
}

func newClient(headers map[string]string) *client {
	cfg := utils.DefaultSSRFSafeHTTPClientConfig()
	cfg.Timeout = requestTimeout
	return &client{
		httpClient: utils.NewSSRFSafeHTTPClient(cfg),
		headers:    headers,
	}
}

// fetch retrieves rawURL with SSRF validation and size limiting. Custom auth
// headers are only attached when withAuthHeaders is true (feed fetches); article
// pages on third-party domains must not receive feed credentials.
func (c *client) fetch(ctx context.Context, rawURL string, maxSize int64, withAuthHeaders bool) ([]byte, error) {
	body, _, err := c.fetchWithURL(ctx, rawURL, maxSize, withAuthHeaders)
	return body, err
}

// fetchWithURL also returns the final response URL after redirects, which is
// the base against which relative article links and images must be resolved.
func (c *client) fetchWithURL(
	ctx context.Context, rawURL string, maxSize int64, withAuthHeaders bool,
) ([]byte, *url.URL, error) {
	if err := utils.ValidateURLForSSRF(rawURL); err != nil {
		return nil, nil, fmt.Errorf("URL rejected: %w", err)
	}
	if _, err := url.Parse(rawURL); err != nil {
		return nil, nil, fmt.Errorf("invalid URL: %w", err)
	}

	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, nil, err
	}

	if withAuthHeaders {
		for k, v := range c.headers {
			req.Header.Set(k, v)
		}
	}
	if req.Header.Get("User-Agent") == "" {
		req.Header.Set("User-Agent", defaultUserAgent)
	}
	if req.Header.Get("Accept") == "" {
		req.Header.Set("Accept",
			"application/rss+xml, application/atom+xml, application/xml, text/xml, application/json, text/html;q=0.9, */*;q=0.8")
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, nil, fmt.Errorf("fetch failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, nil, fmt.Errorf("HTTP %d %s", resp.StatusCode, resp.Status)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxSize+1))
	if err != nil {
		return nil, nil, fmt.Errorf("read body failed: %w", err)
	}
	if int64(len(body)) > maxSize {
		return nil, nil, fmt.Errorf("response exceeds maximum size (%d bytes)", maxSize)
	}
	return body, resp.Request.URL, nil
}

// fetchFeed retrieves the raw bytes of a feed document.
func (c *client) fetchFeed(ctx context.Context, feedURL string) ([]byte, error) {
	return c.fetch(ctx, feedURL, maxFeedSize, true)
}

// extractArticle fetches an article page and returns the readability-cleaned
// main content as HTML, plus the extracted title (may be empty). Returns an
// error if the page can't be fetched or no readable content is found, so the
// caller can fall back to feed-provided content.
func (c *client) extractArticle(ctx context.Context, articleURL string) (contentHTML, title string, err error) {
	body, pageURL, err := c.fetchWithURL(ctx, articleURL, maxArticleSize, false)
	if err != nil {
		return "", "", err
	}

	article, err := readability.FromReader(bytes.NewReader(body), pageURL)
	if err != nil {
		return "", "", fmt.Errorf("readability parse: %w", err)
	}
	if article.Node == nil {
		return "", "", fmt.Errorf("no readable content extracted")
	}

	var buf bytes.Buffer
	if err := article.RenderHTML(&buf); err != nil {
		return "", "", fmt.Errorf("render article html: %w", err)
	}
	return buf.String(), article.Title(), nil
}
