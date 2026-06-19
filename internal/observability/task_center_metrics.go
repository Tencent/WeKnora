package observability

import (
	"context"
	"errors"
	"expvar"
	"net"
	"strings"

	"github.com/Tencent/WeKnora/internal/types"
)

var (
	modelCallsInFlight = expvar.NewMap("weknora_model_calls_inflight")
	modelTimeouts      = expvar.NewMap("weknora_model_call_timeouts_total")
	modelRateLimits    = expvar.NewMap("weknora_model_call_429_total")
	modelAuthFailures  = expvar.NewMap("weknora_model_call_auth_failures_total")
	ledgerWriteFails   = expvar.NewMap("weknora_task_ledger_write_failures_total")
	staleDispatches    = expvar.NewMap("weknora_task_ledger_stale_dispatch_total")
)

func ModelCallStarted(provider, model string) func(error) {
	key := metricKey(provider, model)
	modelCallsInFlight.Add(key, 1)
	return func(err error) {
		modelCallsInFlight.Add(key, -1)
		if err == nil {
			return
		}
		switch types.HTTPStatusFromError(err) {
		case 429:
			modelRateLimits.Add(key, 1)
		case 401, 403:
			modelAuthFailures.Add(key, 1)
		}
		if types.ClassifyTaskError(err) == types.TaskErrorClassRetryable && isTimeoutLike(err) {
			modelTimeouts.Add(key, 1)
		}
	}
}

func RecordTaskLedgerWriteFailure(component, operation string) {
	ledgerWriteFails.Add(metricKey(component, operation), 1)
}

func RecordStaleDispatch(count int64) {
	if count <= 0 {
		return
	}
	staleDispatches.Add("total", count)
}

func metricKey(parts ...string) string {
	clean := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			part = "unknown"
		}
		part = strings.NewReplacer(" ", "_", "/", "_", "\\", "_", ":", "_", ",", "_").Replace(part)
		clean = append(clean, part)
	}
	return strings.Join(clean, ".")
}

func isTimeoutLike(err error) bool {
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var netErr net.Error
	return errors.As(err, &netErr) && netErr.Timeout()
}
