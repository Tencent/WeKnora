package vlm

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/models/api"
)

func TestClassifyErrorHTTPStatus(t *testing.T) {
	cases := []struct {
		name      string
		err       error
		wantKind  ErrorKind
		want5xx   bool
		wantRetry time.Duration
	}{
		{
			name: "429 rate limited with retry-after",
			err: &api.HTTPError{
				StatusCode: http.StatusTooManyRequests,
				Header:     http.Header{"Retry-After": []string{"5"}},
			},
			wantKind:  KindRateLimited,
			want5xx:   false,
			wantRetry: 5 * time.Second,
		},
		{
			name:     "500 without retry-after is a server error",
			err:      &api.HTTPError{StatusCode: 500},
			wantKind: KindServerError,
			want5xx:  true,
		},
		{
			name:     "503 without retry-after is a server error",
			err:      &api.HTTPError{StatusCode: 503},
			wantKind: KindServerError,
			want5xx:  true,
		},
		{
			name: "503 with retry-after is a throttle, not a plain failure",
			err: &api.HTTPError{
				StatusCode: http.StatusServiceUnavailable,
				Header:     http.Header{"Retry-After": []string{"30"}},
			},
			wantKind:  KindRateLimited,
			want5xx:   true,
			wantRetry: 30 * time.Second,
		},
		{
			name:     "400 client error",
			err:      &api.HTTPError{StatusCode: 400},
			wantKind: KindClientError,
			want5xx:  false,
		},
		{
			name:     "401 client error",
			err:      &api.HTTPError{StatusCode: http.StatusUnauthorized},
			wantKind: KindClientError,
			want5xx:  false,
		},
		{
			name:     "408 request timeout is a server-side error",
			err:      &api.HTTPError{StatusCode: http.StatusRequestTimeout},
			wantKind: KindServerError,
			want5xx:  false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			kind, retry, is5xx := classifyError(tc.err, false)
			if kind != tc.wantKind {
				t.Errorf("kind = %v, want %v", kind, tc.wantKind)
			}
			if is5xx != tc.want5xx {
				t.Errorf("is5xx = %v, want %v", is5xx, tc.want5xx)
			}
			if tc.wantRetry != 0 && retry != tc.wantRetry {
				t.Errorf("retry = %v, want %v", retry, tc.wantRetry)
			}
		})
	}
}

func TestClassifyErrorContextDeadline(t *testing.T) {
	// A deadline with NO token yet is a first-token timeout: the server
	// accepted the connection but never started producing.
	if kind, _, _ := classifyError(context.DeadlineExceeded, false); kind != KindFirstTokenTimeout {
		t.Errorf("deadline without first token -> kind=%v, want KindFirstTokenTimeout", kind)
	}
	wrapped := fmt.Errorf("wrapped: %w", context.DeadlineExceeded)
	if kind, _, _ := classifyError(wrapped, false); kind != KindFirstTokenTimeout {
		t.Errorf("wrapped deadline -> kind=%v, want KindFirstTokenTimeout", kind)
	}
	// The SAME raw deadline with tokens already flowing is a mid-stream
	// interruption: the generation stalled out, not a stuck prefill.
	if kind, _, _ := classifyError(context.DeadlineExceeded, true); kind != KindStreamInterrupted {
		t.Errorf("deadline after first token -> kind=%v, want KindStreamInterrupted", kind)
	}
	// A cancellation is the caller walking away: no endpoint signal at all,
	// regardless of the phase, so it must never be lumped in with timeouts
	// (which shed load) or transport outages (which trip the breaker).
	if kind, _, _ := classifyError(context.Canceled, true); kind != KindCancelled {
		t.Errorf("cancel after first token -> kind=%v, want KindCancelled", kind)
	}
	if kind, _, _ := classifyError(context.Canceled, false); kind != KindCancelled {
		t.Errorf("cancel before first token -> kind=%v, want KindCancelled", kind)
	}
}

func TestClassifyErrorTruncatedSentinel(t *testing.T) {
	// Both the buffered and the streaming path surface budget exhaustion via
	// the ErrTruncatedCompletion sentinel; it must classify as its own kind
	// before any transport/text fallback can misread it.
	if kind, _, _ := classifyError(ErrTruncatedCompletion, false); kind != KindTruncated {
		t.Errorf("sentinel -> kind=%v, want KindTruncated", kind)
	}
	wrapped := fmt.Errorf("%w at %d tokens (finish_reason=length)", ErrTruncatedCompletion, defaultMaxToks)
	if kind, _, _ := classifyError(wrapped, true); kind != KindTruncated {
		t.Errorf("wrapped truncation -> kind=%v, want KindTruncated", kind)
	}
	if !isRetryable(KindTruncated) {
		t.Error("truncation should stay retryable: one retry may fit the budget")
	}
}

func TestClassifyErrorMessageFallback(t *testing.T) {
	cases := []struct {
		msg  string
		kind ErrorKind
	}{
		{"rate limit exceeded", KindRateLimited},
		{"HTTP 429 Too Many Requests", KindRateLimited},
		// Connection-level failures (a refused connection or a dial / first-byte
		// timeout) are hard-down: they trip the per-model circuit breaker, so a
		// dead endpoint is detected once and every other request fails fast.
		{"i/o timeout", KindHardDown},
		{"connection refused", KindHardDown},
		{"no such host", KindHardDown},
		// A bare "dns" mention is deliberately NOT a hard-down signal: a
		// server-side error message that merely mentions dns (e.g. "invalid
		// dns config") is the server talking about ITS upstream, not our
		// connection to it dying. Default to stream-interrupted (transient).
		{"invalid dns config in request", KindStreamInterrupted},
		{"stream ended before it finished", KindStreamInterrupted},
	}
	for _, tc := range cases {
		if kind, _, _ := classifyError(errors.New(tc.msg), false); kind != tc.kind {
			t.Errorf("msg=%q -> kind=%v, want %v", tc.msg, kind, tc.kind)
		}
	}
}

func TestClassifyErrorTransportPhases(t *testing.T) {
	sendErr := fmt.Errorf("Post %q: %w", "http://ep", &api.TransportError{
		Op: "send request", Err: errors.New("connection refused"),
	})
	if kind, _, _ := classifyError(sendErr, false); kind != KindHardDown {
		t.Errorf("send-request transport -> kind=%v, want KindHardDown", kind)
	}
	readErr := fmt.Errorf("Post %q: %w", "http://ep", &api.TransportError{
		Op: "read response", Err: errors.New("connection reset by peer"),
	})
	if kind, _, _ := classifyError(readErr, true); kind != KindStreamInterrupted {
		t.Errorf("read-response transport -> kind=%v, want KindStreamInterrupted", kind)
	}
}
