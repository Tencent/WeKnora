package embedding

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/panjf2000/ants/v2"
)

func TestRetryableHTTPStatus(t *testing.T) {
	for code, want := range map[int]bool{
		http.StatusOK:                  false,
		http.StatusBadRequest:          false,
		http.StatusRequestTimeout:      true,
		http.StatusTooManyRequests:     true,
		http.StatusInternalServerError: true,
		http.StatusBadGateway:          true,
		http.StatusServiceUnavailable:  true,
		http.StatusGatewayTimeout:      true,
	} {
		if got := RetryableHTTPStatus(code); got != want {
			t.Errorf("RetryableHTTPStatus(%d) = %v, want %v", code, got, want)
		}
	}
}

func TestEmbedRetryBackoffCapped(t *testing.T) {
	t.Setenv("EMBED_RETRY_BACKOFF_BASE", "1s")
	t.Setenv("EMBED_RETRY_BACKOFF_MAX", "10s")
	if got := embedRetryBackoff(1); got != time.Second {
		t.Fatalf("attempt 1 = %v, want 1s", got)
	}
	if got := embedRetryBackoff(2); got != 2*time.Second {
		t.Fatalf("attempt 2 = %v, want 2s", got)
	}
	if got := embedRetryBackoff(20); got != 10*time.Second {
		t.Fatalf("attempt 20 = %v, want capped 10s", got)
	}
}

func TestRetryAfterSeconds(t *testing.T) {
	if got := retryAfterSeconds("5"); got != 5*time.Second {
		t.Fatalf("retry-after seconds = %v, want 5s", got)
	}
	if got := retryAfterSeconds(""); got != 0 {
		t.Fatalf("empty retry-after = %v, want 0", got)
	}
}

func TestIsRetryableEmbedError(t *testing.T) {
	if !IsRetryableEmbedError(&RetryableEmbedError{StatusCode: 429, Message: "x"}) {
		t.Fatal("typed 429 should be retryable")
	}
	if !IsRetryableEmbedError(errors.New("Http Status 429 Too Many Requests")) {
		t.Fatal("plain-text 429 should be retryable")
	}
	if IsRetryableEmbedError(errors.New("invalid input length")) {
		t.Fatal("non-rate-limit error should not be retryable")
	}
}

// retryTestEmbedder returns a configurable number of retryable failures before
// succeeding, so the batch retry loop can be exercised without a live API.
type retryTestEmbedder struct {
	Embedder
	mu           sync.Mutex
	failuresLeft int
	calls        int
}

func (f *retryTestEmbedder) BatchEmbed(ctx context.Context, texts []string) ([][]float32, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	f.calls++
	if f.failuresLeft > 0 {
		f.failuresLeft--
		return nil, &RetryableEmbedError{StatusCode: 429, Message: "throttled"}
	}
	out := make([][]float32, len(texts))
	for i := range texts {
		out[i] = []float32{float32(i)}
	}
	return out, nil
}

func (f *retryTestEmbedder) GetModelName() string { return "fake" }
func (f *retryTestEmbedder) GetDimensions() int   { return 1 }
func (f *retryTestEmbedder) GetModelID() string   { return "fake-id" }

// callCount returns the number of BatchEmbed calls made so far. All counter
// mutations happen under the fixture's mutex, so this is safe to call from
// the test goroutine after BatchEmbedWithPool returns.
func (f *retryTestEmbedder) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func TestBatchEmbedWithPoolRetriesTransientRateLimit(t *testing.T) {
	t.Setenv("BATCH_EMBED_SIZE", "5")
	t.Setenv("EMBED_RETRY_BACKOFF_BASE", "1ms")
	t.Setenv("EMBED_RETRY_BACKOFF_MAX", "10ms")
	pool, err := ants.NewPool(2)
	if err != nil {
		t.Fatalf("new pool: %v", err)
	}
	defer pool.Release()

	// 5 texts -> 1 sub-batch; it fails once (transient rate-limit blip) and
	// succeeds on the single sub-batch retry.
	model := &retryTestEmbedder{failuresLeft: 1}
	be := NewBatchEmbedder(pool)
	texts := make([]string, 5)
	for i := range texts {
		texts[i] = fmt.Sprintf("chunk-%d", i)
	}
	res, err := be.BatchEmbedWithPool(context.Background(), model, texts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res) != 5 {
		t.Fatalf("got %d results, want 5", len(res))
	}
	for i, vec := range res {
		if len(vec) != 1 {
			t.Fatalf("result[%d] has %d dimensions, want 1", i, len(vec))
		}
	}
	if calls := model.callCount(); calls != 2 {
		t.Fatalf("model called %d times, want 2 (initial + 1 retry)", calls)
	}
}

