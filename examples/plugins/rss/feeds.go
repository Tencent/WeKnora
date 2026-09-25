package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	readability "codeberg.org/readeck/go-readability/v2"
	htmltomd "github.com/JohannesKaufmann/html-to-markdown/v2"
	"github.com/mmcdole/gofeed"
)

const (
	requestTimeout   = 20 * time.Second
	maxFeedSize      = 10 << 20
	maxArticleSize   = 5 << 20
	defaultUserAgent = "Mozilla/5.0 (compatible; WeKnora-RSS/1.0; +https://weknora.weixin.qq.com)"
	maxFileNameBytes = 200
)

// config is the data source configuration: feed URLs in settings, optional
// auth headers (which may carry secrets) in credentials.
type config struct {
	feedURLs    []string
	authHeaders map[string]string
}

func parseConfig(settings, credentials map[string]any) (*config, error) {
	raw, _ := settings["feed_urls"].(string)
	if strings.TrimSpace(raw) == "" {
		// Older rows kept the URLs with the credentials.
		raw, _ = credentials["feed_urls"].(string)
	}
	cfg := &config{feedURLs: splitURLs(raw)}
	if len(cfg.feedURLs) == 0 {
		return nil, errMissingFeeds
	}
	headers, _ := credentials["auth_headers"].(string)
	cfg.authHeaders = parseHeaders(headers)
	return cfg, nil
}

// splitURLs splits on newlines and commas, trims, dedupes (order kept).
func splitURLs(raw string) []string {
	parts := strings.FieldsFunc(raw, func(r rune) bool { return r == '\n' || r == '\r' || r == ',' })
	seen := map[string]bool{}
	var out []string
	for _, u := range parts {
		u = strings.TrimSpace(u)
		if u == "" || seen[u] {
			continue
		}
		seen[u] = true
		out = append(out, u)
	}
	return out
}

func parseHeaders(raw string) map[string]string {
	headers := map[string]string{}
	for _, line := range strings.Split(raw, "\n") {
		name, value, ok := strings.Cut(strings.TrimSpace(line), ":")
		name = strings.TrimSpace(name)
		if !ok || name == "" {
			continue
		}
		headers[name] = strings.TrimSpace(value)
	}
	if len(headers) == 0 {
		return nil
	}
	return headers
}

// fetcher downloads feeds and article pages. Outbound traffic goes through
// the plugin host's egress proxy (HTTPS_PROXY), which refuses private
// addresses, so the plugin needs no SSRF logic of its own.
type fetcher struct {
	http    *http.Client
	headers map[string]string
}

func newFetcher(headers map[string]string) *fetcher {
	return &fetcher{http: &http.Client{Timeout: requestTimeout}, headers: headers}
}

