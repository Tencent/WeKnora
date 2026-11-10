package service

import (
	"context"
	"errors"
	"testing"

	"github.com/Tencent/WeKnora/internal/infrastructure/docparser"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/alicebob/miniredis/v2"
	"github.com/hibiken/asynq"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

// slotReleaseEnqueuer fails the image tasks whose index is in failAt, so a
// test can reproduce a fan-out that only partially reaches the queue.
//
// onImageEnqueued lets a test simulate an image worker that finishes before
// the fan-out loop returns — the ordering that decides whether the shortfall
// release or the last sibling owns the finalize.
type slotReleaseEnqueuer struct {
	failAt          map[int]bool
	imageSeen       int
	imageEnqueued   int
	postProcess     int
	onImageEnqueued func()
}

func (e *slotReleaseEnqueuer) Enqueue(task *asynq.Task, _ ...asynq.Option) (*asynq.TaskInfo, error) {
	switch task.Type() {
	case types.TypeImageMultimodal:
		idx := e.imageSeen
		e.imageSeen++
		if e.failAt[idx] {
			return nil, errors.New("queue write refused")
		}
		e.imageEnqueued++
		if e.onImageEnqueued != nil {
			e.onImageEnqueued()
		}
		return &asynq.TaskInfo{ID: "image"}, nil
	case types.TypeKnowledgePostProcess:
		e.postProcess++
		return &asynq.TaskInfo{ID: "post-process"}, nil
	}
	return &asynq.TaskInfo{ID: "other"}, nil
}

func newSlotReleaseRedis(t *testing.T) (*miniredis.Miniredis, *redis.Client) {
	t.Helper()
	mr, err := miniredis.Run()
	require.NoError(t, err)
	t.Cleanup(mr.Close)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	return mr, rdb
}

func slotReleaseFixture(images int) (
	*types.Knowledge, *types.KnowledgeBase, []docparser.StoredImage, []types.ParsedChunk,
) {
	knowledge := &types.Knowledge{
		ID:              "knowledge-1",
		TenantID:        1,
		KnowledgeBaseID: "kb-1",
		ParseStatus:     types.ParseStatusProcessing,
	}
	kb := &types.KnowledgeBase{ID: "kb-1", TenantID: 1}
	stored := make([]docparser.StoredImage, 0, images)
	for i := 0; i < images; i++ {
		stored = append(stored, docparser.StoredImage{
			OriginalRef: "images/page.png",
			ServingURL:  "local://images/page-" + string(rune('a'+i)) + ".png",
		})
	}
	return knowledge, kb, stored, []types.ParsedChunk{{ChunkID: "chunk-1", Content: "body"}}
}

// The happy path must leave the counter at exactly the number of enqueued
// tasks and must NOT enqueue post-process — the last image to finish owns it.
func TestEnqueueImageMultimodalTasksSeedsCounterForEveryImage(t *testing.T) {
	mr, rdb := newSlotReleaseRedis(t)
	enqueuer := &slotReleaseEnqueuer{}
	svc := &knowledgeService{task: enqueuer, redisClient: rdb}
	knowledge, kb, images, chunks := slotReleaseFixture(3)

	svc.enqueueImageMultimodalTasks(context.Background(), knowledge, kb, images, chunks, nil)

	require.Equal(t, 3, enqueuer.imageEnqueued)
	require.Equal(t, 0, enqueuer.postProcess)
	require.Equal(t, "3", mustGet(t, mr, multimodalPendingKey(knowledge.ID)))
}

// A slot whose task never reached the queue has no owner to drain it. Without
// the release the counter would stall at 2 while only one task can ever
// decrement it, so post-process would never run and the row would sit in
// "processing" until the housekeeping sweep failed it an hour later.
func TestEnqueueImageMultimodalTasksReleasesUnownedSlots(t *testing.T) {
	mr, rdb := newSlotReleaseRedis(t)
	enqueuer := &slotReleaseEnqueuer{failAt: map[int]bool{0: true}}
	svc := &knowledgeService{task: enqueuer, redisClient: rdb}
	knowledge, kb, images, chunks := slotReleaseFixture(2)

	svc.enqueueImageMultimodalTasks(context.Background(), knowledge, kb, images, chunks, nil)

	require.Equal(t, 1, enqueuer.imageEnqueued)
	require.Equal(t, "1", mustGet(t, mr, multimodalPendingKey(knowledge.ID)),
		"the un-enqueued slot must be released so the surviving task can reach zero")
	require.Equal(t, 0, enqueuer.postProcess,
		"one task is still outstanding, so it owns the finalize")
}

// Same shortfall, but the surviving image finishes before the fan-out returns.
// The release is then what brings the counter to zero, so it has to drive
// post-process itself rather than leaving the row for the sweep.
func TestEnqueueImageMultimodalTasksReleaseFinalizesWhenSiblingsAlreadyDone(t *testing.T) {
	mr, rdb := newSlotReleaseRedis(t)
	knowledge, kb, images, chunks := slotReleaseFixture(2)
	enqueuer := &slotReleaseEnqueuer{failAt: map[int]bool{0: true}}
	enqueuer.onImageEnqueued = func() {
		// Mimic a worker that completes immediately: drain its own slot.
		require.NoError(t, rdb.Decr(context.Background(),
			multimodalPendingKey(knowledge.ID)).Err())
	}
	svc := &knowledgeService{task: enqueuer, redisClient: rdb}

	svc.enqueueImageMultimodalTasks(context.Background(), knowledge, kb, images, chunks, nil)

	require.Equal(t, 1, enqueuer.postProcess, "the release owns the finalize here")
	require.False(t, mr.Exists(multimodalPendingKey(knowledge.ID)),
		"counter should be cleared once it reaches zero")
}

// Nothing reached the queue at all, so no image task will ever finalize the
// row whatever the counter says. Post-process has to be driven directly.
func TestEnqueueImageMultimodalTasksFinalizesWhenNoTaskEnqueued(t *testing.T) {
	mr, rdb := newSlotReleaseRedis(t)
	enqueuer := &slotReleaseEnqueuer{failAt: map[int]bool{0: true, 1: true}}
	svc := &knowledgeService{task: enqueuer, redisClient: rdb}
	knowledge, kb, images, chunks := slotReleaseFixture(2)

	svc.enqueueImageMultimodalTasks(context.Background(), knowledge, kb, images, chunks, nil)

	require.Equal(t, 0, enqueuer.imageEnqueued)
	require.Equal(t, 1, enqueuer.postProcess)
	require.False(t, mr.Exists(multimodalPendingKey(knowledge.ID)))
}

// Lite mode: no Redis at all. A partial fan-out must not crash, and the
// surviving task still drives post-process through the nil-client path in
// checkAndFinalizeAllImages — so the fan-out must stay quiet.
func TestEnqueueImageMultimodalTasksWithoutRedisLeavesFinalizeToWorkers(t *testing.T) {
	enqueuer := &slotReleaseEnqueuer{failAt: map[int]bool{0: true}}
	svc := &knowledgeService{task: enqueuer}
	knowledge, kb, images, chunks := slotReleaseFixture(2)

	svc.enqueueImageMultimodalTasks(context.Background(), knowledge, kb, images, chunks, nil)

	require.Equal(t, 1, enqueuer.imageEnqueued)
	require.Equal(t, 0, enqueuer.postProcess)
}

func mustGet(t *testing.T, mr *miniredis.Miniredis, key string) string {
	t.Helper()
	value, err := mr.Get(key)
	require.NoError(t, err)
	return value
}

// failingDecrByHook fails the first `failures` DECRBY commands, so a test can
// drive the release retry deterministically instead of racing miniredis.
type failingDecrByHook struct {
	failures int
	calls    int
}

func (h *failingDecrByHook) DialHook(next redis.DialHook) redis.DialHook { return next }

func (h *failingDecrByHook) ProcessPipelineHook(
	next redis.ProcessPipelineHook,
) redis.ProcessPipelineHook {
	return next
}

func (h *failingDecrByHook) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		if cmd.Name() == "decrby" {
			h.calls++
			if h.calls <= h.failures {
				err := errors.New("redis unavailable")
				cmd.SetErr(err)
				return err
			}
		}
		return next(ctx, cmd)
	}
}

