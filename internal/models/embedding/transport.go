package embedding

import (
	"fmt"
	"net/http"
	"time"

	secutils "github.com/Tencent/WeKnora/internal/utils"
)

// sharedEmbeddingHTTPTransport keeps a single SSRF-safe connection pool for
// all embedding clients. Embedders are recreated as model configuration changes,
// but their outbound connections can be safely reused across client instances.
var sharedEmbeddingHTTPTransport = func() http.RoundTripper {
	cfg := secutils.DefaultSSRFSafeHTTPClientConfig()
	return secutils.NewSSRFSafeHTTPClient(cfg).Transport
}()

// validateEmbeddingBaseURL checks that a resolved embedding API base URL is safe
// for outbound requests. Empty URLs are allowed (callers apply provider defaults).
func validateEmbeddingBaseURL(baseURL string) error {
	if baseURL == "" {
		return nil
	}
	if err := secutils.ValidateURLForSSRF(baseURL); err != nil {
		return fmt.Errorf("base URL SSRF check failed: %w", err)
	}
	return nil
}

// newEmbeddingHTTPClient returns an HTTP client with connection-level SSRF
// protection and redirect validation, aligned with internal/models/chat/transport.go.
func newEmbeddingHTTPClient(timeout time.Duration) *http.Client {
	cfg := secutils.DefaultSSRFSafeHTTPClientConfig()
	cfg.Timeout = timeout
	client := secutils.NewSSRFSafeHTTPClient(cfg)
	client.Transport = sharedEmbeddingHTTPTransport
	return client
}
