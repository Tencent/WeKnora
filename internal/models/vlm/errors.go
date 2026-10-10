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
// only to drive its own adaptive controller and its in-place retry policy.
// The classes are organised by WHERE in the exchange the failure happened —
// before the connection, at the reply, before the first token, mid-stream —
// which is the axis that predicts both the retry decision and the adaptive
// reaction. Sentinels wrap the raw error on the way out (wrapVerdict), so
// callers can errors.Is() a typed verdict while the original error chain
// stays inspectable.
type ErrorKind int

const (
	// KindRateLimited is a provider throttling signal: an HTTP 429, or ANY
	// reply that carries a Retry-After header (e.g. 503 + Retry-After) — the
	// server explicitly asked us to come back later, and that instruction is
	// honoured verbatim.
	KindRateLimited ErrorKind = iota
	// KindServerError is an explicit error reply from a live server: a 5xx,
	// or a 408 (the provider timing out on our request). The endpoint answered,
	// so it is reachable; the failure is transient and worth retrying.
	KindServerError
	// KindFirstTokenTimeout means the endpoint accepted the connection (and a
	// healthy server returns its 200 headers immediately) but never produced a
	// first token within the request deadline. The server is up yet stuck in
	// prefill: shed load, do NOT trip the breaker.
	KindFirstTokenTimeout
	// KindStreamInterrupted covers everything that goes wrong AFTER the first
	// token was seen, or while the body was being read: a mid-stream transport
	// reset, an EOF cut without a proper finish, a channel closed without a
	// terminal event, or a generation that stalled past the deadline. The
	// answer is partial; retry in place like a 5xx.
	KindStreamInterrupted
	// KindTruncated means the model exhausted its completion budget
	// (finish_reason=length). The endpoint is healthy — the output budget is
	// simply too small for the reasoning + answer on this input — so this is
	// recorded but never sheds load; one in-place retry is still worth it.
	KindTruncated
	// KindClientError is a client-side fault: a 4xx that is neither 429 (rate
	// limit) nor 408 (request timeout) — a bad key, an oversized image, an
	// unsupported request. The endpoint is healthy; retrying cannot help, and
	// the failure says nothing about provider capacity.
	KindClientError
	// KindHardDown is a connection-level failure: the endpoint is not reachable
	// at all (connection refused, DNS failure, TLS handshake failure) or it
	// accepted the connection but never sent a response header within
	// ResponseHeaderTimeout. Unlike KindServerError (a 5xx the server DID
	// answer with), a hard-down endpoint cannot serve ANY request, so it is
	// permanent for the retry window and trips the per-model circuit breaker.
	KindHardDown
	// KindCancelled is the caller walking away (context.Canceled). The endpoint
	// said nothing, so it carries no signal about provider health at all: no
	// retry, no load shedding, no circuit-breaker accounting.
	KindCancelled
)

// ErrServerDown is returned by the per-model circuit breaker when the endpoint
// is tripped: a dead or unreachable upstream was detected across recent requests
// and is now in its cooldown window, so the caller should fail fast instead of
// burning a full retry budget on an endpoint that cannot answer. It is a
// "permanent for now" verdict: callers (e.g. the KB sync task, fixed on the
// #3746 branch) may treat it as non-retryable for the duration of the cooldown.
var ErrServerDown = errors.New("vlm endpoint is currently down (circuit breaker open)")

// ErrClientError marks a client-side fault — a 4xx that is neither 429 (rate
// limit) nor 408 (request timeout): a bad key, an oversized image, an
// unsupported request. The server is healthy; retrying cannot help. Callers
// may treat it as non-retryable.
var ErrClientError = errors.New("vlm request failed with a client error")

