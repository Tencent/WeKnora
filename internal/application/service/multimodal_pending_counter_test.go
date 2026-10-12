package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/hibiken/asynq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// liteCounter reads the in-process multimodal counter the way Redis would
// report it: present or not, and its value.
func liteCounter(knowledgeID string) (int64, bool) {
	liteMultimodalPending.Lock()
	defer liteMultimodalPending.Unlock()
	n, ok := liteMultimodalPending.counts[multimodalPendingKey(knowledgeID)]
	return n, ok
}

// useLiteCounter gives a test its own counter key and removes it afterwards;
// the Lite counter is process-global.
func useLiteCounter(t *testing.T, knowledgeID string) string {
	t.Helper()
	key := multimodalPendingKey(knowledgeID)
	liteMultimodalDel(key)
	t.Cleanup(func() { liteMultimodalDel(key) })
	return key
}

func liteImagePayload(knowledgeID string, attempt int) types.ImageMultimodalPayload {
	return types.ImageMultimodalPayload{
		TenantID:        1,
		KnowledgeID:     knowledgeID,
		KnowledgeBaseID: "kb-1",
		ImageURL:        "local://images/a.png",
		Language:        "en-US",
		Attempt:         attempt,
	}
}

func postProcessPayloads(t *testing.T, tasks []*asynq.Task) []types.KnowledgePostProcessPayload {
	t.Helper()
	var out []types.KnowledgePostProcessPayload
	for _, task := range tasks {
		if task.Type() != types.TypeKnowledgePostProcess {
			continue
		}
		var payload types.KnowledgePostProcessPayload
		require.NoError(t, json.Unmarshal(task.Payload(), &payload))
		out = append(out, payload)
	}
	return out
}

// Lite mode has no Redis, so the fan-out seeds the in-process counter with
// one slot per image; without it the first finished image sealed post-process
// before its siblings had written their chunks.
func TestEnqueueImageMultimodalTasksSeedsLiteCounter(t *testing.T) {
	enqueuer := &slotReleaseEnqueuer{}
	svc := &knowledgeService{task: enqueuer}
	knowledge, kb, images, chunks := slotReleaseFixture(3)
	knowledge.ID = "lite-seed"
	useLiteCounter(t, knowledge.ID)

	err := svc.enqueueImageMultimodalTasks(context.Background(), knowledge, kb, images, chunks, nil)

	require.NoError(t, err)
	assert.Equal(t, 3, enqueuer.imageEnqueued)
	assert.Zero(t, enqueuer.postProcess)
	n, ok := liteCounter(knowledge.ID)
	require.True(t, ok)
	assert.Equal(t, int64(3), n)
}

// A Lite fan-out that loses one image task releases that slot, so the
// surviving image is the one that reaches zero and drives post-process.
func TestLiteMultimodalShortfallLetsLastImageFinalize(t *testing.T) {
	enqueuer := &slotReleaseEnqueuer{failAt: map[int]bool{0: true}}
	svc := &knowledgeService{task: enqueuer}
	knowledge, kb, images, chunks := slotReleaseFixture(2)
	knowledge.ID = "lite-shortfall"
	useLiteCounter(t, knowledge.ID)

	require.NoError(t, svc.enqueueImageMultimodalTasks(context.Background(), knowledge, kb, images, chunks, nil))

	n, _ := liteCounter(knowledge.ID)
	assert.Equal(t, int64(1), n, "the un-enqueued slot is released")
	assert.Zero(t, enqueuer.postProcess)

	images2 := &orphanTaskEnqueuer{}
	imageSvc := &ImageMultimodalService{taskEnqueuer: images2}
	require.NoError(t, imageSvc.checkAndFinalizeAllImages(context.Background(), liteImagePayload(knowledge.ID, 1)))

	assert.Len(t, postProcessPayloads(t, images2.enqueued), 1)
	_, ok := liteCounter(knowledge.ID)
	assert.False(t, ok, "counter is removed once post-process is enqueued")
}

