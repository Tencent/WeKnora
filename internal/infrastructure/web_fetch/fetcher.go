// Package web_fetch provides a public URL content fetcher with SSRF protection.
// It extracts core logic from the agent WebFetchTool so it can be used by the chat pipeline.
package web_fetch

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/utils"
)

const (
	fetchTimeout = 15 * time.Second
	maxBodySize  = 100 * 1024 // 100KB
)

// FetchURLContent fetches a URL and returns its text content (HTML converted to clean text).
// Includes SSRF validation, DNS pinning, browser-like headers, and content size limits.
func FetchURLContent(ctx context.Context, rawURL string) (string, error) {
	if rawURL == "" {
		return "", fmt.Errorf("url is empty")
	}

	// SSRF validation
	if err := utils.ValidateURLForSSRF(rawURL); err != nil {
		return "", fmt.Errorf("URL rejected: %w", err)
	}

	u, err := url.Parse(rawURL)
	if err != nil {
		return "", fmt.Errorf("invalid URL: %w", err)
	}
	hostname := u.Hostname()
	port := u.Port()
	if port == "" {
		if u.Scheme == "https" {
			port = "443"
		} else {
			port = "80"
		}
	}

	// DNS pinning: resolve once, use pinned IP
	ips, err := net.DefaultResolver.LookupIP(context.Background(), "ip", hostname)
	if err != nil || len(ips) == 0 {
		return "", fmt.Errorf("DNS lookup failed for %s: %w", hostname, err)
	}
	var pinnedIP net.IP
	for _, ip := range ips {
		if utils.IsPublicIP(ip) {
			pinnedIP = ip
			break
		}
	}
	if pinnedIP == nil {
		return "", fmt.Errorf("no public IP for host %s", hostname)
	}

	// Build request with pinned IP
	hostPort := net.JoinHostPort(pinnedIP.String(), port)
	fetchURL := *u
	fetchURL.Host = hostPort

	ctx, cancel := context.WithTimeout(ctx, fetchTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fetchURL.String(), nil)
	if err != nil {
		return "", err
	}
	req.Host = hostname

	// Browser-like headers to reduce 403 rejections.
	// These match a real Chrome browser fingerprint.
	req.Header.Set("User-Agent",
		"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36")
	req.Header.Set("Accept",
		"text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,image/apng,*/*;q=0.8,application/signed-exchange;v=b3;q=0.7")
	req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9,en;q=0.8,en-GB;q=0.7,en-US;q=0.6")
	req.Header.Set("Accept-Encoding", "identity") // no gzip to simplify reading
	req.Header.Set("Cache-Control", "no-cache")
	req.Header.Set("Pragma", "no-cache")
	req.Header.Set("Sec-Ch-Ua", `"Chromium";v="131", "Not_A Brand";v="24"`)
	req.Header.Set("Sec-Ch-Ua-Mobile", "?0")
	req.Header.Set("Sec-Ch-Ua-Platform", `"macOS"`)
	req.Header.Set("Sec-Fetch-Dest", "document")
	req.Header.Set("Sec-Fetch-Mode", "navigate")
	req.Header.Set("Sec-Fetch-Site", "none")
	req.Header.Set("Sec-Fetch-User", "?1")
	req.Header.Set("Upgrade-Insecure-Requests", "1")
	req.Header.Set("Referer", u.Scheme+"://"+hostname+"/")

	// Custom transport: TLS ServerName for certificate validation with pinned IP.
	client := &http.Client{
		Timeout: fetchTimeout,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				ServerName: hostname,
			},
		},
		// Follow redirects (default behavior), up to 10 hops
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("fetch failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %d %s", resp.StatusCode, resp.Status)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodySize))
	if err != nil {
		return "", fmt.Errorf("read failed: %w", err)
	}

	text := htmlToText(string(body))
	logger.Infof(ctx, "[WebFetch] fetched %s → %d chars", rawURL, len(text))
	return text, nil
}

// htmlToText extracts clean text from HTML, removing scripts/styles/nav.
func htmlToText(html string) string {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		return stripTags(html)
	}
	doc.Find("script, style, nav, footer, header, iframe, noscript, svg, img").Remove()

	var sb strings.Builder
	doc.Find("body").Each(func(i int, s *goquery.Selection) {
		sb.WriteString(s.Text())
	})
	text := sb.String()

	// Normalize whitespace: collapse blank lines
	lines := strings.Split(text, "\n")
	var cleaned []string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" {
			cleaned = append(cleaned, line)
		}
	}
	return strings.Join(cleaned, "\n")
}

func stripTags(s string) string {
	var sb strings.Builder
	inTag := false
	for _, r := range s {
		if r == '<' {
			inTag = true
		} else if r == '>' {
			inTag = false
		} else if !inTag {
			sb.WriteRune(r)
		}
	}
	return sb.String()
}
