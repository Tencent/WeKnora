package embedding

import (
	"fmt"
	"net/http"
	"time"

	secutils "github.com/Tencent/WeKnora/internal/utils"
)

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
	return secutils.NewSSRFSafeHTTPClient(cfg)
}
