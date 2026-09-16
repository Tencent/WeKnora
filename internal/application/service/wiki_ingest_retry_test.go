package service

import (
	"context"
	"errors"
	"testing"
)

// TestIsTransientLLMError_HTTPStatuses covers the status codes we know
// the generic REST chat provider bubbles up as "API request failed with
// status NNN: ...". The status-code arm of the classifier is the one
// that actually fires for the 504 gateway timeouts the upstream LLM
// vendors throw most often.
func TestIsTransientLLMError_HTTPStatuses(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		msg  string
		want bool
	}{
		{"API request failed with status 504: Remote error, timeout with 60", true},
		{"API request failed with status 503: service unavailable", true},
		{"API request failed with status 502: bad gateway", true},
		{"API request failed with status 500: internal server error", true},
		{"API request failed with status 429: rate limited", true},
		{"API request failed with status 408: request timeout", true},
		{"API request failed with status 520: Web server returned unknown", true},
		{"API request failed with status 524: timeout (Cloudflare)", true},
		// 4xx permanent: a retry will just fail the same way.
		{"API request failed with status 400: bad request", false},
		{"API request failed with status 401: unauthorized", false},
		{"API request failed with status 403: forbidden (quota exhausted)", false},
		{"API request failed with status 404: model not found", false},
	}
	for _, tc := range cases {
		got := isTransientLLMError(ctx, errors.New(tc.msg))
		if got != tc.want {
			t.Errorf("isTransientLLMError(%q) = %v, want %v", tc.msg, got, tc.want)
		}
	}
}

// TestIsTransientLLMError_TransportSubstrings covers providers (and Go
// stdlib errors) that surface transient conditions as free-form text
// without a numeric status code — DNS blips, TLS handshake failures,
// broken pipes during an in-flight body read, etc.
func TestIsTransientLLMError_TransportSubstrings(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		msg  string
		want bool
	}{
		{"send request: dial tcp: lookup api.example.com: no such host", true},
		{"send request: read tcp 10.0.0.1: i/o timeout", true},
		{"send request: Post \"...\": context deadline exceeded", true},
		{"send request: connection reset by peer", true},
		{"send request: connection refused", true},
		{"send request: broken pipe", true},
		{"send request: tls handshake timeout", true},
		{"send request: unexpected EOF", true},
		// Not transient: the error is actionable (missing model id,
		// malformed tool call, etc.) — retry is just wasted budget.
		{"model not configured for tool use", false},
		{"invalid JSON in tool arguments", false},
		{"parse template: syntax error", false},
	}
	for _, tc := range cases {
		got := isTransientLLMError(ctx, errors.New(tc.msg))
		if got != tc.want {
			t.Errorf("isTransientLLMError(%q) = %v, want %v", tc.msg, got, tc.want)
		}
	}
}

// TestIsTransientLLMError_AbortsWhenParentCtxDone ensures a cancelled
// outer context short-circuits the retry decision. Without this guard
// an ingest task that was interrupted (asynq shutdown, deletion race,
// parent timeout) would keep retrying the LLM during its teardown
// window and waste the remainder of the backoff budget.
func TestIsTransientLLMError_AbortsWhenParentCtxDone(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	// This exact error would normally be classified transient (see
	// TestIsTransientLLMError_HTTPStatuses), but a cancelled ctx must
	// override that so the task can unwind promptly.
	err := errors.New("API request failed with status 504: Remote error")
	if isTransientLLMError(ctx, err) {
		t.Fatal("cancelled ctx should short-circuit to non-transient")
	}
}

// TestIsTransientLLMError_NilError is a sanity check — nil should never
// be treated as retryable, otherwise callers that forget to handle the
// happy path would spin forever.
func TestIsTransientLLMError_NilError(t *testing.T) {
	if isTransientLLMError(context.Background(), nil) {
		t.Fatal("nil error should not be transient")
	}
}

// TestIsTransientLLMError_RateLimit403 covers gateways that report QPM/QPS
// throttling as HTTP 403 with a rate-limit body instead of 429 (e.g. a MaaS
// gateway returning code 0x04030020, "调用频率（qpm）超限"). These carry the
// provider-embedded response body "API request failed with status 403: {...}",
// and only body-rated limit wording classifies them as transient — a plain
// authorization 403 must stay permanent.
func TestIsTransientLLMError_RateLimit403(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name string
		msg  string
		want bool
	}{
		{"qpm gateway", `API request failed with status 403: {"code":"0x04030020","message":"调用频率（qpm）超限"}`, true},
		{"qps gateway", `API request failed with status 403: {"message":"qps exceeded"}`, true},
		{"rate limit", "API request failed with status 403: rate limit reached", true},
		{"too many requests", "API request failed with status 403: too many requests", true},
		{"throttled", "API request failed with status 403: request throttled", true},
		{"chinese frequency", "API request failed with status 403: 请求过于频繁，请稍后重试", true},
		{"busy", "API request failed with status 403: 服务繁忙", true},
		{"try again later", "API request failed with status 403: please try again later", true},
		{"slow down", "API request failed with status 403: slow down your requests", true},
		// Authorization-shaped 403s stay permanent.
		{"unauthorized", "API request failed with status 403: invalid api key", false},
		{"forbidden", "API request failed with status 403: forbidden", false},
		{"permission denied", "API request failed with status 403: permission denied", false},
		{"quota exhausted", "API request failed with status 403: forbidden (quota exhausted)", false},
		{"unknown body", `API request failed with status 403: {"error":"something else"}`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isTransientLLMError(ctx, errors.New(tc.msg)); got != tc.want {
				t.Errorf("isTransientLLMError(%q) = %v, want %v", tc.msg, got, tc.want)
			}
		})
	}
}
