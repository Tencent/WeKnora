package router

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hibiken/asynq"
	"github.com/stretchr/testify/require"
)

func TestAsynqTaskOptionsReadsNewTaskOptions(t *testing.T) {
	require.Nil(t, asynqTaskOptions(nil))
	require.Empty(t, asynqTaskOptions(asynq.NewTask("test:no-opts", nil)))

	task := asynq.NewTask("test:opts", nil, asynq.MaxRetry(3), asynq.Timeout(time.Minute))
	opts := asynqTaskOptions(task)
	require.Len(t, opts, 2)
	require.Equal(t, asynq.MaxRetryOpt, opts[0].Type())
	require.Equal(t, 3, opts[0].Value())
	require.Equal(t, asynq.TimeoutOpt, opts[1].Type())
	require.Equal(t, time.Minute, opts[1].Value())
}

func TestResolveSyncTaskOptions(t *testing.T) {
	t.Run("defaults", func(t *testing.T) {
		got := resolveSyncTaskOptions(asynq.NewTask("test:defaults", nil), nil)
		require.Equal(t, 25, got.maxRetry)
		require.Zero(t, got.timeout)
		require.True(t, got.deadline.IsZero())
		require.Zero(t, got.delay)
	})

	t.Run("NewTask options honoured", func(t *testing.T) {
		deadline := time.Now().Add(time.Hour)
		task := asynq.NewTask("test:newtask", nil,
			asynq.MaxRetry(2), asynq.Timeout(90*time.Second), asynq.Deadline(deadline), asynq.ProcessIn(time.Second))
		got := resolveSyncTaskOptions(task, nil)
		require.Equal(t, 2, got.maxRetry)
		require.Equal(t, 90*time.Second, got.timeout)
		require.True(t, got.deadline.Equal(deadline))
		require.Equal(t, time.Second, got.delay)
	})

	t.Run("Enqueue options override NewTask", func(t *testing.T) {
		task := asynq.NewTask("test:override", nil, asynq.MaxRetry(5), asynq.Timeout(time.Minute))
		got := resolveSyncTaskOptions(task, []asynq.Option{asynq.MaxRetry(1), asynq.Timeout(time.Hour)})
		require.Equal(t, 1, got.maxRetry)
		require.Equal(t, time.Hour, got.timeout)
	})

	t.Run("explicit zero and negative MaxRetry", func(t *testing.T) {
		require.Equal(t, 0, resolveSyncTaskOptions(asynq.NewTask("t", nil, asynq.MaxRetry(0)), nil).maxRetry)
		negative := resolveSyncTaskOptions(asynq.NewTask("t", nil), []asynq.Option{asynq.MaxRetry(-3)})
		require.Equal(t, 0, negative.maxRetry)
	})

	t.Run("ProcessAt converts to a delay", func(t *testing.T) {
		at := asynq.ProcessAt(time.Now().Add(time.Hour))
		got := resolveSyncTaskOptions(asynq.NewTask("t", nil), []asynq.Option{at})
		require.Greater(t, got.delay, 59*time.Minute)
		require.LessOrEqual(t, got.delay, time.Hour)
	})

	t.Run("does not mutate NewTask options", func(t *testing.T) {
		task := asynq.NewTask("t", nil, asynq.MaxRetry(4))
		_ = resolveSyncTaskOptions(task, []asynq.Option{asynq.MaxRetry(1)})
		require.Len(t, asynqTaskOptions(task), 1)
		require.Equal(t, 4, resolveSyncTaskOptions(task, nil).maxRetry)
	})
}

type deadlineObservation struct {
	deadline time.Time
	ok       bool
}

func observeHandlerDeadline(t *testing.T, task *asynq.Task, opts ...asynq.Option) deadlineObservation {
	t.Helper()
	executor := NewSyncTaskExecutor()
	observed := make(chan deadlineObservation, 1)
	executor.RegisterHandler(task.Type(), func(ctx context.Context, _ *asynq.Task) error {
		d, ok := ctx.Deadline()
		observed <- deadlineObservation{deadline: d, ok: ok}
		return nil
	})
	_, err := executor.Enqueue(task, opts...)
	require.NoError(t, err)
	select {
	case got := <-observed:
		return got
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for sync task")
		return deadlineObservation{}
	}
}

func TestSyncTaskExecutorAppliesNewTaskTimeout(t *testing.T) {
	start := time.Now()
	got := observeHandlerDeadline(t, asynq.NewTask("test:timeout", nil, asynq.Timeout(time.Minute)))
	require.True(t, got.ok, "handler context should carry a deadline")
	require.WithinDuration(t, start.Add(time.Minute), got.deadline, 5*time.Second)
}

func TestSyncTaskExecutorAppliesEarlierDeadline(t *testing.T) {
	deadline := time.Now().Add(30 * time.Second)
	got := observeHandlerDeadline(t,
		asynq.NewTask("test:deadline", nil, asynq.Timeout(time.Hour)), asynq.Deadline(deadline))
	require.True(t, got.ok)
	require.True(t, got.deadline.Equal(deadline), "deadline = %v, want %v", got.deadline, deadline)
}