// TestBatchEmbedWithPoolPerSubBatchRetries verifies the sub-batch retry budget
// (embedSubBatchRetryMax): each sub-batch makes at most 1 extra attempt before
// surfacing the error, so a persistently throttled provider fails the whole
// call promptly instead of hammering the API, and no sub-batch hangs.
func TestBatchEmbedWithPoolPerSubBatchRetries(t *testing.T) {
	t.Setenv("BATCH_EMBED_SIZE", "5")
	t.Setenv("EMBED_RETRY_BACKOFF_BASE", "1ms")
	t.Setenv("EMBED_RETRY_BACKOFF_MAX", "10ms")
	pool, err := ants.NewPool(2)
	if err != nil {
		t.Fatalf("new pool: %v", err)
	}
	defer pool.Release()

	// Never succeeds: each of the 2 sub-batches attempts initial + 1 retry.
	model := &retryTestEmbedder{failuresLeft: 1000}
	be := NewBatchEmbedder(pool)
	texts := make([]string, 10)
	for i := range texts {
		texts[i] = fmt.Sprintf("chunk-%d", i)
	}
	_, err = be.BatchEmbedWithPool(context.Background(), model, texts)
	if err == nil {
		t.Fatal("expected an error from a persistently throttled sub-batch, got nil")
	}
	if !IsRetryableEmbedError(err) {
		t.Fatalf("expected the retryable embed error to propagate, got %v", err)
	}
	// 2 sub-batches x (initial + 1 retry) = 4 calls, regardless of scheduling.
	if calls := model.callCount(); calls != 4 {
		t.Fatalf("model called %d times, want 4 (2 sub-batches x 2 attempts)", calls)
	}
}

// TestBatchEmbedWithPoolContextCancellation ensures a cancelled context
// surfaces promptly instead of being swallowed by the retry loop.
func TestBatchEmbedWithPoolContextCancellation(t *testing.T) {
	t.Setenv("BATCH_EMBED_SIZE", "5")
	t.Setenv("EMBED_RETRY_BACKOFF_BASE", "1h") // never wait on the sleep path
	t.Setenv("EMBED_RETRY_BACKOFF_MAX", "1h")
	pool, err := ants.NewPool(2)
	if err != nil {
		t.Fatalf("new pool: %v", err)
	}
	defer pool.Release()

	model := &retryTestEmbedder{failuresLeft: 1000}
	be := NewBatchEmbedder(pool)
	texts := make([]string, 6)
	for i := range texts {
		texts[i] = "chunk"
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = be.BatchEmbedWithPool(ctx, model, texts)
	if err == nil {
		t.Fatal("expected an error after context cancellation, got nil")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}

// TestBatchEmbedWithPoolInvalidBatchSize ensures a non-positive
// BATCH_EMBED_SIZE fails with a config error instead of panicking in the
// chunking/slicing logic.
func TestBatchEmbedWithPoolInvalidBatchSize(t *testing.T) {
	t.Setenv("BATCH_EMBED_SIZE", "0")
	pool, err := ants.NewPool(2)
	if err != nil {
		t.Fatalf("new pool: %v", err)
	}
	defer pool.Release()

	model := &retryTestEmbedder{}
	be := NewBatchEmbedder(pool)
	_, err = be.BatchEmbedWithPool(context.Background(), model, []string{"chunk"})
	if err == nil {
		t.Fatal("expected an error for BATCH_EMBED_SIZE=0, got nil")
	}
}

// TestParseRetryAfterZero verifies that Retry-After: 0 parses to an immediate
// retry (ok=true, 0 delay) rather than being treated as absent.
func TestParseRetryAfterZero(t *testing.T) {
	d, ok := parseRetryAfter("0")
	if !ok {
		t.Fatal("Retry-After: 0 should parse as present")
	}
	if d != 0 {
		t.Fatalf("Retry-After: 0 = %v, want 0 (immediate retry)", d)
	}
	d, ok = parseRetryAfter("")
	if ok || d != 0 {
		t.Fatalf("empty Retry-After = (%v, %v), want (0, false)", d, ok)
	}
}

// TestParseRetryAfterCapped verifies Retry-After delays are capped at the
// configured backoff maximum (default >= 60s).
func TestParseRetryAfterCapped(t *testing.T) {
	t.Setenv("EMBED_RETRY_BACKOFF_MAX", "10s")
	d, ok := parseRetryAfter("30")
	if !ok {
		t.Fatal("Retry-After: 30 should parse")
	}
	if d != 10*time.Second {
		t.Fatalf("Retry-After: 30 with 10s cap = %v, want 10s", d)
	}
	d, ok = parseRetryAfter("Fri, 31 Dec 1999 23:59:59 GMT") // already past
	if !ok || d != 0 {
		t.Fatalf("past HTTP-date Retry-After = (%v, %v), want (0, true) for immediate retry", d, ok)
	}
}
