package embedding

import (
	"context"
	"errors"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

// This file centralizes retry / backoff policy for embedding API calls.
//
// Rationale: providers enforce TPM/RPM quotas (e.g. 429 tpm_rate_limit_exceeded).
// Before this change, only transport errors were retried — an HTTP 429 surfaced
// as an ordinary error, aborted the whole BatchEmbedWithPool call, and the
// asynq task retry restarted the entire document pipeline from scratch, so
// large documents could never finish under a low quota. See the shared
// policy knobs below.

// RetryableEmbedError marks an embedding API failure that may succeed on retry
// (HTTP 408/429/5xx, or a provider-specific rate-limit response). Embedders
// should wrap these failures so the batch layer can retry them with backoff.
type RetryableEmbedError struct {
	StatusCode int
	Message    string
}

func (e *RetryableEmbedError) Error() string { return e.Message }

// AsRetryableEmbedError unwraps err if it is a retryable embedding error.
func AsRetryableEmbedError(err error) *RetryableEmbedError {
	var target *RetryableEmbedError
	if errors.As(err, &target) {
		return target
	}
	return nil
}

// RetryableHTTPStatus reports whether an HTTP status code is worth retrying.
func RetryableHTTPStatus(code int) bool {
	switch code {
	case http.StatusRequestTimeout, // 408
		http.StatusTooManyRequests,     // 429
		http.StatusInternalServerError, // 500
		http.StatusBadGateway,          // 502
		http.StatusServiceUnavailable,  // 503
		http.StatusGatewayTimeout:      // 504
		return true
	default:
		return false
	}
}

// IsRetryableEmbedError classifies an error as worth retrying. It recognizes
// the typed RetryableEmbedError as well as plain-text errors returned by
// providers that do not wrap their failures (e.g. "Http Status 429 ...",
// "rate limit", "tpm").
func IsRetryableEmbedError(err error) bool {
	if err == nil {
		return false
	}
	if AsRetryableEmbedError(err) != nil {
		return true
	}
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "status 408"),
		strings.Contains(msg, "status 429"),
		strings.Contains(msg, "status 500"),
		strings.Contains(msg, "status 502"),
		strings.Contains(msg, "status 503"),
		strings.Contains(msg, "status 504"),
		strings.Contains(msg, "too many requests"),
		strings.Contains(msg, "rate limit"),
		strings.Contains(msg, "rate_limit"),
		strings.Contains(msg, "quota"),
		strings.Contains(msg, "throttl"):
		return true
	default:
		return false
	}
}

// embedRetryMax is the maximum number of retry attempts after the first
// failure (EMBED_RETRY_MAX, default 3). Read at call time so it can be
// adjusted via environment without a code change.
func embedRetryMax() int {
	if v := strings.TrimSpace(os.Getenv("EMBED_RETRY_MAX")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 && n <= 20 {
			return n
		}
	}
	return 3
}

// embedRetryBackoffBase is the initial backoff delay (EMBED_RETRY_BACKOFF_BASE,
// default 1s). The delay doubles on each attempt up to embedRetryBackoffMax.
func embedRetryBackoffBase() time.Duration {
	if v := strings.TrimSpace(os.Getenv("EMBED_RETRY_BACKOFF_BASE")); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 && d <= time.Minute {
			return d
		}
	}
	return time.Second
}

// embedRetryBackoffMax caps the exponential backoff delay
// (EMBED_RETRY_BACKOFF_MAX, default 60s).
func embedRetryBackoffMax() time.Duration {
	if v := strings.TrimSpace(os.Getenv("EMBED_RETRY_BACKOFF_MAX")); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 && d <= 5*time.Minute {
			return d
		}
	}
	return 60 * time.Second
}

// embedRetryBackoff computes the delay for the given retry attempt
// (attempt is 1-based: 1 → base, 2 → 2×base, ... capped at max).
func embedRetryBackoff(attempt int) time.Duration {
	base := embedRetryBackoffBase()
	max := embedRetryBackoffMax()
	delay := base
	for i := 1; i < attempt && delay < max; i++ {
		delay *= 2
	}
	if delay > max {
		return max
	}
	return delay
}

// parseRetryAfter parses a Retry-After header (seconds or HTTP-date) into a
// duration, clamped to [0, embedRetryBackoffMax]. Returns ok=false when the
// header is absent or unparsable so callers can fall back to exponential
// backoff. Returns ok=true even when the result is 0 so Retry-After: 0
// triggers an immediate retry (the caller must not fall back to backoff).
func parseRetryAfter(header string) (d time.Duration, ok bool) {
	header = strings.TrimSpace(header)
	if header == "" {
		return 0, false
	}
	if secs, err := strconv.Atoi(header); err == nil && secs >= 0 {
		d = time.Duration(secs) * time.Second
		if d > embedRetryBackoffMax() {
			return embedRetryBackoffMax(), true
		}
		return d, true
	}
	if t, err := http.ParseTime(header); err == nil {
		d = time.Until(t)
		if d < 0 {
			return 0, true
		}
		if d > embedRetryBackoffMax() {
			return embedRetryBackoffMax(), true
		}
		return d, true
	}
	return 0, false
}

// retryAfterSeconds is kept for backward compatibility with existing callers.
// Prefer parseRetryAfter for new code.
func retryAfterSeconds(header string) time.Duration {
	d, _ := parseRetryAfter(header)
	return d
}

// sleepWithContext sleeps for d, returning early when ctx is done.
func sleepWithContext(ctx context.Context, d time.Duration) {
	if d <= 0 {
		return
	}
	select {
	case <-ctx.Done():
	case <-time.After(d):
	}
}
