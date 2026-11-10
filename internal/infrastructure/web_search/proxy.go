package web_search

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/utils"
)

// ValidateProxyURL delegates to utils.ValidateURLForSSRF (only http/https pass that check).
func ValidateProxyURL(proxyURL string) error {
	proxyURL = strings.TrimSpace(proxyURL)
	if proxyURL == "" {
		return nil
	}
	return utils.ValidateURLForSSRF(proxyURL)
}

// NewSearchHTTPClient builds an http.Client for outbound web search requests.
// It uses utils.SSRFSafeDialContext, optional explicit or environment proxy, and
// redirect validation consistent with utils.NewSSRFSafeHTTPClient.
func NewSearchHTTPClient(timeout time.Duration, proxyURL string) (*http.Client, error) {
	proxyURL = strings.TrimSpace(proxyURL)
	def, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return nil, fmt.Errorf("default HTTP transport is not *http.Transport")
	}
	t := def.Clone()
	t.DialContext = utils.SSRFSafeDialContext

	if proxyURL != "" {
		if err := ValidateProxyURL(proxyURL); err != nil {
			return nil, err
		}
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

	cfg := utils.DefaultSSRFSafeHTTPClientConfig()
	cfg.Timeout = timeout
	return utils.NewSSRFSafeHTTPClientWithTransport(cfg, t), nil
}
