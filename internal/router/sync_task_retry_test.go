package router

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/hibiken/asynq"
)

func TestSyncTaskExecutorInjectsRetryMetadata(t *testing.T) {
	executor := NewSyncTaskExecutor()
	observed := make(chan [2]int, 1)
	executor.RegisterHandler("test:retry-metadata", func(ctx context.Context, _ *asynq.Task) error {
		retried, maxRetry, ok := types.TaskRetryMetadataFromContext(ctx)
		if !ok {
			observed <- [2]int{-1, -1}
			return nil
		}
		observed <- [2]int{retried, maxRetry}
		return nil
	})

	task := asynq.NewTask("test:retry-metadata", nil)
	if _, err := executor.Enqueue(task, asynq.MaxRetry(3)); err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	select {
	case got := <-observed:
		if got != [2]int{0, 3} {
			t.Fatalf("retry metadata = %v, want [0 3]", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for sync task")
	}
}

func TestSyncTaskExecutorDeduplicatesActiveTaskID(t *testing.T) {
	executor := NewSyncTaskExecutor()
	started := make(chan struct{}, 2)
	release := make(chan struct{})
	executor.RegisterHandler("test:dedup", func(context.Context, *asynq.Task) error {
		started <- struct{}{}
		<-release
		return nil
	})

	task := asynq.NewTask("test:dedup", nil)
	opts := []asynq.Option{
		asynq.Queue(types.QueueMaintenance),
		asynq.TaskID("durable-delete-1"),
		asynq.MaxRetry(0),
	}
	info, err := executor.Enqueue(task, opts...)
	if err != nil {
		t.Fatalf("first enqueue: %v", err)
	}
	if info.ID != "durable-delete-1" || info.Queue != types.QueueMaintenance {
		t.Fatalf("task info = %#v", info)
	}

	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for first task")
	}
	if _, err := executor.Enqueue(task, opts...); !errors.Is(err, asynq.ErrTaskIDConflict) {
		t.Fatalf("second enqueue error = %v, want task ID conflict", err)
	}
	active, err := executor.GetTaskInfo(types.QueueMaintenance, "durable-delete-1")
	if err != nil || active.State != asynq.TaskStateActive {
		t.Fatalf("active task info = %#v, err=%v", active, err)
	}

	close(release)
	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, err := executor.Enqueue(task, opts...); err == nil {
			break
		} else if !errors.Is(err, asynq.ErrTaskIDConflict) {
			t.Fatalf("re-enqueue after completion: %v", err)
		}
		if time.Now().After(deadline) {
			t.Fatal("task ID was not released after completion")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
