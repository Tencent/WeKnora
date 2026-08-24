package utils

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

var defaultHTTPClient = NewSSRFSafeHTTPClient(SSRFSafeHTTPClientConfig{
	Timeout:      60 * time.Second,
	MaxRedirects: 10,
})

// DownloadBytes fetches the content at the given HTTP(S) URL and returns the
// raw bytes. It reuses a package-level http.Client with a 60-second timeout.
func DownloadBytes(url string) ([]byte, error) {
	if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
		return nil, fmt.Errorf("unsupported URL scheme: %s", url)
	}
	if err := ValidateURLForSSRF(url); err != nil {
		return nil, fmt.Errorf("URL rejected by SSRF policy: %w", err)
	}
	resp, err := defaultHTTPClient.Get(url)
	if err != nil {
		return nil, fmt.Errorf("HTTP GET: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d for %s", resp.StatusCode, url)
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}
	return data, nil
}