func (k ErrorKind) String() string {
	switch k {
	case KindRateLimited:
		return "rate_limited"
	case KindServerError:
		return "server_error"
	case KindFirstTokenTimeout:
		return "first_token_timeout"
	case KindStreamInterrupted:
		return "stream_interrupted"
	case KindTruncated:
		return "truncated"
	case KindClientError:
		return "client_error"
	case KindHardDown:
		return "hard_down"
	case KindCancelled:
		return "cancelled"
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
// the circuit breaker can react. firstTokenSeen is the caller's witness that
// at least one token (answer OR reasoning) had already arrived when the error
// happened — it is what separates a first-token timeout from a mid-stream
// interruption, which share the same raw context-deadline error. It does NOT
// change what the caller receives: the original error is passed through
// unchanged (see wrapVerdict, which only annotates it with a typed sentinel).
func classifyError(err error, firstTokenSeen bool) (ErrorKind, time.Duration, bool) {
	// Caller-side lifecycle errors must be classified BEFORE the transport
	// branch below: the HTTP client wraps a cancelled or timed-out request's
	// context error in api.TransportError{Op: "send request"}, so a purely
	// phase-based lookup would count every user cancellation — and every
	// vlmHTTPTimeout deadline — as endpoint downtime, letting repeated
	// caller-side aborts trip the shared breaker against a healthy endpoint.
	// A dial timeout (net's own "i/o timeout", os.ErrDeadlineExceeded) is NOT
	// context.DeadlineExceeded and still falls through to the hard-down
	// classification, so black-holed endpoints keep tripping the breaker.
	if errors.Is(err, context.Canceled) {
		return KindCancelled, 0, false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		if firstTokenSeen {
			// Tokens were flowing and then the deadline hit: a generation
			// that stalled out mid-stream, not a stuck prefill.
			return KindStreamInterrupted, 0, false
		}
		return KindFirstTokenTimeout, 0, false
	}

	// Budget exhaustion (finish_reason=length) is its own class: the endpoint
	// is healthy, the output budget is simply exhausted on this input. Both
	// the buffered and the streaming path surface it via this sentinel.
	if errors.Is(err, ErrTruncatedCompletion) {
		return KindTruncated, 0, false
	}

	var httpErr *api.HTTPError
	if errors.As(err, &httpErr) {
		retryAfter := httpErr.RetryAfter()
		switch {
		case httpErr.StatusCode == http.StatusTooManyRequests:
			return KindRateLimited, retryAfter, false
		case httpErr.StatusCode >= 500:
			// A 5xx that carries a Retry-After is a throttle, not a plain
			// failure: the server explicitly asked us to come back later,
			// so honour that instruction (and its pacing) verbatim.
			if retryAfter > 0 {
				return KindRateLimited, retryAfter, true
			}
			return KindServerError, 0, true
		case httpErr.StatusCode == http.StatusRequestTimeout:
			// 408 is the provider timing out on our request: transient.
			if retryAfter > 0 {
				return KindRateLimited, retryAfter, false
			}
			return KindServerError, 0, false
		case httpErr.StatusCode >= 400:
			// Any other 4xx is a client-side fault (bad key, oversized
			// image, unsupported request): not retryable, and it must not
			// be mistaken for a provider-capacity signal.
			return KindClientError, 0, false
		}
	}

	// A transport error during the connect / response-header phase ("send
	// request") is a hard-down condition: the endpoint is unreachable, or it
	// accepted the connection but never answered with a response header. A
	// break while reading the body ("read response") is a mid-stream reset.
	// This is the exact signal the circuit breaker needs: a healthy server
	// returns its 200 OK headers immediately even while prefilling, so a
	// missing header is the true proof the endpoint is dead — and it needs no
	// special client config in chat.go.
	var transportErr *api.TransportError
	if errors.As(err, &transportErr) {
		if transportErr.Op == "send request" {
			return KindHardDown, 0, false
		}
		return KindStreamInterrupted, 0, false
	}

	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "429") || strings.Contains(msg, "rate limit") ||
		strings.Contains(msg, "too many requests") {
		return KindRateLimited, 0, false
	}
	if isHardDownMessage(err) {
		return KindHardDown, 0, false
	}
	// An error event whose text the provider cut off mid-answer ("ended before
	// it finished") or any other unrecognised server-side failure: transient
	// by default, worth one retry.
	return KindStreamInterrupted, 0, false
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
	case KindTruncated:
		return fmt.Errorf("%w: %w", ErrTruncatedCompletion, err)
	case KindClientError:
		return fmt.Errorf("%w: %w", ErrClientError, err)
	default:
		return err
	}
}
