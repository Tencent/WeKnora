package im

import (
	"context"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
)

// defaultRecycleInterval is how often a supervised long connection is proactively
// torn down and rebuilt. Some IM SDKs (e.g. DingTalk, Feishu) delegate reconnection
// to internal logic that can silently enter a "zombie" state on long-running
// connections — the connection object stays alive but no longer receives messages.
// Periodically recreating the connection bounds the worst-case outage to this
// interval, on top of whatever auto-reconnect the SDK already provides.
const defaultRecycleInterval = 6 * time.Hour

// defaultSupervisorRetryDelay is the backoff applied after a failed connect attempt.
const defaultSupervisorRetryDelay = 5 * time.Second

// SupervisorConfig configures RunSupervised.
type SupervisorConfig struct {
	// Name identifies the supervised connection in logs (e.g. "DingTalk channel xxx").
	Name string

	// MaxConnAge is the interval between proactive reconnections.
	// Defaults to defaultRecycleInterval when <= 0.
	MaxConnAge time.Duration

	// RetryDelay is the backoff after a failed connect attempt.
	// Defaults to defaultSupervisorRetryDelay when <= 0.
	RetryDelay time.Duration

	// Connect establishes a fresh connection and returns a stop function that
	// tears it down cleanly (preventing any further internal reconnects).
	// It may wrap either a blocking or a non-blocking SDK Start: the only
	// contract is that it returns once the connection has been established
	// (or the attempt has failed), along with a stop function.
	Connect func(ctx context.Context) (stop func(), err error)
}

// RunSupervised manages the lifecycle of a long-lived IM connection.
//
// It keeps a connection alive by (re)establishing it via cfg.Connect, then
// proactively recycling it every cfg.MaxConnAge. The underlying SDK is still
// expected to handle transient drops via its own auto-reconnect; the periodic
// recycle is a safety net that eliminates stuck/zombie connections that the SDK
// fails to recover on its own.
//
// RunSupervised blocks until ctx is cancelled. On cancellation it tears down the
// active connection via the stop function before returning, so callers can rely
// on cancelling ctx to fully stop the connection.
func RunSupervised(ctx context.Context, cfg SupervisorConfig) {
	maxAge := cfg.MaxConnAge
	if maxAge <= 0 {
		maxAge = defaultRecycleInterval
	}
	retryDelay := cfg.RetryDelay
	if retryDelay <= 0 {
		retryDelay = defaultSupervisorRetryDelay
	}

	for {
		if ctx.Err() != nil {
			return
		}

		stop, err := cfg.Connect(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			logger.Warnf(ctx, "[IM] %s connect failed: %v, retrying in %v", cfg.Name, err, retryDelay)
			select {
			case <-time.After(retryDelay):
				continue
			case <-ctx.Done():
				return
			}
		}

		logger.Infof(ctx, "[IM] %s connection established (recycle in %v)", cfg.Name, maxAge)

		select {
		case <-time.After(maxAge):
			logger.Infof(ctx, "[IM] %s periodic reconnect to refresh connection", cfg.Name)
			if stop != nil {
				stop()
			}
		case <-ctx.Done():
			if stop != nil {
				stop()
			}
			return
		}
	}
}
