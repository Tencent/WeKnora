package embedding

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Tencent/WeKnora/internal/models/utils"
	"github.com/panjf2000/ants/v2"
)

type batchEmbedder struct {
	pool *ants.Pool
}

func NewBatchEmbedder(pool *ants.Pool) EmbedderPooler {
	return &batchEmbedder{pool: pool}
}

type textEmbedding struct {
	text    string
	results []float32
}

// embedThrottle is a per-call adaptive pacing gate. When embedding requests
// start receiving rate-limit responses, the level rises and every sub-batch
// waits `level × step` before its next request; consecutive successes decay
// the level back down. This keeps the aggregate request rate below a
// provider's TPM/RPM ceiling without freezing the whole pipeline — the
// in-task equivalent of what previously required manual
// CONCURRENCY_POOL_SIZE / BATCH_EMBED_SIZE tuning.
type embedThrottle struct {
	level atomic.Int32
}

// embedThrottleStep is the per-level sleep (EMBED_THROTTLE_STEP, default 300ms).
func embedThrottleStep() time.Duration {
	if v := os.Getenv("EMBED_THROTTLE_STEP"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 && d <= 10*time.Second {
			return d
		}
	}
	return 300 * time.Millisecond
}

// embedThrottleMaxLevel caps the throttle escalation (levels double the
// penalty effect they would have unbounded).
const embedThrottleMaxLevel = 6

// wait sleeps proportionally to the current throttle level.
func (t *embedThrottle) wait(ctx context.Context) {
	if l := t.level.Load(); l > 0 {
		sleepWithContext(ctx, time.Duration(l)*embedThrottleStep())
	}
}

// onRateLimit escalates the throttle after a rate-limit failure.
func (t *embedThrottle) onRateLimit() {
	if t.level.Load() < embedThrottleMaxLevel {
		t.level.Add(1)
	}
}

// onSuccess decays the throttle after a clean success.
func (t *embedThrottle) onSuccess() {
	if t.level.Load() > 0 {
		t.level.Add(-1)
	}
}

// embedSubBatchRetryMax bounds the extra retries applied per sub-batch on top
// of the request-level retries each embedder performs internally. The shared
// HTTP retry layer (retry_http.go) owns Retry-After/status backoff; this
// single extra attempt is only a cheap local backstop so an isolated blip
// does not fail the whole batch.
const embedSubBatchRetryMax = 1

func (e *batchEmbedder) BatchEmbedWithPool(ctx context.Context, model Embedder, texts []string) ([][]float32, error) {
	// Create goroutine pool for concurrent processing of document chunks
	var wg sync.WaitGroup
	var mu sync.Mutex  // For synchronizing access to error
	var firstErr error // Record the first error that occurs
	batchSizeStr := os.Getenv("BATCH_EMBED_SIZE")
	if batchSizeStr == "" {
		batchSizeStr = "5"
	}
	batchSize, err := strconv.Atoi(batchSizeStr)
	if err != nil {
		return nil, err
	}
	if batchSize <= 0 {
		return nil, fmt.Errorf("invalid BATCH_EMBED_SIZE %d: must be a positive integer", batchSize)
	}
	textEmbeddings := utils.MapSlice(texts, func(text string) *textEmbedding {
		return &textEmbedding{text: text}
	})

	var throttle embedThrottle

	// Function to process each document chunk
	processChunk := func(texts []*textEmbedding) func() {
		return func() {
			defer wg.Done()
			// If an error has already occurred, don't continue processing
			mu.Lock()
			hasError := firstErr != nil
			mu.Unlock()
			if hasError {
				return
			}

			input := utils.MapSlice(texts, func(text *textEmbedding) string {
				return text.text
			})
			// Retry retryable failures (429/5xx) per sub-batch so a transient
			// quota blip no longer aborts the whole document. Non-retryable
			// errors fail fast and keep the historical all-or-nothing behavior.
			var embedding [][]float32
			var err error
			for attempt := 0; ; attempt++ {
				throttle.wait(ctx)
				embedding, err = model.BatchEmbed(ctx, input)
				if err == nil {
					throttle.onSuccess()
					break
				}
				if !IsRetryableEmbedError(err) || attempt >= embedSubBatchRetryMax {
					break
				}
				throttle.onRateLimit()
				sleepWithContext(ctx, embedRetryBackoff(attempt+1))
			}
			if err != nil {
				mu.Lock()
				if firstErr == nil {
					firstErr = err
				}
				mu.Unlock()
				return
			}
			if len(embedding) != len(texts) {
				mu.Lock()
				if firstErr == nil {
					firstErr = fmt.Errorf("embedding model returned %d embeddings for %d inputs", len(embedding), len(texts))
				}
				mu.Unlock()
				return
			}
			mu.Lock()
			for i, text := range texts {
				if text == nil {
					continue
				}
				text.results = embedding[i]
			}
			mu.Unlock()
		}
	}

	// Count each task before submission. ants may start a task immediately
	// after Submit returns, so adding to the WaitGroup afterwards can race
	// with the task's Done call and drive the counter negative.
	chunks := utils.ChunkSlice(textEmbeddings, batchSize)
	for _, texts := range chunks {
		wg.Add(1)
		if err := e.pool.Submit(processChunk(texts)); err != nil {
			wg.Done()
			wg.Wait()
			return nil, fmt.Errorf("submit embedding task: %w", err)
		}
	}

	// Wait for all tasks to complete
	wg.Wait()

	// Check if any errors occurred
	mu.Lock()
	err = firstErr
	mu.Unlock()
	if err != nil {
		return nil, err
	}

	results := utils.MapSlice(textEmbeddings, func(text *textEmbedding) []float32 {
		return text.results
	})
	return results, nil
}
