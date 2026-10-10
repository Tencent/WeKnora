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
			name:     "500 unavailable is 5xx",
			err:      &api.HTTPError{StatusCode: 500},
			wantKind: KindUnavailable,
			want5xx:  true,
		},
		{
			name:     "503 unavailable is 5xx",
			err:      &api.HTTPError{StatusCode: 503},
			wantKind: KindUnavailable,
			want5xx:  true,
		},
		{
			name:     "400 permanent client error",
			err:      &api.HTTPError{StatusCode: 400},
			wantKind: KindPermanent,
			want5xx:  false,
		},
		{
			name:     "401 permanent client error",
			err:      &api.HTTPError{StatusCode: http.StatusUnauthorized},
			wantKind: KindPermanent,
			want5xx:  false,
		},
		{
			name:     "408 request timeout is transient",
			err:      &api.HTTPError{StatusCode: http.StatusRequestTimeout},
			wantKind: KindUnavailable,
			want5xx:  false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			kind, retry, is5xx := classifyError(tc.err)
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

func TestClassifyErrorContextTimeout(t *testing.T) {
	// A deadline is our own request timeout: slow-endpoint signal, sheds load.
	if kind, _, _ := classifyError(context.DeadlineExceeded); kind != KindTimeout {
		t.Errorf("err=%v -> kind=%v, want KindTimeout", context.DeadlineExceeded, kind)
	}
	wrapped := fmt.Errorf("wrapped: %w", context.DeadlineExceeded)
	if kind, _, _ := classifyError(wrapped); kind != KindTimeout {
		t.Errorf("wrapped deadline -> kind=%v, want KindTimeout", kind)
	}
	// A cancellation is the caller walking away: no endpoint signal at all, so
	// it gets its own kind and must never be lumped in with timeouts (which
	// shed load) or transport outages (which trip the breaker).
	if kind, _, _ := classifyError(context.Canceled); kind != KindCancelled {
		t.Errorf("err=%v -> kind=%v, want KindCancelled", context.Canceled, kind)
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
	}
	for _, tc := range cases {
		if kind, _, _ := classifyError(errors.New(tc.msg)); kind != tc.kind {
			t.Errorf("msg=%q -> kind=%v, want %v", tc.msg, kind, tc.kind)
		}
	}
}
