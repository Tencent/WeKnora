package container

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/Tencent/WeKnora/internal/application/service"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/hibiken/asynq"
	"gorm.io/gorm"
)

// pendingWikiScope is the minimum routing information needed to recreate an
// ephemeral wiki trigger from the durable task_pending_ops queue.
type pendingWikiScope struct {
	TenantID uint64 `gorm:"column:tenant_id"`
	TaskType string `gorm:"column:task_type"`
	ScopeID  string `gorm:"column:scope_id"`
}

// recoverPendingWikiTasks recreates one trigger per pending wiki queue lane.
//
// task_pending_ops is deliberately durable, but the trigger that wakes its
// consumer is not durable in Lite mode (SyncTaskExecutor is process-local) and
// may also be absent after an interrupted Redis enqueue. Running this after all
// handlers are registered closes that gap. Duplicate triggers are harmless:
// ingest claims/peeks disjoint rows and finalize coalesces its pending lane.
func recoverPendingWikiTasks(db *gorm.DB, task interfaces.TaskEnqueuer) {
	if db == nil || task == nil {
		return
	}
	ctx := context.Background()
	var scopes []pendingWikiScope
	if err := db.WithContext(ctx).
		Model(&types.TaskPendingOp{}).
		Distinct("tenant_id", "task_type", "scope_id").
		Where("scope = ? AND task_type IN ?", types.TaskScopeKnowledgeBase,
			[]string{types.TypeWikiIngest, types.TypeWikiFinalize}).
		Find(&scopes).Error; err != nil {
		logger.Warnf(ctx, "[WikiRecovery] failed to list pending queues: %v", err)
		return
	}

	recovered := 0
	for _, scope := range scopes {
		if scope.ScopeID == "" {
			continue
		}
		payload, err := json.Marshal(service.WikiIngestPayload{
			TenantID:        scope.TenantID,
			KnowledgeBaseID: scope.ScopeID,
		})
		if err != nil {
			logger.Warnf(ctx, "[WikiRecovery] marshal trigger for KB %s failed: %v", scope.ScopeID, err)
			continue
		}
		opts := []asynq.Option{
			asynq.Queue(types.QueueWiki),
			asynq.MaxRetry(10), // keep aligned with the wiki ingest retry policy
			asynq.Timeout(60 * time.Minute),
		}
		if scope.TaskType == types.TypeWikiFinalize {
			opts[2] = asynq.Timeout(30 * time.Minute)
			// Match scheduleFinalize so simultaneous replica startups collapse
			// into the same per-KB finalize trigger.
			opts = append(opts, asynq.TaskID("wiki-finalize-"+scope.ScopeID))
		}
		trigger := asynq.NewTask(scope.TaskType, payload, opts...)
		if _, err := task.Enqueue(trigger); err != nil {
			if errors.Is(err, asynq.ErrTaskIDConflict) || errors.Is(err, asynq.ErrDuplicateTask) {
				recovered++
				continue
			}
			logger.Warnf(ctx, "[WikiRecovery] enqueue %s trigger for KB %s failed: %v",
				scope.TaskType, scope.ScopeID, err)
			continue
		}
		recovered++
	}
	if recovered > 0 {
		logger.Infof(ctx, "[WikiRecovery] recreated %d trigger(s) from durable pending queues", recovered)
	}
}
