package embedding

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/Tencent/WeKnora/internal/logger"
)

// This file is the single shared HTTP retry implementation for every
// embedding provider. Providers only build the URL/headers/body and parse
// successful responses; 429/5xx handling, Retry-After and backoff live here
// so no per-provider retry loop needs to be maintained.

// retryEmbeddingRequest sends one embedding request under the shared retry
// policy. The request is rebuilt from body on every attempt (the body is
// never consumed) and setHeaders is re-applied, so signed/authenticated
// requests stay valid across retries. Retryable responses are drained and
// closed before the next attempt; context cancellation aborts immediately.
//
// It returns the final response as soon as it is non-retryable or the retry
// budget is exhausted — including a still-retryable status on the last
// attempt, so the caller can map it with embedHTTPError and let the batch
// layer apply its own throttle/backoff.
func retryEmbeddingRequest(
	ctx context.Context,
	client *http.Client,
	method, url string,
	body []byte,
	setHeaders func(*http.Request),
) (*http.Response, error) {
	maxRetries := embedRetryMax()
	for attempt := 0; attempt <= maxRetries; attempt++ {
		req, err := http.NewRequestWithContext(ctx, method, url, bytes.NewReader(body))
		if err != nil {
			return nil, fmt.Errorf("create embedding request: %w", err)
		}
		if setHeaders != nil {
			setHeaders(req)
		}

		resp, err := client.Do(req)
		if err == nil {
			if !RetryableHTTPStatus(resp.StatusCode) || attempt == maxRetries {
				return resp, nil
			}
			delay := embedRetryBackoff(attempt + 1)
			if ra, ok := parseRetryAfter(resp.Header.Get("Retry-After")); ok {
				delay = ra
			}
			logger.GetLogger(ctx).Warnf("embedding request got retryable status %d, retrying (%d/%d) after %v",
				resp.StatusCode, attempt+1, maxRetries+1, delay)
			// Drain and close so the connection can be reused.
			io.Copy(io.Discard, io.LimitReader(resp.Body, 64*1024))
			resp.Body.Close()
			sleepWithContext(ctx, delay)
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			continue
		}

		if attempt == maxRetries {
			return nil, err
		}
		logger.GetLogger(ctx).Errorf("embedding request failed (attempt %d/%d): %v", attempt+1, maxRetries+1, err)
		sleepWithContext(ctx, embedRetryBackoff(attempt+1))
		if err := ctx.Err(); err != nil {
			return nil, err
		}
	}
	return nil, errors.New("embedding request retry exhausted")
}

// embedHTTPError maps a final HTTP response status onto the common embedding
// error type: retryable statuses become RetryableEmbedError (so the batch
// layer can retry with backoff/throttle), anything else is a plain error.
func embedHTTPError(status int, message string) error {
	if RetryableHTTPStatus(status) {
		return &RetryableEmbedError{StatusCode: status, Message: message}
	}
	return errors.New(message)
}
