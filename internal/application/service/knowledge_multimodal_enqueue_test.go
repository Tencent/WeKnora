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

type failingTaskEnqueuer struct {
	err error
}

func (q failingTaskEnqueuer) Enqueue(*asynq.Task, ...asynq.Option) (*asynq.TaskInfo, error) {
	return nil, q.err
}

type recordingTaskEnqueuer struct {
	types []string
}

func (q *recordingTaskEnqueuer) Enqueue(task *asynq.Task, _ ...asynq.Option) (*asynq.TaskInfo, error) {
	q.types = append(q.types, task.Type())
	return &asynq.TaskInfo{ID: "task-1", Type: task.Type()}, nil
}

func TestEnqueueImageMultimodalTasksReturnsEnqueueFailure(t *testing.T) {
	ctx := context.Background()
	wantErr := errors.New("redis enqueue unavailable")
	svc := &knowledgeService{task: failingTaskEnqueuer{err: wantErr}}

	err := svc.enqueueImageMultimodalTasks(ctx,
		&types.Knowledge{ID: "knowledge-1", TenantID: 7},
		&types.KnowledgeBase{ID: "kb-1"},
		[]docparser.StoredImage{{ServingURL: "local://img-1.png"}},
		[]types.ParsedChunk{{Content: "![img](local://img-1.png)", ChunkID: "chunk-1"}},
		nil,
	)

	require.ErrorIs(t, err, wantErr)
}

func TestImageMultimodalFinalizeFallbacksWhenRedisPendingKeyMissing(t *testing.T) {
	ctx := context.Background()
	mr, err := miniredis.Run()
	require.NoError(t, err)
	t.Cleanup(mr.Close)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })

	queue := &recordingTaskEnqueuer{}
	svc := &ImageMultimodalService{redisClient: rdb, taskEnqueuer: queue}
	svc.checkAndFinalizeAllImages(ctx, types.ImageMultimodalPayload{
		TenantID:        7,
		KnowledgeID:     "knowledge-1",
		KnowledgeBaseID: "kb-1",
		Attempt:         3,
	})

	require.Equal(t, []string{types.TypeKnowledgePostProcess}, queue.types)
}
