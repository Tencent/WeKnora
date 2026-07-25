// Package web_fetch provides a public URL content fetcher with SSRF protection.
package web_fetch

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
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
	maxBodySize  = 100 * 1024
)

// ErrorCode identifies the stage and class of a fetch failure.
type ErrorCode string

const (
	ErrorInvalidURL       ErrorCode = "invalid_url"
	ErrorDNS              ErrorCode = "dns_failed"
	ErrorTimeout          ErrorCode = "connection_timeout"
	ErrorTLS              ErrorCode = "tls_failed"
	ErrorHTTP403          ErrorCode = "http_403"
	ErrorHTTP429          ErrorCode = "http_429"
	ErrorHTTP5xx          ErrorCode = "http_5xx"
	ErrorHTTPStatus       ErrorCode = "http_status"
	ErrorSSRFRejected     ErrorCode = "ssrf_rejected"
	ErrorRedirectRejected ErrorCode = "redirect_rejected"
	ErrorRead             ErrorCode = "read_failed"
	ErrorHTMLParse        ErrorCode = "html_parse_failed"
	ErrorEmptyContent     ErrorCode = "empty_content"
	ErrorConnection       ErrorCode = "connection_failed"
)

// FetchError carries stable, machine-readable failure details.
type FetchError struct {
	Code      ErrorCode
	Retryable bool
	Err       error
}

func (e *FetchError) Error() string {
	if e == nil {
		return ""
	}
	if e.Err == nil {
		return string(e.Code)
	}
	return e.Err.Error()
}

