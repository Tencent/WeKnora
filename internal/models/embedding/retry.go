package embedding

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// RateLimitError is returned when an embedding provider rejects a request
// with HTTP 429 (or an equivalent rate-limit signal). Callers can inspect
// RetryAfter to back off without replaying already-successful work.
type RateLimitError struct {
	StatusCode int
	RetryAfter time.Duration
	Body       string
}

func (e *RateLimitError) Error() string {
	if e == nil {
		return "embedding rate limited"
	}
	if e.RetryAfter > 0 {
		return fmt.Sprintf("embedding rate limited: status=%d retry_after=%s body=%s",
			e.StatusCode, e.RetryAfter, e.Body)
	}
	return fmt.Sprintf("embedding rate limited: status=%d body=%s", e.StatusCode, e.Body)
}

// IsRateLimitError reports whether err (or any wrapped cause) is a rate-limit
// failure that should be paced rather than treated as a permanent error.
func IsRateLimitError(err error) bool {
	if err == nil {
		return false
	}
	var rl *RateLimitError
	if errors.As(err, &rl) {
		return true
	}
	// Fallback for providers that still surface 429 as a plain fmt.Errorf.
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "429") ||
		strings.Contains(msg, "rate limit") ||
		strings.Contains(msg, "ratelimit") ||
		strings.Contains(msg, "too many requests") ||
		strings.Contains(msg, "accountratelimitexceeded")
}

// shouldRetryHTTPStatus reports whether a provider HTTP status is worth
// retrying inside doRequestWithRetry (429 + 5xx).
func shouldRetryHTTPStatus(code int) bool {
	return code == http.StatusTooManyRequests || (code >= 500 && code < 600)
}

// parseRetryAfter interprets a Retry-After header (integer seconds) into a
// wait duration. Empty / unparseable values fall back to fallback. Zero or
// negative values are coerced to 100ms so we still yield the scheduler.
func parseRetryAfter(header string, fallback time.Duration) time.Duration {
	header = strings.TrimSpace(header)
	if header == "" {
		return fallback
	}
	if secs, err := strconv.Atoi(header); err == nil {
		if secs <= 0 {
			return 100 * time.Millisecond
		}
		return time.Duration(secs) * time.Second
	}
	// Some providers emit Go-style durations ("1.5s"); accept those too.
	if d, err := time.ParseDuration(header); err == nil {
		if d <= 0 {
			return 100 * time.Millisecond
		}
		return d
	}
	return fallback
}

// jitteredBackoff returns base * 2^attempt capped at max, with ±20% jitter
// so concurrent document workers do not thundering-herd the provider.
func jitteredBackoff(base time.Duration, attempt int, max time.Duration) time.Duration {
	if attempt < 0 {
		attempt = 0
	}
	d := base << uint(attempt)
	if d > max || d <= 0 {
		d = max
	}
	// ±20% jitter
	j := time.Duration(float64(d) * (0.8 + 0.4*rand.Float64()))
	if j < 100*time.Millisecond {
		return 100 * time.Millisecond
	}
	return j
}

// waitCtx pauses for d or until ctx is cancelled.
func waitCtx(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// rateLimitErrorFromResponse builds a RateLimitError from a 429 response
// body + Retry-After header. Body is truncated for log safety.
func rateLimitErrorFromResponse(statusCode int, retryAfterHeader string, body []byte, fallback time.Duration) *RateLimitError {
	bodyStr := string(body)
	if len(bodyStr) > 500 {
		bodyStr = bodyStr[:500] + "... (truncated)"
	}
	return &RateLimitError{
		StatusCode: statusCode,
		RetryAfter: parseRetryAfter(retryAfterHeader, fallback),
		Body:       bodyStr,
	}
}
