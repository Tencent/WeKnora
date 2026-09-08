package embedding

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/models/utils"
	"github.com/panjf2000/ants/v2"
)

// Defaults for the batch pool. Overridable via env without rebuilding:
//
//	BATCH_EMBED_SIZE            – texts per provider call (default 5)
//	EMBED_REQUEST_INTERVAL_MS   – min spacing between sub-batch starts (default 0)
//	EMBED_BATCH_MAX_RETRIES     – retries for failed sub-batches only (default 3)
const (
	defaultBatchEmbedSize      = 5
	defaultBatchMaxRetries     = 3
	defaultBatchRetryBaseDelay = 500 * time.Millisecond
	defaultBatchRetryMaxDelay  = 15 * time.Second
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

// batchJob is one sub-batch of texts submitted to the provider together.
type batchJob struct {
	items []*textEmbedding
	err   error
}

// BatchEmbedWithPool splits texts into sub-batches, embeds them through the
// worker pool, and — critically — only retries the sub-batches that failed.
// Successful results are preserved so a transient 429 on one sub-batch does
// not force the entire document to be re-embedded (see #2533).
//
// Optional rate pacing: when EMBED_REQUEST_INTERVAL_MS > 0, each sub-batch
// start is spaced by at least that many milliseconds. This complements the
// concurrency governor (which only caps in-flight calls) by bounding request
// rate against provider RPM/TPM limits.
func (e *batchEmbedder) BatchEmbedWithPool(ctx context.Context, model Embedder, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return [][]float32{}, nil
	}

	batchSize, err := envInt("BATCH_EMBED_SIZE", defaultBatchEmbedSize)
	if err != nil {
		return nil, err
	}
	if batchSize < 1 {
		batchSize = defaultBatchEmbedSize
	}
	maxRetries, err := envInt("EMBED_BATCH_MAX_RETRIES", defaultBatchMaxRetries)
	if err != nil {
		return nil, err
	}
	if maxRetries < 0 {
		maxRetries = 0
	}
	requestInterval := envDurationMS("EMBED_REQUEST_INTERVAL_MS", 0)

	textEmbeddings := utils.MapSlice(texts, func(text string) *textEmbedding {
		return &textEmbedding{text: text}
	})
	jobs := make([]*batchJob, 0)
	for _, chunk := range utils.ChunkSlice(textEmbeddings, batchSize) {
		jobs = append(jobs, &batchJob{items: chunk})
	}

	// Pace + run a wave of jobs. First wave runs everything; subsequent waves
	// only include jobs that still lack results.
	pending := jobs
	for attempt := 0; attempt <= maxRetries; attempt++ {
		if len(pending) == 0 {
			break
		}
		if attempt > 0 {
			// Honour the longest Retry-After among the failed jobs when the
			// provider told us how long to wait; otherwise exponential backoff.
			wait := backoffForFailedJobs(pending, attempt)
			logger.GetLogger(ctx).Warnf(
				"BatchEmbedWithPool retrying %d/%d failed sub-batches (attempt %d/%d), waiting %v",
				len(pending), len(jobs), attempt, maxRetries, wait,
			)
			if err := waitCtx(ctx, wait); err != nil {
				return nil, err
			}
		}
		if err := e.runJobs(ctx, model, pending, requestInterval); err != nil {
			return nil, err
		}
		pending = filterFailedJobs(pending)
	}

	if len(pending) > 0 {
		// Surface the first remaining error so callers can log / classify it.
		return nil, pending[0].err
	}

	results := utils.MapSlice(textEmbeddings, func(text *textEmbedding) []float32 {
		return text.results
	})
	// Defensive: every text should now have a non-nil vector. A nil result
	// would only happen if a job reported success without writing — treat as
	// hard failure so we never index zero-vectors silently.
	for i, vec := range results {
		if vec == nil {
			return nil, fmt.Errorf("embedding missing for text index %d after retries", i)
		}
	}
	return results, nil
}

