package rerank

import (
	"fmt"
	"net/http"
	"time"

	secutils "github.com/Tencent/WeKnora/internal/utils"
)

func validateRerankBaseURL(baseURL string) error {
	if baseURL == "" {
		return nil
	}
	if err := secutils.ValidateURLForSSRF(baseURL); err != nil {
		return fmt.Errorf("base URL SSRF check failed: %w", err)
	}
	return nil
}

// sharedRerankHTTPTransport keeps a single SSRF-safe connection pool for all
// rerank clients. Rerankers are recreated on every GetRerankModel call, but
// their outbound connections can be safely reused, so the transport (and its
// keep-alive pool) is built once at package load.
var sharedRerankHTTPTransport = secutils.NewSSRFSafeTransport(
	secutils.DefaultSSRFSafeHTTPClientConfig(),
)

// newRerankHTTPClient returns an HTTP client with connection-level SSRF
// protection and redirect validation. All clients share
// sharedRerankHTTPTransport so keep-alive connections are pooled globally,
// while each keeps its own timeout.
func newRerankHTTPClient(timeout time.Duration) *http.Client {
	cfg := secutils.DefaultSSRFSafeHTTPClientConfig()
	cfg.Timeout = timeout
	return secutils.NewSSRFSafeHTTPClientWithTransport(cfg, sharedRerankHTTPTransport)
}
