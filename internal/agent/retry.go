package agent

import (
	"context"
	"strings"
	"time"
)

const (
	rateLimitRetryBaseDelay = 5 * time.Second
	rateLimitMaxRetries     = 3
)

func llmRetryLimit(err error) int {
	if isRateLimitError(err) {
		return rateLimitMaxRetries
	}
	return maxLLMRetries
}

func llmRetryDelay(err error, attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	if isRateLimitError(err) {
		return rateLimitRetryBaseDelay * time.Duration(1<<(attempt-1))
	}
	return time.Duration(attempt) * time.Second
}

func isRateLimitError(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "429") ||
		strings.Contains(message, "rate limit") ||
		strings.Contains(message, "limit_burst_rate")
}

func waitForLLMRetry(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()

	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
