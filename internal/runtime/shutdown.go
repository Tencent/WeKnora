package runtime

import "time"

// DefaultShutdownTimeout is used when server.shutdown_timeout is unset.
const DefaultShutdownTimeout = 30 * time.Second

// CleanupBudget is reserved from the configured shutdown timeout so connection
// drain cannot consume the whole grace period before process cleanup runs.
// Supervisors often use the same number for termination grace (Kubernetes
// defaults to 30s), so drain and cleanup share one budget instead of stacking.
const CleanupBudget = 5 * time.Second

// ShutdownBudgets splits total into an HTTP drain budget and a cleanup budget.
// Their sum equals total. total <= 0 uses DefaultShutdownTimeout. When total
// is too small to reserve CleanupBudget, the two halves are equal.
func ShutdownBudgets(total time.Duration) (drain, cleanup time.Duration) {
	if total <= 0 {
		total = DefaultShutdownTimeout
	}
	cleanup = CleanupBudget
	if cleanup >= total {
		cleanup = total / 2
		if cleanup <= 0 {
			cleanup = total
		}
	}
	drain = total - cleanup
	if drain <= 0 {
		drain = total
	}
	return drain, cleanup
}

// WaitFor reports whether ch was received before timeout. timeout <= 0 waits
// until ch is received. The boolean is false only when the timeout wins.
func WaitFor(ch <-chan struct{}, timeout time.Duration) bool {
	if timeout <= 0 {
		<-ch
		return true
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-ch:
		return true
	case <-timer.C:
		return false
	}
}
