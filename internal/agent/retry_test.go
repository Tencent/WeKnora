package agent

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestLLMRetryDelayUsesLongerBackoffForRateLimits(t *testing.T) {
	err := errors.New("API request failed with status 429: rate limit reached")

	if got, want := llmRetryDelay(err, 1), 5*time.Second; got != want {
		t.Fatalf("first retry delay = %v, want %v", got, want)
	}
	if got, want := llmRetryDelay(err, 2), 10*time.Second; got != want {
		t.Fatalf("second retry delay = %v, want %v", got, want)
	}
	if got, want := llmRetryDelay(err, 3), 20*time.Second; got != want {
		t.Fatalf("third retry delay = %v, want %v", got, want)
	}
	if got, want := llmRetryLimit(err), 3; got != want {
		t.Fatalf("retry limit = %d, want %d", got, want)
	}
}

func TestLLMRetryDelayKeepsShortBackoffForOtherTransientErrors(t *testing.T) {
	err := errors.New("upstream returned 503 temporarily unavailable")

	if got, want := llmRetryDelay(err, 1), time.Second; got != want {
		t.Fatalf("first retry delay = %v, want %v", got, want)
	}
	if got, want := llmRetryDelay(err, 2), 2*time.Second; got != want {
		t.Fatalf("second retry delay = %v, want %v", got, want)
	}
	if got, want := llmRetryLimit(err), maxLLMRetries; got != want {
		t.Fatalf("retry limit = %d, want %d", got, want)
	}
}

func TestWaitForLLMRetryStopsWhenContextIsCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	started := time.Now()
	err := waitForLLMRetry(ctx, time.Minute)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("waitForLLMRetry() error = %v, want context.Canceled", err)
	}
	if elapsed := time.Since(started); elapsed > 100*time.Millisecond {
		t.Fatalf("canceled retry wait took %v, want less than 100ms", elapsed)
	}
}

func TestIsTransientErrorTreatsEOFAsRetryable(t *testing.T) {
	tests := []error{
		errors.New("send request: Post https://example.com/chat/completions: EOF"),
		errors.New("send request: unexpected EOF"),
	}

	for _, err := range tests {
		if !isTransientError(err) {
			t.Fatalf("isTransientError(%q) = false, want true", err)
		}
	}
}