// runJobs submits the given jobs to the ants pool (optionally paced) and
// waits for completion. It does not short-circuit remaining work on the
// first error: every job gets a chance so successes can be retained.
func (e *batchEmbedder) runJobs(
	ctx context.Context,
	model Embedder,
	jobs []*batchJob,
	requestInterval time.Duration,
) error {
	var (
		wg       sync.WaitGroup
		paceMu   sync.Mutex
		nextSlot = time.Now()
	)

	process := func(job *batchJob) func() {
		return func() {
			defer wg.Done()
			if err := ctx.Err(); err != nil {
				job.err = err
				return
			}

			// Rate pacing: reserve the next start slot under the mutex so
			// concurrent workers queue on distinct slots instead of all
			// sleeping until the same deadline (thundering herd). The
			// actual provider call runs outside the lock.
			if requestInterval > 0 {
				paceMu.Lock()
				now := time.Now()
				var sleep time.Duration
				if now.Before(nextSlot) {
					sleep = nextSlot.Sub(now)
					nextSlot = nextSlot.Add(requestInterval)
				} else {
					nextSlot = now.Add(requestInterval)
				}
				paceMu.Unlock()
				if err := waitCtx(ctx, sleep); err != nil {
					job.err = err
					return
				}
			}

			inputs := utils.MapSlice(job.items, func(t *textEmbedding) string { return t.text })
			embedding, err := model.BatchEmbed(ctx, inputs)
			if err != nil {
				job.err = err
				return
			}
			if len(embedding) != len(job.items) {
				job.err = fmt.Errorf("embedding model returned %d embeddings for %d inputs",
					len(embedding), len(job.items))
				return
			}
			for i, text := range job.items {
				if text == nil {
					continue
				}
				text.results = embedding[i]
			}
			job.err = nil
		}
	}

	for _, job := range jobs {
		// Skip jobs that already have results (defensive — filterFailedJobs
		// should have removed them, but keep the invariant local).
		if jobHasResults(job) {
			continue
		}
		wg.Add(1)
		if err := e.pool.Submit(process(job)); err != nil {
			wg.Done()
			return err
		}
	}
	wg.Wait()
	return ctx.Err()
}

func jobHasResults(job *batchJob) bool {
	if job == nil || len(job.items) == 0 {
		return true
	}
	for _, item := range job.items {
		if item == nil || item.results == nil {
			return false
		}
	}
	return true
}

func filterFailedJobs(jobs []*batchJob) []*batchJob {
	var failed []*batchJob
	for _, job := range jobs {
		if !jobHasResults(job) {
			failed = append(failed, job)
		}
	}
	return failed
}

// backoffForFailedJobs picks a wait duration for the next retry wave.
// Prefer the largest Retry-After advertised by a RateLimitError; otherwise
// fall back to jittered exponential backoff based on the attempt number.
func backoffForFailedJobs(jobs []*batchJob, attempt int) time.Duration {
	var maxRetryAfter time.Duration
	for _, job := range jobs {
		var rl *RateLimitError
		if errors.As(job.err, &rl) && rl.RetryAfter > maxRetryAfter {
			maxRetryAfter = rl.RetryAfter
		}
	}
	if maxRetryAfter > 0 {
		return maxRetryAfter
	}
	// attempt is 1-based for the first retry wave.
	return jitteredBackoff(defaultBatchRetryBaseDelay, attempt-1, defaultBatchRetryMaxDelay)
}

// envInt reads an integer env var or returns the default. Empty string → default.
// Non-numeric values return an error so misconfiguration is visible at startup.
func envInt(name string, def int) (int, error) {
	raw := os.Getenv(name)
	if raw == "" {
		return def, nil
	}
	v, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", name, err)
	}
	return v, nil
}

// envDurationMS reads a millisecond env var into a time.Duration.
func envDurationMS(name string, def time.Duration) time.Duration {
	raw := os.Getenv(name)
	if raw == "" {
		return def
	}
	v, err := strconv.Atoi(raw)
	if err != nil || v < 0 {
		return def
	}
	return time.Duration(v) * time.Millisecond
}