// With no image task on the queue, the fan-out itself drives post-process;
// if that enqueue fails the parse task must fail so asynq retries it.
func TestEnqueueImageMultimodalTasksLiteReturnsPostProcessEnqueueError(t *testing.T) {
	queueErr := errors.New("queue unavailable")
	enqueuer := &failingEnqueuer{err: queueErr}
	svc := &knowledgeService{task: enqueuer}
	knowledge, kb, images, chunks := slotReleaseFixture(2)
	knowledge.ID = "lite-nothing-enqueued"
	useLiteCounter(t, knowledge.ID)
	// A cancelled context ends the in-place retry after the first attempt.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := svc.enqueueImageMultimodalTasks(ctx, knowledge, kb, images, chunks, nil)

	require.ErrorContains(t, err, queueErr.Error())
	_, ok := liteCounter(knowledge.ID)
	assert.False(t, ok, "the seeded counter is cleared when no image task exists")
}

// Lite mode counts every image before sealing: the first of two images must
// not enqueue post-process, the second does and clears the counter, and the
// post-process task carries the attempt so it can skip itself if superseded.
func TestCheckAndFinalizeAllImagesLiteWaitsForEveryImage(t *testing.T) {
	const knowledgeID = "lite-fan-in"
	key := useLiteCounter(t, knowledgeID)
	liteMultimodalSet(key, 2)
	enqueuer := &orphanTaskEnqueuer{}
	svc := &ImageMultimodalService{taskEnqueuer: enqueuer}

	require.NoError(t, svc.checkAndFinalizeAllImages(context.Background(), liteImagePayload(knowledgeID, 4)))

	assert.Empty(t, enqueuer.enqueued, "one image is still outstanding")
	n, _ := liteCounter(knowledgeID)
	assert.Equal(t, int64(1), n)

	require.NoError(t, svc.checkAndFinalizeAllImages(context.Background(), liteImagePayload(knowledgeID, 4)))

	payloads := postProcessPayloads(t, enqueuer.enqueued)
	require.Len(t, payloads, 1)
	assert.Equal(t, 4, payloads[0].Attempt)
	assert.Equal(t, knowledgeID, payloads[0].KnowledgeID)
	assert.Equal(t, "en-US", payloads[0].Language)
	_, ok := liteCounter(knowledgeID)
	assert.False(t, ok)
}