// A transient Redis error must not end the release: it is the only thing that
// can drain an un-owned slot, so giving up on the first error would strand the
// row in "processing" exactly as before this fix.
func TestEnqueueImageMultimodalTasksRetriesSlotRelease(t *testing.T) {
	mr, rdb := newSlotReleaseRedis(t)
	hook := &failingDecrByHook{failures: multimodalSlotReleaseAttempts - 1}
	rdb.AddHook(hook)
	enqueuer := &slotReleaseEnqueuer{failAt: map[int]bool{0: true}}
	svc := &knowledgeService{task: enqueuer, redisClient: rdb}
	knowledge, kb, images, chunks := slotReleaseFixture(2)

	svc.enqueueImageMultimodalTasks(context.Background(), knowledge, kb, images, chunks, nil)

	require.Equal(t, multimodalSlotReleaseAttempts, hook.calls, "every attempt should be used")
	require.Equal(t, "1", mustGet(t, mr, multimodalPendingKey(knowledge.ID)),
		"the retry that succeeded must still release the un-owned slot")
}

// When Redis stays down the release cannot happen, so the row is left to the
// housekeeping sweep. Assert the bounded retry rather than an unbounded loop,
// and that nothing is finalized on a counter we failed to settle.
func TestEnqueueImageMultimodalTasksGivesUpSlotReleaseAfterRetries(t *testing.T) {
	mr, rdb := newSlotReleaseRedis(t)
	hook := &failingDecrByHook{failures: multimodalSlotReleaseAttempts + 5}
	rdb.AddHook(hook)
	enqueuer := &slotReleaseEnqueuer{failAt: map[int]bool{0: true}}
	svc := &knowledgeService{task: enqueuer, redisClient: rdb}
	knowledge, kb, images, chunks := slotReleaseFixture(2)

	svc.enqueueImageMultimodalTasks(context.Background(), knowledge, kb, images, chunks, nil)

	require.Equal(t, multimodalSlotReleaseAttempts, hook.calls, "retries must stay bounded")
	require.Equal(t, "2", mustGet(t, mr, multimodalPendingKey(knowledge.ID)))
	require.Equal(t, 0, enqueuer.postProcess,
		"must not finalize on a counter the release could not settle")
}

// The fan-out can be reached with an already-cancelled context (graceful
// shutdown). The nothing-enqueued branch still has to clear the seeded key, or
// it lingers for its full 24h TTL; asynq's Enqueue takes no context, so
// post-process still goes out.
func TestEnqueueImageMultimodalTasksClearsCounterOnCancelledContext(t *testing.T) {
	mr, rdb := newSlotReleaseRedis(t)
	enqueuer := &slotReleaseEnqueuer{failAt: map[int]bool{0: true, 1: true}}
	svc := &knowledgeService{task: enqueuer, redisClient: rdb}
	knowledge, kb, images, chunks := slotReleaseFixture(2)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	svc.enqueueImageMultimodalTasks(ctx, knowledge, kb, images, chunks, nil)

	require.False(t, mr.Exists(multimodalPendingKey(knowledge.ID)),
		"seeded counter must be cleared on a detached context")
	require.Equal(t, 1, enqueuer.postProcess)
}
