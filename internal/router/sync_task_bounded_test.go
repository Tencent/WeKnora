package router

import (
	"context"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/hibiken/asynq"
	"sync/atomic"
	"testing"
	"time"
)

func TestLiteTaskExecutionBoundAndChildProgress(t *testing.T) {
	t.Setenv("WEKNORA_LITE_TASK_CONCURRENCY", "1")
	e := NewSyncTaskExecutor()
	entered := make(chan struct{}, 4)
	release := make(chan struct{})
	var active, max atomic.Int32
	e.RegisterHandler(types.TypeDocumentProcess, func(context.Context, *asynq.Task) error {
		n := active.Add(1)
		if n > max.Load() {
			max.Store(n)
		}
		entered <- struct{}{}
		<-release
		active.Add(-1)
		return nil
	})
	child := make(chan struct{}, 1)
	e.RegisterHandler(types.TypeSummaryGeneration, func(context.Context, *asynq.Task) error { child <- struct{}{}; return nil })
	for i := 0; i < 3; i++ {
		_, err := e.Enqueue(asynq.NewTask(types.TypeDocumentProcess, nil))
		if err != nil {
			t.Fatal(err)
		}
	}
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("no worker")
	}
	_, err := e.Enqueue(asynq.NewTask(types.TypeSummaryGeneration, nil))
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-child:
	case <-time.After(time.Second):
		t.Fatal("child blocked by parent pool")
	}
	select {
	case <-entered:
		t.Fatal("execution bound exceeded")
	case <-time.After(30 * time.Millisecond):
	}
	close(release)
	for i := 0; i < 2; i++ {
		select {
		case <-entered:
		case <-time.After(time.Second):
			t.Fatal("waiting task lost")
		}
	}
	if max.Load() != 1 {
		t.Fatal("active bound", max.Load())
	}
}
