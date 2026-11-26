package client

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestParseRetryAfterSeconds(t *testing.T) {
	now := time.Date(2026, time.November, 26, 9, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name  string
		value string
		want  int
	}{
		{"delta seconds", "42", 42},
		{"delta seconds padded", "  7 ", 7},
		{"zero delta", "0", 0},
		{"negative delta", "-5", 0},
		{"absent", "", 0},
		{"unparsable", "soon", 0},
		{"http date in the future", now.Add(30 * time.Second).Format(http.TimeFormat), 30},
		{"http date already passed", now.Add(-time.Minute).Format(http.TimeFormat), 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := parseRetryAfterSeconds(tc.value, now); got != tc.want {
				t.Errorf("parseRetryAfterSeconds(%q) = %d, want %d", tc.value, got, tc.want)
			}
		})
	}
}

// A partially elapsed second still means "not before that instant": the
// HTTP-date form has one-second granularity, so a deadline 30.4s away must
// round up to 31 rather than tell the caller to retry early.
func TestParseRetryAfterSecondsRoundsPartialSecondUp(t *testing.T) {
	now := time.Date(2026, time.November, 26, 9, 0, 0, 600*int(time.Millisecond), time.UTC)
	value := time.Date(2026, time.November, 26, 9, 0, 31, 0, time.UTC).Format(http.TimeFormat)
	if got := parseRetryAfterSeconds(value, now); got != 31 {
		t.Errorf("parseRetryAfterSeconds(%q) = %d, want 31", value, got)
	}
}

// A 429 that carries Retry-After must surface the hint on *APIError without
// changing the legacy "HTTP error <status>: <body>" message that string-matching
// consumers already depend on.
func TestParseResponseRecordsRetryAfter(t *testing.T) {
	resp := &http.Response{
		StatusCode: http.StatusTooManyRequests,
		Header:     http.Header{"Retry-After": []string{"42"}},
		Body:       io.NopCloser(strings.NewReader(`{"code":1006}`)),
	}

	var apiErr *APIError
	if err := parseResponse(resp, nil); !errors.As(err, &apiErr) {
		t.Fatalf("parseResponse() = %v, want *APIError", err)
	}
	if apiErr.RetryAfterSeconds != 42 {
		t.Errorf("RetryAfterSeconds = %d, want 42", apiErr.RetryAfterSeconds)
	}
	if got, want := apiErr.Error(), `HTTP error 429: {"code":1006}`; got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
}

// A response without the header keeps the hint at zero, so callers can leave
// retry_after_seconds out of the wire envelope instead of printing a bogus 0.
func TestParseResponseWithoutRetryAfter(t *testing.T) {
	resp := &http.Response{
		StatusCode: http.StatusInternalServerError,
		Header:     http.Header{},
		Body:       io.NopCloser(strings.NewReader("boom")),
	}

	var apiErr *APIError
	if err := parseResponse(resp, nil); !errors.As(err, &apiErr) {
		t.Fatalf("parseResponse() = %v, want *APIError", err)
	}
	if apiErr.RetryAfterSeconds != 0 {
		t.Errorf("RetryAfterSeconds = %d, want 0 (omitted)", apiErr.RetryAfterSeconds)
	}
}
