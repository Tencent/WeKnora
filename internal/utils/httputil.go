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
// Responses exceeding the configured MAX_FILE_SIZE_MB limit are rejected.
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
	maxBytes := GetMaxFileSize()
	if resp.ContentLength > maxBytes {
		return nil, fmt.Errorf("download size exceeds limit of %d bytes (MAX_FILE_SIZE_MB)", maxBytes)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}
	if int64(len(data)) > maxBytes {
		return nil, fmt.Errorf("download size exceeds limit of %d bytes (MAX_FILE_SIZE_MB)", maxBytes)
	}
	return data, nil
}
