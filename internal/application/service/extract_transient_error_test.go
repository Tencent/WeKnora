package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"testing"
)

// The classifier decides retry-vs-skip for a failed graph extraction. Skipping
// a transient failure is what silently produces an incomplete graph, so the
// transient cases are the ones worth pinning down.
func TestIsTransientExtractionErrorRetriesServiceFailures(t *testing.T) {
	cases := []struct {
		name string
		err  error
	}{
		{"cancelled context", context.Canceled},
		{"deadline exceeded", context.DeadlineExceeded},
		{"wrapped cancellation", fmt.Errorf("extract chunk: %w", context.Canceled)},
		{"truncated response", io.ErrUnexpectedEOF},
		{"dial failure", &net.OpError{Op: "dial", Err: errors.New("connection refused")}},
		{"dns failure", &net.DNSError{Err: "no such host", Name: "gateway.invalid"}},
		{"throttled", errors.New("rate limit exceeded, please retry")},
		{"gateway 503", errors.New("request failed with status 503")},
		// Gateways routinely report pool exhaustion as 400 with the reason in
		// the body, so the text has to carry the decision here.
		{"pool exhausted reported as 400", errors.New("status 400: model pool temporarily unavailable")},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if !isTransientExtractionError(tc.err) {
				t.Fatalf("expected %v to be retried, got skipped", tc.err)
			}
		})
	}
}

func TestIsTransientExtractionErrorSkipsInputFailures(t *testing.T) {
	cases := []struct {
		name string
		err  error
	}{
		{"no error", nil},
		// The failure that motivated the split: a model answering with prose
		// instead of JSON fails identically on every retry.
		{"malformed model output", errors.New("invalid character 'a' looking for beginning of value")},
		{"unparseable graph", errors.New("failed to unmarshal graph data")},
		{"empty response", errors.New("model returned no extractable entities")},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if isTransientExtractionError(tc.err) {
				t.Fatalf("expected %v to be skipped, got retried", tc.err)
			}
		})
	}
}
