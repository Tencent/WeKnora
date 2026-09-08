package embedding

import (
	"errors"
	"fmt"
	"testing"
	"time"
)

func TestParseRetryAfter(t *testing.T) {
	cases := []struct {
		header   string
		fallback time.Duration
		want     time.Duration
	}{
		{"", time.Second, time.Second},
		{"2", time.Second, 2 * time.Second},
		{"0", time.Second, 100 * time.Millisecond},
		{"-1", time.Second, 100 * time.Millisecond},
		{"1.5s", time.Second, 1500 * time.Millisecond},
		{"nope", 3 * time.Second, 3 * time.Second},
	}
	for _, c := range cases {
		if got := parseRetryAfter(c.header, c.fallback); got != c.want {
			t.Fatalf("parseRetryAfter(%q) = %v, want %v", c.header, got, c.want)
		}
	}
}

func TestIsRateLimitError(t *testing.T) {
	if !IsRateLimitError(&RateLimitError{StatusCode: 429}) {
		t.Fatal("typed RateLimitError should match")
	}
	if !IsRateLimitError(fmt.Errorf("wrap: %w", &RateLimitError{StatusCode: 429})) {
		t.Fatal("wrapped RateLimitError should match")
	}
	if !IsRateLimitError(errors.New("EmbedBatch API error: Http Status 429 Too Many Requests")) {
		t.Fatal("plain 429 string should match")
	}
	if !IsRateLimitError(errors.New("AccountRateLimitExceeded")) {
		t.Fatal("AccountRateLimitExceeded should match")
	}
	if IsRateLimitError(errors.New("invalid api key")) {
		t.Fatal("unrelated error must not match")
	}
	if IsRateLimitError(nil) {
		t.Fatal("nil must not match")
	}
}

func TestShouldRetryHTTPStatus(t *testing.T) {
	if !shouldRetryHTTPStatus(429) || !shouldRetryHTTPStatus(503) {
		t.Fatal("429/5xx should be retriable")
	}
	if shouldRetryHTTPStatus(400) || shouldRetryHTTPStatus(401) || shouldRetryHTTPStatus(200) {
		t.Fatal("4xx (non-429) and 200 must not be retriable")
	}
}
