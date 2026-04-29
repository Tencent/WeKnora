package httpx

import (
	"net/http"
	"time"

	secutils "github.com/Tencent/WeKnora/internal/utils"
)

// StreamingClient is the shared HTTP client used for raw LLM calls where we
// manage the body ourselves (streaming chat + custom provider request bodies).
//
// It sets connection-level timeouts only — no overall request timeout — so
// long-running streams are bounded by context cancellation, not by the client.
// DialContext uses the SSRF-safe dialer so DNS-rebinding style attacks are
// rejected at the connection layer as well as by URL validation.
var StreamingClient = &http.Client{
	Transport: &http.Transport{
		Proxy:               http.ProxyFromEnvironment,
		DialContext:         secutils.SSRFSafeDialContext,
		TLSHandshakeTimeout: 10 * time.Second,
		IdleConnTimeout:     90 * time.Second,
		MaxIdleConnsPerHost: 5,
	},
}
