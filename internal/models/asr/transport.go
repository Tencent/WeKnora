package asr

import (
	"fmt"
	"net/http"
	"time"

	"github.com/Tencent/WeKnora/internal/ipclass"
	secutils "github.com/Tencent/WeKnora/internal/utils"
)

func validateASRBaseURL(baseURL string) error {
	if baseURL == "" {
		return nil
	}
	if err := secutils.ValidateURLForSSRFWithPolicy(baseURL, ipclass.ProviderMode); err != nil {
		return fmt.Errorf("base URL SSRF check failed: %w", err)
	}
	return nil
}

func newASRHTTPClient(timeout time.Duration) *http.Client {
	cfg := secutils.DefaultSSRFSafeHTTPClientConfig()
	cfg.Timeout = timeout
	return secutils.NewSSRFSafeHTTPClientWithTransportAndPolicy(cfg, nil, ipclass.ProviderMode)
}
