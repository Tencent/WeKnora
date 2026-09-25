package service

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/hibiken/asynq"
)

// liteMultimodalPending stands in for the Redis multimodal:pending:<kid>
// counter in Lite mode. Lite runs every task inside this process, so an
// in-memory counter sees every image; without one, each finished image
// enqueued post-process, and the first one sealed the enrichment fan-out
// before the other images had written their OCR / caption chunks. The
// counter is lost on restart, where the Lite startup reset already fails
// rows that were still processing.
//
// Keys and semantics mirror Redis: a missing key reads as zero, so a
// decrement on it goes negative and still drives post-process.
var liteMultimodalPending = struct {
	sync.Mutex
	counts map[string]int64
}{counts: map[string]int64{}}

func liteMultimodalSet(key string, n int64) {
	liteMultimodalPending.Lock()
	defer liteMultimodalPending.Unlock()
	liteMultimodalPending.counts[key] = n
}

func liteMultimodalDecrBy(key string, by int64) int64 {
	liteMultimodalPending.Lock()
	defer liteMultimodalPending.Unlock()
	n := liteMultimodalPending.counts[key] - by
	liteMultimodalPending.counts[key] = n
	return n
}

func liteMultimodalDel(key string) {
	liteMultimodalPending.Lock()
	defer liteMultimodalPending.Unlock()
	delete(liteMultimodalPending.counts, key)
}

// postProcessEnqueueAttempts bounds the in-place retry of the post-process
// enqueue. That enqueue is the only thing that moves a knowledge out of
// "processing" once its images are counted; retrying the whole image task
// instead would run the VLM again and write its chunks twice.
const (
	postProcessEnqueueAttempts = 3
	postProcessEnqueueBackoff  = 500 * time.Millisecond
)

// enqueueWithRetry enqueues task, retrying transient failures with a short
// linear backoff. It stops early when ctx is done.
func enqueueWithRetry(ctx context.Context, enq interfaces.TaskEnqueuer, task *asynq.Task) error {
	var lastErr error
	for attempt := 1; attempt <= postProcessEnqueueAttempts; attempt++ {
		_, err := enq.Enqueue(task)
		if err == nil {
			return nil
		}
		lastErr = err
		if attempt == postProcessEnqueueAttempts {
			break
		}
		select {
		case <-time.After(time.Duration(attempt) * postProcessEnqueueBackoff):
		case <-ctx.Done():
			return fmt.Errorf("enqueue %s: %w (last error: %v)", task.Type(), ctx.Err(), lastErr)
		}
	}
	return fmt.Errorf("enqueue %s after %d attempts: %w", task.Type(), postProcessEnqueueAttempts, lastErr)
}
