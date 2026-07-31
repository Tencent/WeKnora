package router

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/hibiken/asynq"
)

func TestShouldStopSyncTaskRetryRecognizesWrappedSkipRetry(t *testing.T) {
	taskErr := errors.Join(errors.New("sync failed"), asynq.SkipRetry)

	if !shouldStopSyncTaskRetry(taskErr) {
		t.Fatal("wrapped asynq.SkipRetry must stop the Lite retry loop")
	}
	if shouldStopSyncTaskRetry(errors.New("transient")) {
		t.Fatal("ordinary errors must remain retryable")
	}
}

func TestSyncTaskExecutorInjectsRetryMetadata(t *testing.T) {
	executor := NewSyncTaskExecutor()
	type metadata struct {
		retryCount int
		maxRetry   int
		ok         bool
	}
	received := make(chan metadata, 1)
	executor.RegisterHandler("test:retry-metadata", func(ctx context.Context, _ *asynq.Task) error {
		retryCount, maxRetry, ok := types.TaskRetryMetadata(ctx)
		received <- metadata{retryCount: retryCount, maxRetry: maxRetry, ok: ok}
		return nil
	})

	_, err := executor.Enqueue(
		asynq.NewTask("test:retry-metadata", nil),
		asynq.MaxRetry(120),
	)
	if err != nil {
		t.Fatalf("Enqueue() error = %v", err)
	}

	select {
	case got := <-received:
		if !got.ok || got.retryCount != 0 || got.maxRetry != 120 {
			t.Fatalf("retry metadata = %+v, want retryCount=0 maxRetry=120 ok=true", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for Lite task execution")
	}
}

func TestSyncTaskExecutorHonorsTaskIDWhileTaskIsActive(t *testing.T) {
	executor := NewSyncTaskExecutor()
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	executor.RegisterHandler("test:task-id", func(context.Context, *asynq.Task) error {
		started <- struct{}{}
		<-release
		return nil
	})
	const taskID = "deterministic-task-id"

	info, err := executor.Enqueue(
		asynq.NewTask("test:task-id", nil),
		asynq.TaskID(taskID),
	)
	if err != nil {
		t.Fatalf("first Enqueue() error = %v", err)
	}
	if info.ID != taskID {
		t.Fatalf("TaskInfo.ID = %q, want %q", info.ID, taskID)
	}

	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for active task")
	}

	_, err = executor.Enqueue(
		asynq.NewTask("test:task-id", nil),
		asynq.TaskID(taskID),
	)
	if !errors.Is(err, asynq.ErrTaskIDConflict) {
		t.Fatalf("duplicate Enqueue() error = %v, want ErrTaskIDConflict", err)
	}
	close(release)
}
