package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/hibiken/asynq"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

type multimodalGenerationEnqueuer struct {
	tasks []*asynq.Task
}

func (e *multimodalGenerationEnqueuer) Enqueue(task *asynq.Task, _ ...asynq.Option) (*asynq.TaskInfo, error) {
	e.tasks = append(e.tasks, task)
	return &asynq.TaskInfo{}, nil
}

func TestMultimodalReparseKeepsNewCounter(t *testing.T) {
	for _, mode := range []string{"lite", "redis"} {
		t.Run(mode, func(t *testing.T) {
			for _, stage := range []string{"before decrement", "legacy before decrement", "before counter cleanup"} {
				t.Run(stage, func(t *testing.T) {
					ctx := context.Background()
					var rdb *redis.Client
					if mode == "redis" {
						_, rdb = newSlotReleaseRedis(t)
					}
					knowledge, kb, images, chunks := slotReleaseFixture(2)
					knowledge.ID = mode + "-" + stage
					key := useLiteCounter(t, knowledge.ID)
					queue := &multimodalGenerationEnqueuer{}
					svc := &knowledgeService{task: queue, redisClient: rdb}
					oldImages := images
					if stage == "before counter cleanup" {
						oldImages = images[:1]
					}
					require.NoError(t, svc.enqueueImageMultimodalTasks(ctx, knowledge, kb, oldImages, chunks, nil))
					var old types.ImageMultimodalPayload
					require.NoError(t, json.Unmarshal(queue.tasks[0].Payload(), &old))
					if stage == "legacy before decrement" {
						old.CounterBatch = ""
					}

					reparse := func() {
						require.NoError(t, svc.enqueueImageMultimodalTasks(ctx, knowledge, kb, images, chunks, nil))
					}
					finalize := &slotReleaseEnqueuer{}
					imageSvc := &ImageMultimodalService{redisClient: rdb, taskEnqueuer: finalize}
					if stage != "before counter cleanup" {
						reparse()
					} else {
						// A new parse seeds its counter while the old finalizer is enqueueing.
						imageSvc.taskEnqueuer = generationEnqueueFunc(func(*asynq.Task) error {
							reparse()
							return nil
						})
					}

					require.NoError(t, imageSvc.checkAndFinalizeAllImages(ctx, old))
					if rdb == nil {
						pending, exists := liteCounter(knowledge.ID)
						require.True(t, exists, "the old finalizer must not remove the new counter")
						require.Equal(t, int64(2), pending)
					} else {
						pending, err := rdb.Get(ctx, key).Int64()
						require.NoError(t, err, "the old finalizer must not remove the new counter")
						require.Equal(t, int64(2), pending)
					}
					require.Zero(t, finalize.postProcess)

					var first, second types.ImageMultimodalPayload
					require.NoError(t, json.Unmarshal(queue.tasks[len(queue.tasks)-2].Payload(), &first))
					require.NoError(t, json.Unmarshal(queue.tasks[len(queue.tasks)-1].Payload(), &second))
					imageSvc.taskEnqueuer = finalize
					require.NoError(t, imageSvc.checkAndFinalizeAllImages(ctx, first))
					require.Zero(t, finalize.postProcess, "the new parse still has one outstanding image")
					require.NoError(t, imageSvc.checkAndFinalizeAllImages(ctx, second))
					require.Equal(t, 1, finalize.postProcess)
				})
			}
		})
	}
}

func TestMultimodalCounterSeedFailureDoesNotEnqueueImages(t *testing.T) {
	_, rdb := newSlotReleaseRedis(t)
	require.NoError(t, rdb.Close())
	queue := &multimodalGenerationEnqueuer{}
	svc := &knowledgeService{task: queue, redisClient: rdb}
	knowledge, kb, images, chunks := slotReleaseFixture(2)

	err := svc.enqueueImageMultimodalTasks(context.Background(), knowledge, kb, images, chunks, nil)

	require.Error(t, err)
	require.Empty(t, queue.tasks)
}

type generationEnqueueFunc func(*asynq.Task) error

func (f generationEnqueueFunc) Enqueue(task *asynq.Task, _ ...asynq.Option) (*asynq.TaskInfo, error) {
	return &asynq.TaskInfo{}, f(task)
}

func TestMultimodalBatchCounterSurvivesPostProcessRetry(t *testing.T) {
	for _, mode := range []string{"lite", "redis"} {
		t.Run(mode, func(t *testing.T) {
			var rdb *redis.Client
			if mode == "redis" {
				_, rdb = newSlotReleaseRedis(t)
			}
			key := useLiteCounter(t, "post-process-retry-"+mode)
			batch := "current-batch"
			require.NoError(t, seedMultimodalCounter(context.Background(), rdb, key, batch, 1))
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			enqueueErr := errors.New("queue unavailable")
			svc := &ImageMultimodalService{
				redisClient: rdb,
				taskEnqueuer: generationEnqueueFunc(func(*asynq.Task) error {
					cancel()
					return enqueueErr
				}),
			}
			payload := liteImagePayload("post-process-retry-"+mode, 1)
			payload.CounterBatch = batch
			require.Error(t, svc.checkAndFinalizeAllImages(ctx, payload))
			queue := &slotReleaseEnqueuer{}
			svc.taskEnqueuer = queue

			require.NoError(t, svc.checkAndFinalizeAllImages(context.Background(), payload))

			require.Equal(t, 1, queue.postProcess)
			if rdb == nil {
				_, exists := liteCounter(payload.KnowledgeID)
				require.False(t, exists)
			} else {
				require.Equal(t, int64(0), rdb.Exists(context.Background(), key, key+":batch").Val())
			}
		})
	}
}

func TestMultimodalShortfallCannotDrainNewBatch(t *testing.T) {
	for _, mode := range []string{"lite", "redis"} {
		t.Run(mode, func(t *testing.T) {
			var rdb *redis.Client
			if mode == "redis" {
				_, rdb = newSlotReleaseRedis(t)
			}
			knowledge, kb, images, chunks := slotReleaseFixture(2)
			knowledge.ID = "shortfall-reparse-" + mode
			key := useLiteCounter(t, knowledge.ID)
			newQueue := &multimodalGenerationEnqueuer{}
			newParse := &knowledgeService{task: newQueue, redisClient: rdb}
			oldQueue := &slotReleaseEnqueuer{failAt: map[int]bool{1: true}}
			oldQueue.onImageEnqueued = func() {
				require.NoError(t, newParse.enqueueImageMultimodalTasks(
					context.Background(), knowledge, kb, images, chunks, nil))
			}
			oldParse := &knowledgeService{task: oldQueue, redisClient: rdb}

			require.NoError(t, oldParse.enqueueImageMultimodalTasks(
				context.Background(), knowledge, kb, images, chunks, nil))

			if rdb == nil {
				pending, exists := liteCounter(knowledge.ID)
				require.True(t, exists)
				require.Equal(t, int64(2), pending)
			} else {
				require.Equal(t, "2", rdb.Get(context.Background(), key).Val())
			}
			require.Zero(t, oldQueue.postProcess)
		})
	}
}
