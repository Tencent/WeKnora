package service

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/tracing/langfuse"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/redis/go-redis/v9"
)

const (
	wikiFollowUpPrefix = "wiki:followup:"
	// This is a reconciliation interval, not a queue lease. A task can wait
	// arbitrarily long for a worker without losing its reserved capacity.
	// Missing tasks are retained for this interval to allow an enqueue in
	// progress to finish, including when its caller times out.
	wikiFollowUpReconcileAfter = 2 * time.Minute
)

// Reservations cover the entire asynq task lifetime, including delayed tasks
// and retries. Keep one spare beyond the running cap so the last completing
// task can enqueue its successor before its own reservation is released.
// Atomic ZCARD + ZADD makes concurrent completions share the same budget.
const wikiFollowUpReserveScript = `
local available = math.max(0, tonumber(ARGV[2]) - redis.call('ZCARD', KEYS[1]))
local n = math.min(tonumber(ARGV[1]), available)
local ids = {}
for i = 1, n do
  local id = ARGV[3] .. ':' .. i
  redis.call('ZADD', KEYS[1], ARGV[4], id)
  ids[#ids + 1] = id
end
return ids
`

// Do not expire a reservation just because the task has waited a long time.
// Inspect asynq first, then remove/refresh only if another reconciler hasn't
// already updated it. ZADD XX also avoids resurrecting a completed task.
const wikiFollowUpReconcileScript = `
local score = redis.call('ZSCORE', KEYS[1], ARGV[1])
if score and tonumber(score) == tonumber(ARGV[2]) then
  if ARGV[3] == 'remove' then
    redis.call('ZREM', KEYS[1], ARGV[1])
  else
    redis.call('ZADD', KEYS[1], 'XX', ARGV[3], ARGV[1])
  end
end
return 1
`

func (s *wikiIngestService) reserveFollowUpTasks(
	ctx context.Context, kbID string, requested, maxInflight int,
) ([]string, error) {
	if requested <= 0 {
		return nil, nil
	}
	if err := s.reconcileFollowUpTasks(ctx, kbID); err != nil {
		return nil, err
	}
	return s.redisClient.Eval(ctx, wikiFollowUpReserveScript,
		[]string{wikiFollowUpPrefix + kbID}, requested, max(1, maxInflight)+1,
		"wiki-followup-"+uuid.NewString(), time.Now().Add(wikiFollowUpReconcileAfter).UnixMilli(),
	).StringSlice()
}

func (s *wikiIngestService) reconcileFollowUpTasks(ctx context.Context, kbID string) error {
	key := wikiFollowUpPrefix + kbID
	now := time.Now()
	entries, err := s.redisClient.ZRangeByScoreWithScores(ctx, key, &redis.ZRangeBy{
		Min: "-inf", Max: strconv.FormatInt(now.UnixMilli(), 10),
	}).Result()
	if err != nil || len(entries) == 0 {
		return err
	}
	// Shares the service's Redis connection; Inspector.Close must not close it.
	inspector := asynq.NewInspectorFromRedisClient(s.redisClient)
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		id, ok := entry.Member.(string)
		if !ok {
			continue
		}
		info, err := inspector.GetTaskInfo(types.QueueWiki, id)
		missing := errors.Is(err, asynq.ErrTaskNotFound) || errors.Is(err, asynq.ErrQueueNotFound)
		if err != nil && !missing {
			return err
		}
		action := strconv.FormatInt(now.Add(wikiFollowUpReconcileAfter).UnixMilli(), 10)
		if missing || info.State == asynq.TaskStateArchived || info.State == asynq.TaskStateCompleted {
			action = "remove"
		}
		if err := s.redisClient.Eval(ctx, wikiFollowUpReconcileScript,
			[]string{key}, id, entry.Score, action).Err(); err != nil {
			return err
		}
	}
	return nil
}

func (s *wikiIngestService) releaseFollowUpTask(ctx context.Context, kbID string) {
	if s.redisClient == nil {
		return
	}
	id, ok := asynq.GetTaskID(ctx)
	if !ok {
		return
	}
	cleanupCtx, cancel := wikiIngestCleanupContext(ctx)
	defer cancel()
	if err := s.redisClient.ZRem(cleanupCtx, wikiFollowUpPrefix+kbID, id).Err(); err != nil {
		logger.Warnf(ctx, "wiki ingest: follow-up reservation release failed: %v", err)
	}
}

// A coalesced probe keeps progress possible when all capacity is reserved but
// an enqueue failed or a worker died. It also revisits reservations whose
// cleanup failed. It is outside the reservation budget and contributes at
// most two probes per KB, independent of the backlog size.
func (s *wikiIngestService) scheduleFollowUpRecheck(ctx context.Context, payload WikiIngestPayload) bool {
	langfuse.InjectTracing(ctx, &payload)
	b, _ := json.Marshal(payload)
	id := "wiki-followup-recheck-" + payload.KnowledgeBaseID
	if current, _ := asynq.GetTaskID(ctx); current == id {
		// The current task ID remains occupied until asynq acknowledges it.
		// Alternate between two IDs so the probe can re-arm itself.
		id += "-next"
	}
	t := asynq.NewTask(types.TypeWikiIngest, b,
		asynq.Queue(types.QueueWiki),
		asynq.MaxRetry(wikiIngestMaxRetry),
		asynq.Timeout(60*time.Minute),
		asynq.ProcessIn(wikiFollowUpReconcileAfter+wikiFollowUpDelay),
		asynq.TaskID(id),
	)
	if _, err := s.task.Enqueue(t); err != nil {
		if errors.Is(err, asynq.ErrTaskIDConflict) || errors.Is(err, asynq.ErrDuplicateTask) {
			return true
		}
		logger.Warnf(ctx, "wiki ingest: follow-up recheck enqueue failed: %v", err)
		return false
	}
	return true
}