func (e *FetchError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

// ErrorDetails returns stable fields suitable for tool responses and logs.
func ErrorDetails(err error) (ErrorCode, bool, string) {
	if err == nil {
		return "", false, ""
	}
	var fetchErr *FetchError
	if errors.As(err, &fetchErr) {
		return fetchErr.Code, fetchErr.Retryable, fetchErr.Error()
	}
	return ErrorConnection, true, err.Error()
}

// Fetcher fetches and extracts public web pages through an SSRF-safe client.
type Fetcher struct {
	client      *http.Client
	timeout     time.Duration
	maxBodySize int64
	validateURL func(string) error
}

// NewFetcher creates a production fetcher with DNS and redirect SSRF guards.
func NewFetcher() *Fetcher {
	return &Fetcher{
		client: utils.NewSSRFSafeHTTPClient(utils.SSRFSafeHTTPClientConfig{
			Timeout:      fetchTimeout,
			MaxRedirects: 10,
		}),
		timeout:     fetchTimeout,
		maxBodySize: maxBodySize,
		validateURL: utils.ValidateURLForSSRF,
	}
}

// FetchURLContent preserves the existing package-level API.
func FetchURLContent(ctx context.Context, rawURL string) (string, error) {
	return NewFetcher().Fetch(ctx, rawURL)
}

// Fetch downloads a page and returns clean text content.
func (f *Fetcher) Fetch(ctx context.Context, rawURL string) (string, error) {
	if strings.TrimSpace(rawURL) == "" {
		return "", newFetchError(ErrorInvalidURL, false, "url is empty")
	}
	parsedURL, err := url.Parse(rawURL)
	if err != nil {
		return "", newFetchError(ErrorInvalidURL, false, "invalid URL: %v", err)
	}
	if (parsedURL.Scheme != "http" && parsedURL.Scheme != "https") || parsedURL.Hostname() == "" {
		return "", newFetchError(ErrorInvalidURL, false, "invalid URL format")
	}
	if err := f.validateURL(rawURL); err != nil {
		return "", classifyValidationError(err)
	}

	requestCtx, cancel := context.WithTimeout(ctx, f.timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(requestCtx, http.MethodGet, rawURL, nil)
	if err != nil {
		return "", newFetchError(ErrorInvalidURL, false, "invalid URL: %v", err)
	}
	setBrowserHeaders(req, parsedURL)

	resp, err := f.client.Do(req)
	if err != nil {
		return "", classifyRequestError(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", classifyHTTPStatus(resp.StatusCode, resp.Status)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, f.maxBodySize))
	if err != nil {
		return "", newFetchError(ErrorRead, true, "read failed: %v", err)
	}
	if len(body) == 0 {
		return "", newFetchError(ErrorEmptyContent, false, "page body is empty")
	}

	content, err := htmlToText(string(body))
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(content) == "" {
		return "", newFetchError(ErrorEmptyContent, false, "page contains no readable text")
	}

	logger.Infof(ctx, "[WebFetch] fetched %s → %d chars", rawURL, len(content))
	return content, nil
}

func setBrowserHeaders(req *http.Request, parsedURL *url.URL) {
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,image/apng,*/*;q=0.8,application/signed-exchange;v=b3;q=0.7")
	req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9,en;q=0.8,en-GB;q=0.7,en-US;q=0.6")
	req.Header.Set("Accept-Encoding", "identity")
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
	req.Header.Set("Referer", parsedURL.Scheme+"://"+parsedURL.Hostname()+"/")
}

func classifyHTTPStatus(statusCode int, status string) error {
	switch {
	case statusCode == http.StatusForbidden:
		return newFetchError(ErrorHTTP403, false, "HTTP %s", status)
	case statusCode == http.StatusTooManyRequests:
		return newFetchError(ErrorHTTP429, true, "HTTP %s", status)
	case statusCode >= http.StatusInternalServerError:
		return newFetchError(ErrorHTTP5xx, true, "HTTP %s", status)
	default:
		return newFetchError(ErrorHTTPStatus, false, "HTTP %s", status)
	}
}

func classifyValidationError(err error) error {
	message := strings.ToLower(err.Error())
	if strings.Contains(message, "dns resolution failed") || strings.Contains(message, "dns lookup failed") {
		return newFetchError(ErrorDNS, true, "DNS lookup failed: %v", err)
	}
	return newFetchError(ErrorSSRFRejected, false, "URL rejected: %v", err)
}

func classifyRequestError(err error) error {
	if errors.Is(err, context.DeadlineExceeded) {
		return newFetchError(ErrorTimeout, true, "fetch timed out: %v", err)
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return newFetchError(ErrorDNS, true, "DNS lookup failed: %v", err)
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return newFetchError(ErrorTimeout, true, "fetch timed out: %v", err)
	}
	var unknownAuthority x509.UnknownAuthorityError
	var certificateInvalid x509.CertificateInvalidError
	var hostnameError x509.HostnameError
	var recordHeaderError tls.RecordHeaderError
	if errors.As(err, &unknownAuthority) || errors.As(err, &certificateInvalid) ||
		errors.As(err, &hostnameError) || errors.As(err, &recordHeaderError) {
		return newFetchError(ErrorTLS, false, "TLS validation failed: %v", err)
	}
	if errors.Is(err, utils.ErrSSRFRedirectBlocked) {
		return newFetchError(ErrorRedirectRejected, false, "redirect rejected: %v", err)
	}
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		redirectMessage := strings.ToLower(urlErr.Err.Error())
		if strings.Contains(redirectMessage, "redirect") || strings.Contains(redirectMessage, "stopped after") {
			return newFetchError(ErrorRedirectRejected, false, "redirect rejected: %v", err)
		}
	}

	message := strings.ToLower(err.Error())
	if strings.Contains(message, "connection blocked:") {
		return newFetchError(ErrorSSRFRejected, false, "URL rejected: %v", err)
	}
	if strings.Contains(message, "certificate") || strings.Contains(message, "tls") {
		return newFetchError(ErrorTLS, false, "TLS validation failed: %v", err)
	}
	return newFetchError(ErrorConnection, true, "fetch failed: %v", err)
}

func newFetchError(code ErrorCode, retryable bool, format string, args ...interface{}) error {
	return &FetchError{Code: code, Retryable: retryable, Err: fmt.Errorf(format, args...)}
}

func htmlToText(html string) (string, error) {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		fallback := stripTags(html)
		if fallback == "" {
			return "", newFetchError(ErrorHTMLParse, false, "HTML parse failed: %v", err)
		}
		return fallback, nil
	}
	doc.Find("script, style, nav, footer, header, iframe, noscript, svg, img").Remove()

	var builder strings.Builder
	doc.Find("body").Each(func(_ int, selection *goquery.Selection) {
		builder.WriteString(selection.Text())
	})
	lines := strings.Split(builder.String(), "\n")
	cleaned := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" {
			cleaned = append(cleaned, line)
		}
	}
	return strings.Join(cleaned, "\n"), nil
}

func stripTags(html string) string {
	var builder strings.Builder
	inTag := false
	for _, char := range html {
		switch char {
		case '<':
			inTag = true
		case '>':
			inTag = false
		default:
			if !inTag {
				builder.WriteRune(char)
			}
		}
	}
	return strings.TrimSpace(builder.String())
}
