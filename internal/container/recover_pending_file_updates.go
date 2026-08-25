package container

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/hibiken/asynq"
)

// recoverPendingFileUpdates recreates ephemeral coordinator triggers from the
// durable update slots after all Redis/Lite handlers have been registered.
func recoverPendingFileUpdates(repo interfaces.KnowledgeRepository, task interfaces.TaskEnqueuer) {
	if repo == nil || task == nil {
		return
	}
	ctx := context.Background()
	slots, err := repo.ListRecoverableKnowledgeFileUpdates(ctx, 1000)
	if err != nil {
		logger.Warnf(ctx, "[FileUpdateRecovery] list update slots failed: %v", err)
		return
	}
	recovered := 0
	for _, slot := range slots {
		if slot == nil || slot.ActiveVersion == nil || slot.ActiveState == types.KnowledgeFileUpdateStateFailed {
			continue
		}
		payload, err := json.Marshal(types.KnowledgeFileUpdateTaskPayload{
			TenantID:        slot.TenantID,
			KnowledgeBaseID: slot.KnowledgeBaseID,
			KnowledgeID:     slot.KnowledgeID,
			ActiveVersion:   *slot.ActiveVersion,
		})
		if err != nil {
			logger.Warnf(ctx, "[FileUpdateRecovery] encode trigger failed: knowledge_id=%s err=%v", slot.KnowledgeID, err)
			continue
		}
		_, err = task.Enqueue(
			asynq.NewTask(types.TypeKnowledgeFileUpdate, payload),
			asynq.Queue(types.QueueMaintenance),
			asynq.MaxRetry(3),
			asynq.Timeout(2*time.Hour),
			asynq.Unique(2*time.Hour),
		)
		if err != nil && !errors.Is(err, asynq.ErrTaskIDConflict) && !errors.Is(err, asynq.ErrDuplicateTask) {
			logger.Warnf(ctx, "[FileUpdateRecovery] enqueue trigger failed: knowledge_id=%s err=%v", slot.KnowledgeID, err)
			continue
		}
		recovered++
	}
	if recovered > 0 {
		logger.Infof(ctx, "[FileUpdateRecovery] recreated %d coordinator trigger(s)", recovered)
	}
}
