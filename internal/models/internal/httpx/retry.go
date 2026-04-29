// Package httpx provides shared HTTP helpers used by the LLM sub-packages
// (chat / embedding / rerank / vlm / asr). It centralizes the POST-with-retry
// pattern that was previously copy-pasted across ~11 provider implementations.
//
// The helpers deliberately stop at "successful *http.Response" — callers own
// body decoding with their own DTOs, because response shapes differ widely
// between vendors (OpenAI vs Aliyun DashScope vs Volcengine multimodal, …).
package httpx

import (
	"bytes"
	"context"
	"net/http"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	secutils "github.com/Tencent/WeKnora/internal/utils"
)

// defaultClient is used when a POSTRequest doesn't supply its own HTTP client.
// Timeout covers the whole call (request + response body read) — this is fine
// for embedding / rerank; streaming chat uses its own transport elsewhere.
var defaultClient = &http.Client{Timeout: 60 * time.Second}

// POSTRequest describes a single POST call with retry semantics.
// All fields except URL are optional; sensible defaults are applied.
type POSTRequest struct {
	URL        string
	Body       []byte
	Headers    map[string]string // Authorization / Content-Type / custom headers
	MaxRetries int               // default 3 attempts after the initial one fails

	// LogPrefix is prepended to retry log lines, e.g. "OpenAIEmbedder".
	// Kept free-form so existing log-based alerting rules on "<Type> retrying"
	// keep firing after the consolidation.
	LogPrefix string

	// HTTPClient overrides defaultClient. Chat streaming uses a connection-level
	// timeout client (see chat package) which is passed in here.
	HTTPClient *http.Client

	// CustomHeaders, when non-nil, are applied via secutils.ApplyCustomHeaders
	// on every attempt after the base Headers are set. Reserved headers (e.g.
	// Authorization) are automatically skipped by ApplyCustomHeaders.
	CustomHeaders map[string]string

	// HeaderBuilder, when non-nil, is called on every attempt and returns the
	// headers to set. Used by signed providers (WeKnoraCloud) whose signature
	// depends on the body bytes and a per-request timestamp/nonce.
	// When set, Headers and CustomHeaders still apply first, then HeaderBuilder
	// overrides.
	HeaderBuilder func(body []byte) (map[string]string, error)
}

// DoPOST performs a POST with exponential-backoff retry.
//
// Retry schedule (seconds): 1, 2, 4, 8, 10 (capped). The initial attempt runs
// immediately. Up to MaxRetries+1 attempts total; MaxRetries<=0 means a single
// attempt. Retries only trigger on transport errors (err != nil from Do); 4xx
// and 5xx HTTP responses are returned to the caller as-is so the caller can
// inspect the body.
//
// The returned *http.Response's Body MUST be closed by the caller.
func DoPOST(ctx context.Context, req POSTRequest) (*http.Response, error) {
	client := req.HTTPClient
	if client == nil {
		client = defaultClient
	}
	maxRetries := max(req.MaxRetries, 0)

	var resp *http.Response
	var err error

	for attempt := 0; attempt <= maxRetries; attempt++ {
		if attempt > 0 {
			backoff := backoffFor(attempt)
			if req.LogPrefix != "" {
				logger.GetLogger(ctx).Infof("%s retrying request (%d/%d), waiting %v",
					req.LogPrefix, attempt, maxRetries, backoff)
			}
			select {
			case <-time.After(backoff):
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}

		httpReq, rerr := http.NewRequestWithContext(ctx, http.MethodPost, req.URL, bytes.NewReader(req.Body))
		if rerr != nil {
			if req.LogPrefix != "" {
				logger.GetLogger(ctx).Errorf("%s failed to create request: %v", req.LogPrefix, rerr)
			}
			err = rerr
			continue
		}

		// Base headers: Content-Type defaults to application/json when not set
		// by the caller — every provider we have uses JSON bodies.
		if _, ok := req.Headers["Content-Type"]; !ok {
			httpReq.Header.Set("Content-Type", "application/json")
		}
		for k, v := range req.Headers {
			httpReq.Header.Set(k, v)
		}
		secutils.ApplyCustomHeaders(httpReq, req.CustomHeaders)
		if req.HeaderBuilder != nil {
			signed, berr := req.HeaderBuilder(req.Body)
			if berr != nil {
				return nil, berr
			}
			for k, v := range signed {
				httpReq.Header.Set(k, v)
			}
		}

		resp, err = client.Do(httpReq)
		if err == nil {
			return resp, nil
		}

		if req.LogPrefix != "" {
			logger.GetLogger(ctx).Errorf("%s request failed (attempt %d/%d): %v",
				req.LogPrefix, attempt+1, maxRetries+1, err)
		}
	}
	return nil, err
}

// backoffFor returns the sleep duration for the given attempt (1-based).
// Same schedule as the previous copy-pasted impl: 1s, 2s, 4s, 8s, capped at 10s.
func backoffFor(attempt int) time.Duration {
	d := min(time.Duration(1<<uint(attempt-1))*time.Second, 10*time.Second)
	return d
}
