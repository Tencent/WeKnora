package web_search

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// NewSearchHTTPClient builds an http.Client for outbound web search requests.
// If proxyURL is non-empty, all requests use that HTTP/HTTPS/SOCKS5 proxy; otherwise
// the transport uses ProxyFromEnvironment (HTTP_PROXY / HTTPS_PROXY etc.).
func NewSearchHTTPClient(timeout time.Duration, proxyURL string) (*http.Client, error) {
	proxyURL = strings.TrimSpace(proxyURL)
	transport, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return nil, fmt.Errorf("default HTTP transport is not *http.Transport")
	}
	t := transport.Clone()
	if proxyURL != "" {
		u, err := url.Parse(proxyURL)
		if err != nil {
			return nil, fmt.Errorf("invalid proxy_url: %w", err)
		}
		if u.Scheme == "" || u.Host == "" {
			return nil, fmt.Errorf("invalid proxy_url: scheme and host are required")
		}
		t.Proxy = http.ProxyURL(u)
	} else {
		t.Proxy = http.ProxyFromEnvironment
	}
	return &http.Client{
		Timeout:   timeout,
		Transport: t,
	}, nil
}