func TestSyncTaskExecutorNoDeadlineWithoutTimeout(t *testing.T) {
	got := observeHandlerDeadline(t, asynq.NewTask("test:no-timeout", nil))
	require.False(t, got.ok, "handler context should not carry a deadline, got %v", got.deadline)
}

type finalFailureCall struct {
	task *asynq.Task
	err  error
	// Context state sampled inside the hook; the executor cancels the
	// hook's context once it returns.
	ctxErr      error
	hasDeadline bool
}

func newFailingExecutor(taskType string, handlerErr error) (*SyncTaskExecutor, *atomic.Int32, chan finalFailureCall) {
	executor := NewSyncTaskExecutor()
	calls := &atomic.Int32{}
	executor.RegisterHandler(taskType, func(context.Context, *asynq.Task) error {
		calls.Add(1)
		return handlerErr
	})
	hook := make(chan finalFailureCall, 4)
	executor.SetFinalFailureHook(func(ctx context.Context, task *asynq.Task, err error) {
		_, hasDeadline := ctx.Deadline()
		hook <- finalFailureCall{task: task, err: err, ctxErr: ctx.Err(), hasDeadline: hasDeadline}
	})
	return executor, calls, hook
}

func waitFinalFailure(t *testing.T, hook chan finalFailureCall) finalFailureCall {
	t.Helper()
	select {
	case got := <-hook:
		return got
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for final-failure hook")
		return finalFailureCall{}
	}
}

func TestSyncTaskExecutorNewTaskMaxRetryZeroRunsOnce(t *testing.T) {
	boom := errors.New("boom")
	executor, calls, hook := newFailingExecutor("test:max-retry-zero", boom)
	task := asynq.NewTask("test:max-retry-zero", []byte("p"), asynq.MaxRetry(0))
	_, err := executor.Enqueue(task)
	require.NoError(t, err)

	got := waitFinalFailure(t, hook)
	require.Equal(t, int32(1), calls.Load())
	require.Same(t, task, got.task)
	require.ErrorIs(t, got.err, boom)
	// The hook gets its own bounded context, not a cancelled attempt ctx.
	require.NoError(t, got.ctxErr)
	require.True(t, got.hasDeadline)

	select {
	case extra := <-hook:
		t.Fatalf("final-failure hook called twice: %v", extra.err)
	case <-time.After(100 * time.Millisecond):
	}
}

func TestSyncTaskExecutorSkipRetryStopsImmediately(t *testing.T) {
	handlerErr := fmt.Errorf("bad payload: %w", asynq.SkipRetry)
	executor, calls, hook := newFailingExecutor("test:skip-retry", handlerErr)
	// Default MaxRetry (25) would otherwise back off for 5s before attempt 2.
	_, err := executor.Enqueue(asynq.NewTask("test:skip-retry", nil))
	require.NoError(t, err)

	got := waitFinalFailure(t, hook)
	require.Equal(t, int32(1), calls.Load())
	require.ErrorIs(t, got.err, asynq.SkipRetry)
	require.EqualError(t, got.err, handlerErr.Error())
}

func TestSyncTaskExecutorFinalFailureHookNotCalledOnSuccess(t *testing.T) {
	executor, calls, hook := newFailingExecutor("test:success", nil)
	done := make(chan struct{})
	executor.RegisterHandler("test:success", func(context.Context, *asynq.Task) error {
		calls.Add(1)
		close(done)
		return nil
	})
	_, err := executor.Enqueue(asynq.NewTask("test:success", nil, asynq.MaxRetry(0)))
	require.NoError(t, err)

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for sync task")
	}
	select {
	case got := <-hook:
		t.Fatalf("final-failure hook called on success: %v", got.err)
	case <-time.After(200 * time.Millisecond):
	}
	require.Equal(t, int32(1), calls.Load())
}

func TestSyncTaskExecutorRecoversPanickingFinalFailureHook(t *testing.T) {
	executor := NewSyncTaskExecutor()
	executor.RegisterHandler("test:hook-panic", func(context.Context, *asynq.Task) error {
		return errors.New("boom")
	})
	hookEntered := make(chan struct{}, 2)
	executor.SetFinalFailureHook(func(context.Context, *asynq.Task, error) {
		hookEntered <- struct{}{}
		panic("hook exploded")
	})
	for i := 0; i < 2; i++ {
		_, err := executor.Enqueue(asynq.NewTask("test:hook-panic", nil, asynq.MaxRetry(0)))
		require.NoError(t, err)
		select {
		case <-hookEntered:
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for final-failure hook")
		}
	}
	// An unrecovered panic in the executor goroutine would kill the test
	// binary; give it a moment to surface before declaring success.
	time.Sleep(100 * time.Millisecond)
}
