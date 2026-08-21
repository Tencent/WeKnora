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

const kbDeleteRecoveryInterval = time.Minute

type pendingKBDeleteIntent struct {
	ID       int64  `gorm:"column:id"`
	TenantID uint64 `gorm:"column:tenant_id"`
	ScopeID  string `gorm:"column:scope_id"`
	Payload  []byte `gorm:"column:payload"`
}

// recoverPendingKBDeleteTasks re-arms triggers for durable KB deletion
// intents. The intent is committed with the soft-delete, so a lost Redis
// enqueue or a process crash cannot strand the KB without a cleanup path.
// A deterministic task ID makes this safe to run from multiple replicas.
func recoverPendingKBDeleteTasks(
	db *gorm.DB,
	task interfaces.TaskEnqueuer,
	recovery interfaces.TaskRecoveryController,
) {
	if db == nil || task == nil || recovery == nil {
		return
	}
	ctx := context.Background()
	var intents []pendingKBDeleteIntent
	if err := db.WithContext(ctx).
		Model(&types.TaskPendingOp{}).
		Select("id, tenant_id, scope_id, payload").
		Where("task_type = ? AND scope = ? AND op = ?",
			types.TypeKBDelete, types.TaskScopeKnowledgeBaseDelete, types.TaskOpKnowledgeBaseDelete).
		Order("id ASC").
		Find(&intents).Error; err != nil {
		logger.Warnf(ctx, "[KBDeleteRecovery] failed to list durable intents: %v", err)
		return
	}

	seen := make(map[string]struct{}, len(intents))
	for _, intent := range intents {
		if intent.ScopeID == "" {
			continue
		}
		if _, ok := seen[intent.ScopeID]; ok {
			continue
		}
		seen[intent.ScopeID] = struct{}{}

		var payload types.KBDeletePayload
		if err := json.Unmarshal(intent.Payload, &payload); err != nil ||
			payload.TenantID == 0 || payload.TenantID != intent.TenantID ||
			payload.KnowledgeBaseID != intent.ScopeID {
			logger.Warnf(ctx, "[KBDeleteRecovery] invalid durable intent id=%d kb=%s", intent.ID, intent.ScopeID)
			continue
		}
		if _, err := service.EnqueueKBDeleteTask(task, intent.Payload, intent.ScopeID); err != nil {
			if errors.Is(err, asynq.ErrTaskIDConflict) || errors.Is(err, asynq.ErrDuplicateTask) {
				recoverArchivedKBDeleteTask(ctx, task, recovery, intent)
				continue
			}
			logger.Warnf(ctx, "[KBDeleteRecovery] enqueue trigger for KB %s failed: %v", intent.ScopeID, err)
			continue
		}
		logger.Infof(ctx, "[KBDeleteRecovery] re-armed cleanup trigger for KB %s", intent.ScopeID)
	}
}

// recoverArchivedKBDeleteTask replaces an exhausted trigger while keeping the
// durable outbox row intact. Pending, active, scheduled, and retrying tasks are
// left alone; their existing execution already owns the delete intent.
func recoverArchivedKBDeleteTask(
	ctx context.Context,
	task interfaces.TaskEnqueuer,
	recovery interfaces.TaskRecoveryController,
	intent pendingKBDeleteIntent,
) {
	taskID := service.KBDeleteTaskID(intent.ScopeID)
	info, err := recovery.GetTaskInfo(types.QueueMaintenance, taskID)
	if err != nil {
		if !errors.Is(err, asynq.ErrTaskNotFound) {
			logger.Warnf(ctx, "[KBDeleteRecovery] inspect trigger for KB %s failed: %v", intent.ScopeID, err)
		}
		return
	}
	if info.State != asynq.TaskStateArchived {
		return
	}
	if err := recovery.DeleteTask(types.QueueMaintenance, taskID); err != nil &&
		!errors.Is(err, asynq.ErrTaskNotFound) {
		logger.Warnf(ctx, "[KBDeleteRecovery] remove archived trigger for KB %s failed: %v", intent.ScopeID, err)
		return
	}
	if _, err := service.EnqueueKBDeleteTask(task, intent.Payload, intent.ScopeID); err != nil {
		if errors.Is(err, asynq.ErrTaskIDConflict) || errors.Is(err, asynq.ErrDuplicateTask) {
			return
		}
		logger.Warnf(ctx, "[KBDeleteRecovery] replace archived trigger for KB %s failed: %v", intent.ScopeID, err)
		return
	}
	logger.Infof(ctx, "[KBDeleteRecovery] replaced exhausted cleanup trigger for KB %s", intent.ScopeID)
}

// startKBDeleteRecovery performs startup recovery and a lightweight periodic
// pass in every deployment. Redis uses the inspector to replace archived
// tasks; Lite mode deduplicates active task IDs in SyncTaskExecutor.
func startKBDeleteRecovery(
	db *gorm.DB,
	task interfaces.TaskEnqueuer,
	recovery interfaces.TaskRecoveryController,
	cleaner interfaces.ResourceCleaner,
) {
	recoverPendingKBDeleteTasks(db, task, recovery)
	if db == nil || task == nil || recovery == nil || cleaner == nil {
		return
	}
	stop := make(chan struct{})
	go func() {
		ticker := time.NewTicker(kbDeleteRecoveryInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				recoverPendingKBDeleteTasks(db, task, recovery)
			case <-stop:
				return
			}
		}
	}()
	cleaner.RegisterWithName("KBDeleteRecovery", func() error {
		close(stop)
		return nil
	})
}