// fetch attaches auth headers only to feed fetches; article pages on
// third-party sites must not receive feed credentials.
func (f *fetcher) fetch(ctx context.Context, rawURL string, maxSize int64, withAuth bool) ([]byte, error) {
	u, err := url.Parse(rawURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return nil, fmt.Errorf("invalid URL %q", rawURL)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	if withAuth {
		for k, v := range f.headers {
			req.Header.Set(k, v)
		}
	}
	if req.Header.Get("User-Agent") == "" {
		req.Header.Set("User-Agent", defaultUserAgent)
	}
	if req.Header.Get("Accept") == "" {
		req.Header.Set("Accept", "application/rss+xml, application/atom+xml, application/xml, text/xml, "+
			"application/json, text/html;q=0.9, */*;q=0.8")
	}
	resp, err := f.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("HTTP %d %s", resp.StatusCode, resp.Status)
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

func (f *fetcher) feed(ctx context.Context, feedURL string) (*gofeed.Feed, error) {
	data, err := f.fetch(ctx, feedURL, maxFeedSize, true)
	if err != nil {
		return nil, err
	}
	feed, err := gofeed.NewParser().Parse(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("parse feed: %w", err)
	}
	return feed, nil
}

// article returns the readability-cleaned main content of a page as HTML.
func (f *fetcher) article(ctx context.Context, articleURL string) (string, string, error) {
	body, err := f.fetch(ctx, articleURL, maxArticleSize, false)
	if err != nil {
		return "", "", err
	}
	pageURL, _ := url.Parse(articleURL)
	art, err := readability.FromReader(bytes.NewReader(body), pageURL)
	if err != nil {
		return "", "", fmt.Errorf("readability parse: %w", err)
	}
	if art.Node == nil {
		return "", "", fmt.Errorf("no readable content extracted")
	}
	var buf bytes.Buffer
	if err := art.RenderHTML(&buf); err != nil {
		return "", "", fmt.Errorf("render article html: %w", err)
	}
	return buf.String(), art.Title(), nil
}

// cursorState is the incremental sync state, the same shape the builtin
// connector keeps: feed URL → item ID → content fingerprint, and feed URL →
// item ID → feed-only signal that lets unchanged entries skip article fetches.
type cursorState struct {
	FeedItems   map[string]map[string]string
	FeedSignals map[string]map[string]string
}

func newCursorState() *cursorState {
	return &cursorState{FeedItems: map[string]map[string]string{}, FeedSignals: map[string]map[string]string{}}
}

func cursorFromState(state map[string]any) *cursorState {
	c := newCursorState()
	read := func(key string, into map[string]map[string]string) {
		feeds, _ := state[key].(map[string]any)
		for feedURL, items := range feeds {
			m, _ := items.(map[string]any)
			into[feedURL] = map[string]string{}
			for id, v := range m {
				if s, ok := v.(string); ok {
					into[feedURL][id] = s
				}
			}
		}
	}
	read("feed_items", c.FeedItems)
	read("feed_signals", c.FeedSignals)
	return c
}

func (c *cursorState) toState() map[string]any {
	return map[string]any{"feed_items": c.FeedItems, "feed_signals": c.FeedSignals}
}

func (c *cursorState) copyFeed(prev *cursorState, feedURL string) {
	if prev == nil {
		return
	}
	if src := prev.FeedItems[feedURL]; len(src) > 0 {
		c.FeedItems[feedURL] = maps.Clone(src)
	}
	if src := prev.FeedSignals[feedURL]; len(src) > 0 {
		c.FeedSignals[feedURL] = maps.Clone(src)
	}
}

func contentFingerprint(markdown string) string {
	sum := sha256.Sum256([]byte(markdown))
	return "h:" + hex.EncodeToString(sum[:])[:16]
}

func feedSignal(item *gofeed.Item, feedContent string) string {
	var b strings.Builder
	b.WriteString(item.GUID + "\n" + item.Link + "\n" + item.Title + "\n")
	if item.UpdatedParsed != nil && !item.UpdatedParsed.IsZero() {
		b.WriteString(item.UpdatedParsed.UTC().Format(time.RFC3339))
	}
	b.WriteByte('\n')
	if item.PublishedParsed != nil && !item.PublishedParsed.IsZero() {
		b.WriteString(item.PublishedParsed.UTC().Format(time.RFC3339))
	}
	b.WriteByte('\n')
	b.WriteString(feedContent)
	sum := sha256.Sum256([]byte(b.String()))
	return "s:" + hex.EncodeToString(sum[:])[:16]
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func htmlToMarkdown(html string) string {
	if strings.TrimSpace(html) == "" {
		return ""
	}
	md, err := htmltomd.ConvertString(html)
	if err != nil || strings.TrimSpace(md) == "" {
		return strings.TrimSpace(html)
	}
	return strings.TrimSpace(md)
}

var fileNameReplacer = strings.NewReplacer(
	"/", "_", "\\", "_", ":", "_", "*", "_", "?", "_", "\"", "_", "<", "_", ">", "_", "|", "_",
)

// fileName makes a feed title safe to use as a file name, the way WeKnora's
// builtin connectors do: hostile punctuation replaced, at most 200 bytes
// without splitting a rune.
func fileName(title string) string {
	title = strings.TrimSpace(strings.NewReplacer("\n", " ", "\r", " ", "\t", " ").Replace(title))
	if title == "" {
		return "untitled.md"
	}
	name := fileNameReplacer.Replace(title)
	if len(name) > maxFileNameBytes {
		name = name[:maxFileNameBytes]
		for len(name) > 0 {
			r, size := utf8.DecodeLastRuneInString(name)
			if r != utf8.RuneError || size != 1 {
				break
			}
			name = name[:len(name)-1]
		}
	}
	return name + ".md"
}
