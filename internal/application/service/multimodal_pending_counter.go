package service

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/hibiken/asynq"
	"github.com/redis/go-redis/v9"
)

// liteMultimodalPending stands in for the Redis multimodal:pending:<kid>
// counter in Lite mode. Lite runs every task inside this process, so an
// in-memory counter sees every image; without one, each finished image
// enqueued post-process, and the first one sealed the enrichment fan-out
// before the other images had written their OCR / caption chunks. The
// counter is lost on restart, where the Lite startup reset already fails
// rows that were still processing.
//
// Legacy tasks without a batch retain the missing-key fallback. A task carrying
// a batch may change the counter only while that batch still owns it.
var liteMultimodalPending = struct {
	sync.Mutex
	counts  map[string]int64
	batches map[string]string
}{counts: map[string]int64{}, batches: map[string]string{}}

func liteMultimodalSet(key string, n int64) {
	liteMultimodalPending.Lock()
	defer liteMultimodalPending.Unlock()
	liteMultimodalPending.counts[key] = n
	delete(liteMultimodalPending.batches, key)
}

func liteMultimodalDel(key string) {
	liteMultimodalPending.Lock()
	defer liteMultimodalPending.Unlock()
	delete(liteMultimodalPending.counts, key)
	delete(liteMultimodalPending.batches, key)
}

// Check ownership in the same operation as the mutation: a reparse can seed
// its counter while an older image worker or fan-out is still finishing.
var decrementMultimodalCounterScript = redis.NewScript(`
if (redis.call('GET', KEYS[2]) or '') ~= ARGV[1] then
    return {0, 0}
end
return {1, redis.call('DECRBY', KEYS[1], ARGV[2])}
`)

var deleteMultimodalCounterScript = redis.NewScript(`
if (redis.call('GET', KEYS[2]) or '') ~= ARGV[1] then
    return 0
end
return redis.call('DEL', KEYS[1], KEYS[2])
`)

func seedMultimodalCounter(ctx context.Context, client *redis.Client, key, batch string, count int64) error {
	if client != nil {
		_, err := client.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
			pipe.Set(ctx, key, count, 24*time.Hour)
			pipe.Set(ctx, key+":batch", batch, 24*time.Hour)
			return nil
		})
		return err
	}
	liteMultimodalPending.Lock()
	defer liteMultimodalPending.Unlock()
	liteMultimodalPending.counts[key] = count
	liteMultimodalPending.batches[key] = batch
	return nil
}

func decrementMultimodalCounter(
	ctx context.Context, client *redis.Client, key, batch string, by int64,
) (int64, bool, error) {
	if client != nil {
		result, err := decrementMultimodalCounterScript.Run(ctx, client, []string{key, key + ":batch"}, batch, by).
			Int64Slice()
		if err != nil {
			return 0, false, err
		}
		return result[1], result[0] == 1, nil
	}
	liteMultimodalPending.Lock()
	defer liteMultimodalPending.Unlock()
	if liteMultimodalPending.batches[key] != batch {
		return 0, false, nil
	}
	pending := liteMultimodalPending.counts[key] - by
	liteMultimodalPending.counts[key] = pending
	return pending, true, nil
}

func deleteMultimodalCounter(ctx context.Context, client *redis.Client, key, batch string) {
	if client != nil {
		deleteMultimodalCounterScript.Run(ctx, client, []string{key, key + ":batch"}, batch)
		return
	}
	liteMultimodalPending.Lock()
	defer liteMultimodalPending.Unlock()
	if liteMultimodalPending.batches[key] == batch {
		delete(liteMultimodalPending.counts, key)
		delete(liteMultimodalPending.batches, key)
	}
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
