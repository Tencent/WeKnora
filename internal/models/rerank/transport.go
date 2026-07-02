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

func newRerankHTTPClient(timeout time.Duration) *http.Client {
	cfg := secutils.DefaultSSRFSafeHTTPClientConfig()
	cfg.Timeout = timeout
	return secutils.NewSSRFSafeHTTPClient(cfg)
}
