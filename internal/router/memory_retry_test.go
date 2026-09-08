package router

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/hibiken/asynq"
	"github.com/stretchr/testify/require"
)

func TestMemoryLeaseRetryWaitsForExpiry(t *testing.T) {
	err := fmt.Errorf("extract: %w", &types.MemoryExtractionLeaseError{RetryAt: time.Now().Add(10 * time.Minute)})
	delay := asynqRetryDelayFunc(1, err, asynq.NewTask(types.TypeMemoryExtract, nil))
	require.GreaterOrEqual(t, delay, 10*time.Minute)
	require.Less(t, delay, 10*time.Minute+2*time.Second)
	expired := &types.MemoryExtractionLeaseError{RetryAt: time.Now().Add(-time.Minute)}
	require.Equal(t, time.Second, asynqRetryDelayFunc(1, expired, asynq.NewTask(types.TypeMemoryExtract, nil)))
}

func TestLiteMemoryLeaseRetryDoesNotExhaustBeforeExpiry(t *testing.T) {
	executor := NewSyncTaskExecutor()
	done := make(chan int, 1)
	executor.RegisterHandler(types.TypeMemoryExtract, func(ctx context.Context, _ *asynq.Task) error {
		attempt, _, _ := types.TaskRetryMetadataFromContext(ctx)
		if attempt == 0 {
			return &types.MemoryExtractionLeaseError{RetryAt: time.Now().Add(-time.Second)}
		}
		done <- attempt
		return nil
	})
	_, err := executor.Enqueue(asynq.NewTask(types.TypeMemoryExtract, nil), asynq.MaxRetry(1))
	require.NoError(t, err)
	select {
	case attempt := <-done:
		require.Equal(t, 1, attempt)
	case <-time.After(3 * time.Second):
		t.Fatal("Lite retry ignored the lease delay")
	}
}
