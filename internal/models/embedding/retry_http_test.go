package embedding

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// testRetryClient is a plain client for httptest servers: the production
// client (newEmbeddingHTTPClient) carries SSRF protection that blocks
// loopback addresses.
func testRetryClient() *http.Client { return &http.Client{Timeout: 10 * time.Second} }

func TestRetryEmbeddingRequestHonorsRetryAfterSeconds(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	// Backoff would be 10ms; the 1s Retry-After must win.
	t.Setenv("EMBED_RETRY_BACKOFF_BASE", "10ms")
	start := time.Now()
	resp, err := retryEmbeddingRequest(context.Background(), testRetryClient(),
		http.MethodPost, srv.URL, []byte(`{}`), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer resp.Body.Close()
	if calls.Load() != 2 {
		t.Fatalf("expected 2 calls, got %d", calls.Load())
	}
	if elapsed := time.Since(start); elapsed < 900*time.Millisecond {
		t.Fatalf("Retry-After not honored: elapsed %v < 1s", elapsed)
	}
}

func TestRetryEmbeddingRequestRetryAfterHTTPDate(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			w.Header().Set("Retry-After", time.Now().Add(500*time.Millisecond).UTC().Format(http.TimeFormat))
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	t.Setenv("EMBED_RETRY_BACKOFF_BASE", "1s")
	_, err := retryEmbeddingRequest(context.Background(), testRetryClient(),
		http.MethodPost, srv.URL, []byte(`{}`), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if calls.Load() != 2 {
		t.Fatalf("expected 2 calls, got %d", calls.Load())
	}
}

func TestRetryEmbeddingRequestRetryAfterZeroRetriesImmediately(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	t.Setenv("EMBED_RETRY_BACKOFF_BASE", "10s")
	start := time.Now()
	_, err := retryEmbeddingRequest(context.Background(), testRetryClient(),
		http.MethodPost, srv.URL, []byte(`{}`), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("Retry-After: 0 should not back off, took %v", elapsed)
	}
}

func TestRetryEmbeddingRequestUsesExponentialBackoff(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	t.Setenv("EMBED_RETRY_BACKOFF_BASE", "100ms")
	start := time.Now()
	_, err := retryEmbeddingRequest(context.Background(), testRetryClient(),
		http.MethodPost, srv.URL, []byte(`{}`), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if elapsed := time.Since(start); elapsed < 100*time.Millisecond {
		t.Fatalf("expected backoff wait, took %v", elapsed)
	}
}

func TestRetryEmbeddingRequestExhaustsOnPersistentRetryableStatus(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Retry-After", "0")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()

	t.Setenv("EMBED_RETRY_MAX", "2")
	resp, err := retryEmbeddingRequest(context.Background(), testRetryClient(),
		http.MethodPost, srv.URL, []byte(`{}`), nil)
	if err != nil {
		t.Fatalf("expected the final response, got error: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("expected final 429 response, got %d", resp.StatusCode)
	}
	if calls.Load() != 3 {
		t.Fatalf("expected 1+2=3 calls, got %d", calls.Load())
	}
}

func TestRetryEmbeddingRequestTransportErrorRetried(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	url := srv.URL
	srv.Close() // connection refused from now on

	t.Setenv("EMBED_RETRY_MAX", "2")
	t.Setenv("EMBED_RETRY_BACKOFF_BASE", "10ms")
	_, err := retryEmbeddingRequest(context.Background(), testRetryClient(),
		http.MethodPost, url, []byte(`{}`), nil)
	if err == nil {
		t.Fatal("expected transport error after retries exhausted")
	}
}

func TestRetryEmbeddingRequestContextCancelledDuringWait(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Retry-After", "30")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, err := retryEmbeddingRequest(ctx, testRetryClient(),
		http.MethodPost, srv.URL, []byte(`{}`), nil)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected context deadline exceeded, got %v", err)
	}
	if calls.Load() != 1 {
		t.Fatalf("expected only the first call, got %d", calls.Load())
	}
}

func TestRetryEmbeddingRequestRebuildsBodyAndHeaders(t *testing.T) {
	const wantBody = `{"input":["hello"]}`
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if string(body) != wantBody {
			t.Errorf("attempt %d: body mismatch: %q", calls.Load()+1, body)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer secret" {
			t.Errorf("attempt %d: missing Authorization header", calls.Load()+1)
		}
		if calls.Add(1) < 3 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	t.Setenv("EMBED_RETRY_MAX", "3")
	_, err := retryEmbeddingRequest(context.Background(), testRetryClient(),
		http.MethodPost, srv.URL, []byte(wantBody), func(r *http.Request) {
			r.Header.Set("Authorization", "Bearer secret")
		})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if calls.Load() != 3 {
		t.Fatalf("expected 3 calls, got %d", calls.Load())
	}
}

func TestEmbedHTTPError(t *testing.T) {
	msg := "EmbedBatch API error: Http Status 429 Too Many Requests"
	err := embedHTTPError(http.StatusTooManyRequests, msg)
	var retryable *RetryableEmbedError
	if !errors.As(err, &retryable) {
		t.Fatalf("expected RetryableEmbedError for 429, got %T", err)
	}
	if retryable.StatusCode != http.StatusTooManyRequests || retryable.Message != msg {
		t.Fatalf("typed error fields wrong: %+v", retryable)
	}

	plain := embedHTTPError(http.StatusBadRequest, "bad request")
	if errors.As(plain, &retryable) {
		t.Fatalf("400 must not be retryable, got %+v", plain)
	}
	if !strings.Contains(plain.Error(), "bad request") {
		t.Fatalf("plain error should carry the message: %v", plain)
	}
}
