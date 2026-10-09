package vlm

import (
	"context"
	"errors"
	"fmt"
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
	// KindHardDown is a connection-level failure: the endpoint is not reachable
	// at all (connection refused, DNS failure, TLS handshake failure) or it
	// accepted the connection but never sent a response header within
	// ResponseHeaderTimeout. Unlike KindUnavailable (a 5xx the server DID
	// answer with), a hard-down endpoint cannot serve ANY request, so it is
	// permanent for the retry window and trips the per-model circuit breaker.
	KindHardDown
)

// ErrServerDown is returned by the per-model circuit breaker when the endpoint
// is tripped: a dead or unreachable upstream was detected across recent requests
// and is now in its cooldown window, so the caller should fail fast instead of
// burning a full retry budget on an endpoint that cannot answer. It is a
// "permanent for now" verdict: callers (e.g. the KB sync task, fixed on the
// #3746 branch) may treat it as non-retryable for the duration of the cooldown.
var ErrServerDown = errors.New("vlm endpoint is currently down (circuit breaker open)")

// ErrPermanent marks a client-side, permanent fault — a 4xx that is neither 429
// (rate limit) nor 408 (request timeout): a bad key, an oversized image, an
// unsupported request. The server is healthy; retrying cannot help. Callers may
// treat it as non-retryable.
var ErrPermanent = errors.New("vlm request failed with a permanent client error")

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
	case KindHardDown:
		return "hard_down"
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
// plus an optional Retry-After and a 5xx flag, so the adaptive controller and
// the circuit breaker can react. It does NOT change what the caller receives:
// the original error is passed through unchanged (see wrapVerdict, which only
// annotates it with a typed sentinel for the caller's retry policy).
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

	// A transport error during the connect / response-header phase ("send
	// request") is a hard-down condition: the endpoint is unreachable, or it
	// accepted the connection but never answered with a response header. A
	// break while reading the body ("read response") is a mid-stream reset and
	// is transient (retry in place, like a 5xx). This is the exact signal the
	// circuit breaker needs: a healthy server returns its 200 OK headers
	// immediately even while prefilling, so a missing header is the true proof
	// the endpoint is dead — and it needs no special client config in chat.go.
	var transportErr *api.TransportError
	if errors.As(err, &transportErr) {
		if transportErr.Op == "send request" {
			return KindHardDown, 0, false
		}
		return KindUnavailable, 0, false
	}

	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return KindTimeout, 0, false
	}
	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "429") || strings.Contains(msg, "rate limit") ||
		strings.Contains(msg, "too many requests") {
		return KindRateLimited, 0, false
	}
	if isHardDownMessage(err) {
		return KindHardDown, 0, false
	}
	return KindUnavailable, 0, false
}

// isHardDownMessage reports whether an error's text identifies a
// connection-level failure that no healthy server could recover from within a
// retry: a refused connection, a DNS failure, a TLS handshake failure, or a
// timeout before any response header arrived (first-byte timeout). Mid-stream
// body stalls are deliberately NOT included — a server that already sent its
// headers is alive and merely slow. It is only consulted for non-TransportError
// errors; api.TransportError carries the phase ("send request" vs "read
// response") explicitly and is the authoritative signal.
func isHardDownMessage(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "connection refused") ||
		strings.Contains(msg, "no such host") ||
		strings.Contains(msg, "dns") ||
		strings.Contains(msg, "tls handshake") ||
		strings.Contains(msg, "tls: ") ||
		strings.Contains(msg, "i/o timeout") ||
		strings.Contains(msg, "awaiting response headers") ||
		strings.Contains(msg, "timeout awaiting response headers")
}

// wrapVerdict annotates a terminal (non-retried) error with a typed sentinel so
// the caller can apply the correct retry policy, without discarding the
// underlying error. It is the single place the manager emits a "permanent for
// now" (dead endpoint) or "permanent" (bad request) verdict. The original error
// is wrapped with %w — not formatted as text — so the decoupling contract holds:
// a caller can still errors.Is() the original api.HTTPError (e.g. to read the
// 401/403/404 status) AND errors.Is() the new sentinel (for the #3746 branch's
// asynq.SkipRetry). Both layers stay inspectable.
func wrapVerdict(err error, kind ErrorKind) error {
	switch kind {
	case KindHardDown:
		return fmt.Errorf("%w: %w", ErrServerDown, err)
	case KindPermanent:
		return fmt.Errorf("%w: %w", ErrPermanent, err)
	default:
		return err
	}
}
