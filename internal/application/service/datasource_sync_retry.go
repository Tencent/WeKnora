package service

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"math/rand/v2"
	"strconv"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/hibiken/asynq"
)

const (
	dataSourceSyncDeferDelay        = 15 * time.Second
	dataSourceSyncCleanupTimeout    = 5 * time.Second
	dataSourceSyncRetryOffsetHeader = "weknora.datasource.retry-offset"
)

type dataSourceSyncAttemptKey struct{}

func dataSourceSyncRetryCounts(ctx context.Context) (retried, maxRetry int, ok bool) {
	retried, retryOK := asynq.GetRetryCount(ctx)
	maxRetry, maxOK := asynq.GetMaxRetry(ctx)
	if retryOK && maxOK {
		return retried, maxRetry, true
	}
	return types.TaskRetryMetadataFromContext(ctx)
}

func withDataSourceSyncAttempt(ctx context.Context, task *asynq.Task) context.Context {
	offset, _ := strconv.Atoi(task.Headers()[dataSourceSyncRetryOffsetHeader])
	retried, _, _ := dataSourceSyncRetryCounts(ctx)
	return context.WithValue(ctx, dataSourceSyncAttemptKey{}, offset+retried)
}

func dataSourceSyncAttempt(ctx context.Context) int {
	if attempt, ok := ctx.Value(dataSourceSyncAttemptKey{}).(int); ok {
		return attempt
	}
	retried, _, _ := dataSourceSyncRetryCounts(ctx)
	return retried
}

// A durable delayed successor frees the current worker without spending the
// failure budget, including contention on the last allowed attempt. Headers
// preserve the attempt offset so a retried full sync resumes its checkpoint.
// Returning success is safe only after the successor is enqueued successfully.
func (s *DataSourceService) deferContendedSync(ctx context.Context, task *asynq.Task) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.taskEnqueuer == nil {
		return errors.New("cannot defer data source sync: task enqueuer unavailable")
	}
	retried, maxRetry, ok := dataSourceSyncRetryCounts(ctx)
	if !ok {
		return errors.New("cannot defer data source sync: retry metadata unavailable")
	}
	headers := maps.Clone(task.Headers())
	if headers == nil {
		headers = make(map[string]string)
	}
	headers[dataSourceSyncRetryOffsetHeader] = strconv.Itoa(dataSourceSyncAttempt(ctx))
	successor := asynq.NewTaskWithHeaders(task.Type(), task.Payload(), headers)
	// Spread contenders across scheduler ticks instead of repeatedly waking
	// all of them together and allowing only one to acquire each round.
	delay := dataSourceSyncDeferDelay + time.Duration(rand.Int64N(int64(dataSourceSyncDeferDelay)))
	_, err := s.taskEnqueuer.Enqueue(successor, asynq.Queue(types.QueueSync),
		asynq.MaxRetry(max(0, maxRetry-retried)), asynq.Timeout(2*time.Hour), asynq.ProcessIn(delay))
	if err != nil {
		return fmt.Errorf("defer data source sync: %w", err)
	}
	logger.Infof(ctx, "data source sync deferred after lock contention (remaining retries=%d)",
		max(0, maxRetry-retried))
	return nil
}

// Cleanup owns only this run's log. It must never publish a cursor or change
// shared source state after cancellation or loss of the execution lease.
func (s *DataSourceService) finishTerminalSyncLog(
	ctx context.Context, payload types.DataSourceSyncPayload, taskErr error,
) {
	if taskErr == nil {
		return
	}
	retried, maxRetry, ok := dataSourceSyncRetryCounts(ctx)
	if ok && retried < maxRetry && !errors.Is(taskErr, asynq.SkipRetry) {
		return
	}
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), dataSourceSyncCleanupTimeout)
	defer cancel()
	log, err := s.syncLogRepo.FindByID(cleanupCtx, payload.SyncLogID)
	if err != nil || log == nil || log.DataSourceID != payload.DataSourceID ||
		log.Status != types.SyncLogStatusRunning {
		return
	}
	log.Status = types.SyncLogStatusFailed
	log.FinishedAt = timePtr(time.Now())
	log.ErrorMessage = "Sync task failed before completion; see server logs"
	if err := s.syncLogRepo.UpdateResult(cleanupCtx, log); err != nil {
		logger.Errorf(cleanupCtx, "failed to finalize sync log %s: %v", log.ID, err)
	}
}
