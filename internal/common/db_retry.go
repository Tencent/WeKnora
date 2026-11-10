package common

import (
	"context"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
)

const defaultMaxDeadlockRetries = 3

// WithDeadlockRetry retries fn when MySQL returns Error 1213 (deadlock).
// Retries up to 3 times with exponential backoff (50ms, 100ms, 200ms).
// Safe for all DB drivers — non-deadlock errors are returned immediately.
func WithDeadlockRetry(ctx context.Context, fn func() error) error {
	var err error
	for attempt := 0; attempt <= defaultMaxDeadlockRetries; attempt++ {
		err = fn()
		if err == nil || !IsDeadlockError(err) {
			return err
		}
		if attempt < defaultMaxDeadlockRetries {
			wait := time.Duration(50<<attempt) * time.Millisecond
			logger.Warnf(ctx, "[DeadlockRetry] attempt %d/%d failed with deadlock, retrying in %v: %v",
				attempt+1, defaultMaxDeadlockRetries, wait, err)
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(wait):
			}
		}
	}
	return err
}

// IsDeadlockError checks whether err is a MySQL deadlock (Error 1213).
func IsDeadlockError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "Error 1213") ||
		strings.Contains(msg, "Deadlock found when trying to get lock")
}