// A failed post-process enqueue is returned, and the counter is kept at zero
// so the retried image task finds it drained and enqueues again.
func TestCheckAndFinalizeAllImagesReturnsEnqueueErrorAndKeepsCounter(t *testing.T) {
	const knowledgeID = "lite-enqueue-fails"
	key := useLiteCounter(t, knowledgeID)
	liteMultimodalSet(key, 1)
	queueErr := errors.New("queue unavailable")
	svc := &ImageMultimodalService{taskEnqueuer: &failingEnqueuer{err: queueErr}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := svc.checkAndFinalizeAllImages(ctx, liteImagePayload(knowledgeID, 1))

	require.ErrorContains(t, err, queueErr.Error())
	_, ok := liteCounter(knowledgeID)
	require.True(t, ok, "the counter must survive a failed enqueue")

	enqueuer := &orphanTaskEnqueuer{}
	svc.taskEnqueuer = enqueuer
	require.NoError(t, svc.checkAndFinalizeAllImages(context.Background(), liteImagePayload(knowledgeID, 1)))
	assert.Len(t, postProcessPayloads(t, enqueuer.enqueued), 1, "the retry still finalizes")
}

type failIfCalledKnowledgeRepo struct {
	interfaces.KnowledgeRepository
	t *testing.T
}

func (r failIfCalledKnowledgeRepo) GetKnowledgeByIDOnly(context.Context, string) (*types.Knowledge, error) {
	r.t.Fatal("a superseded image must not be processed")
	return nil, nil
}

func imageMultimodalTask(t *testing.T, payload types.ImageMultimodalPayload) *asynq.Task {
	t.Helper()
	raw, err := json.Marshal(payload)
	require.NoError(t, err)
	return asynq.NewTask(types.TypeImageMultimodal, raw)
}

// A reparse re-seeded the counter for its own images; a stale image from the
// previous attempt must not count against it.
func TestImageMultimodalHandleSupersededAttemptLeavesCounter(t *testing.T) {
	const knowledgeID = "lite-image-superseded"
	key := useLiteCounter(t, knowledgeID)
	liteMultimodalSet(key, 1)
	enqueuer := &orphanTaskEnqueuer{}
	svc := &ImageMultimodalService{
		knowledgeRepo: failIfCalledKnowledgeRepo{t: t},
		taskEnqueuer:  enqueuer,
		spanTracker:   newAttemptTracker(2),
	}

	err := svc.Handle(context.Background(), imageMultimodalTask(t, liteImagePayload(knowledgeID, 1)))

	require.NoError(t, err)
	n, _ := liteCounter(knowledgeID)
	assert.Equal(t, int64(1), n)
	assert.Empty(t, enqueuer.enqueued)
}

// When the orphan check cannot read the row on the last attempt no retry
// follows, so the image is still counted (here it is the last one and seals
// post-process) and the read error is still reported. Earlier attempts leave
// the counter for the retry.
func TestImageMultimodalHandleCountsImageWhenOrphanCheckFailsOnFinalAttempt(t *testing.T) {
	readErr := errors.New("db unavailable")
	tests := []struct {
		name          string
		retried       int
		wantCounted   bool
		wantPostCount int
	}{
		{name: "final attempt", retried: 3, wantCounted: true, wantPostCount: 1},
		{name: "retry remains", retried: 1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			knowledgeID := "lite-orphan-read-" + test.name
			key := useLiteCounter(t, knowledgeID)
			liteMultimodalSet(key, 1)
			enqueuer := &orphanTaskEnqueuer{}
			svc := &ImageMultimodalService{
				knowledgeRepo: &orphanKnowledgeRepo{err: readErr},
				taskEnqueuer:  enqueuer,
			}
			ctx := types.WithTaskRetryMetadata(context.Background(), test.retried, 3)

			err := svc.Handle(ctx, imageMultimodalTask(t, liteImagePayload(knowledgeID, 0)))

			require.ErrorIs(t, err, readErr)
			assert.Len(t, postProcessPayloads(t, enqueuer.enqueued), test.wantPostCount)
			n, ok := liteCounter(knowledgeID)
			if test.wantCounted {
				assert.False(t, ok, "the last image drained the counter")
			} else {
				assert.Equal(t, int64(1), n, "a retry will count the image")
			}
		})
	}
}

type flakyEnqueuer struct {
	failures int
	calls    int
}

func (e *flakyEnqueuer) Enqueue(*asynq.Task, ...asynq.Option) (*asynq.TaskInfo, error) {
	e.calls++
	if e.calls <= e.failures {
		return nil, errors.New("transient")
	}
	return &asynq.TaskInfo{ID: "ok"}, nil
}

func TestEnqueueWithRetryRecoversFromTransientFailure(t *testing.T) {
	enqueuer := &flakyEnqueuer{failures: 1}

	err := enqueueWithRetry(context.Background(), enqueuer, asynq.NewTask(types.TypeKnowledgePostProcess, nil))

	require.NoError(t, err)
	assert.Equal(t, 2, enqueuer.calls)
}

func TestEnqueueWithRetryStopsWhenContextDone(t *testing.T) {
	enqueuer := &flakyEnqueuer{failures: postProcessEnqueueAttempts}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	started := time.Now()

	err := enqueueWithRetry(ctx, enqueuer, asynq.NewTask(types.TypeKnowledgePostProcess, nil))

	require.ErrorIs(t, err, context.Canceled)
	assert.Equal(t, 1, enqueuer.calls)
	assert.Less(t, time.Since(started), postProcessEnqueueBackoff)
}
