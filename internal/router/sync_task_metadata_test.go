package router

import (
	"context"
	"testing"

	"github.com/hibiken/asynq"
	"github.com/stretchr/testify/require"
)

func TestSyncTaskExecutorRejectsDuplicateActiveTaskID(t *testing.T) {
	executor := NewSyncTaskExecutor()
	started := make(chan struct{})
	release := make(chan struct{})
	executor.RegisterHandler("metadata:test", func(context.Context, *asynq.Task) error {
		close(started)
		<-release
		return nil
	})

	task := asynq.NewTask("metadata:test", nil)
	info, err := executor.Enqueue(task, asynq.TaskID("metadata-task-1"), asynq.MaxRetry(0))
	require.NoError(t, err)
	require.Equal(t, "metadata-task-1", info.ID)
	<-started

	_, err = executor.Enqueue(task, asynq.TaskID("metadata-task-1"), asynq.MaxRetry(0))
	require.ErrorIs(t, err, asynq.ErrTaskIDConflict)
	close(release)
}
