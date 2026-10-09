package vlm

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/models/api"
)

// Phase marks which stage of the managed request a failure was observed at.
// It is a lifecycle signal the manager emits to an optional StatusSink; it is
// NOT a success/failure verdict for the caller. The caller judges success from
// the (text, error) reply the manager passes through.
type Phase int

const (
	// PhaseQueued means the request was still waiting for an admission slot
	// (concurrency / rate budget) when it ended.
	PhaseQueued Phase = iota
	// PhaseRateWait means it was rejected by the per-model RPM budget.
	PhaseRateWait
	// PhaseInflight means a slot was held and the request had been sent to the
	// server but no token had returned yet (server in prefill).
	PhaseInflight
	// PhaseFirstToken means the first token had returned (server actively
	// generating) when the failure occurred.
	PhaseFirstToken
	// PhaseDone means the request completed normally; only used on success
	// snapshots, never on a typed error.
	PhaseDone
)

func (p Phase) String() string {
	switch p {
	case PhaseQueued:
		return "queued"
	case PhaseRateWait:
		return "rate_wait"
	case PhaseInflight:
		return "inflight"
	case PhaseFirstToken:
		return "first_token"
	case PhaseDone:
		return "done"
	default:
		return "unknown"
	}
}

// ErrorKind is the manager's INTERNAL classification of a raw error, used
// only to drive its own adaptive controller (shed load on 429 / 5xx / timeout)
// and its in-place retry policy. It is deliberately not surfaced to callers as
// a typed error: the manager passes the original error through and lets each
// caller decide success from the reply content. See the vlm-manager decoupling
// note in plan.md and the works/pr-3746 caller-side doc.
type ErrorKind int

const (
	// KindRateLimited is an HTTP 429 / provider rate-limit signal.
	KindRateLimited ErrorKind = iota
	// KindUnavailable is a 5xx / transport error / upstream timeout.
	KindUnavailable
	// KindTimeout is the caller's own context deadline, attributed to the phase
	// it was observed in.
	KindTimeout
	// KindPermanent is a client-side fault: a 4xx that is neither 429 (rate
	// limit) nor 408 (request timeout) — a bad key, an oversized image, an
	// unsupported request. It is NOT retryable and says nothing about provider
	// health, so it must neither be retried nor shed load / start a cooldown.
	KindPermanent
)

func (k ErrorKind) String() string {
	switch k {
	case KindRateLimited:
		return "rate_limited"
	case KindUnavailable:
		return "unavailable"
	case KindTimeout:
		return "timeout"
	case KindPermanent:
		return "permanent"
	default:
		return "unknown"
	}
}

// PhaseInfo carries context about a lifecycle phase transition, delivered to
// a caller's StatusSink. Dwell is the time spent in the previous phase.
type PhaseInfo struct {
	Dwell time.Duration
}

// classifyError maps a raw inner error to the manager's internal ErrorKind
// plus an optional Retry-After and a 5xx flag, so the adaptive controller can
// react. It does NOT change what the caller receives: the original error is
// passed through unchanged.
func classifyError(err error) (ErrorKind, time.Duration, bool) {
	var httpErr *api.HTTPError
	if errors.As(err, &httpErr) {
		switch {
		case httpErr.StatusCode == http.StatusTooManyRequests:
			return KindRateLimited, httpErr.RetryAfter(), false
		case httpErr.StatusCode >= 500:
			return KindUnavailable, 0, true
		case httpErr.StatusCode == http.StatusRequestTimeout:
			// 408 is the provider timing out on our request: transient.
			return KindUnavailable, 0, false
		case httpErr.StatusCode >= 400:
			// Any other 4xx is a client-side, permanent fault (bad key,
			// oversized image, unsupported request): not retryable, and it must
			// not be mistaken for a provider-capacity signal.
			return KindPermanent, 0, false
		}
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return KindTimeout, 0, false
	}
	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "429") || strings.Contains(msg, "rate limit") ||
		strings.Contains(msg, "too many requests") {
		return KindRateLimited, 0, false
	}
	return KindUnavailable, 0, false
}
