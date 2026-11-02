package container

import (
	"context"

	"github.com/Tencent/WeKnora/internal/application/service"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"gorm.io/gorm"
)

type pendingDocumentScope struct {
	TenantID uint64 `gorm:"column:tenant_id"`
	ScopeID  string `gorm:"column:scope_id"`
}

// recoverPendingDocumentTasks recreates one dispatcher trigger per durable
// document lane. The document payload is stored in task_pending_ops, while the
// Asynq/Lite trigger is intentionally ephemeral and can be lost on restart.
func recoverPendingDocumentTasks(db *gorm.DB, task interfaces.TaskEnqueuer) {
	if db == nil || task == nil {
		return
	}
	ctx := context.Background()

	const activeKnowledgeBase = `(EXISTS (
		SELECT 1 FROM knowledge_bases kb
		WHERE kb.id = task_pending_ops.scope_id
			AND kb.tenant_id = task_pending_ops.tenant_id
			AND kb.deleted_at IS NULL
	) AND EXISTS (
		SELECT 1 FROM tenants t
		WHERE t.id = task_pending_ops.tenant_id
			AND t.deleted_at IS NULL
	))`

	cleanup := db.WithContext(ctx).
		Where("scope = ? AND task_type = ?", types.TaskScopeKnowledgeBase, types.TypeDocumentProcess).
		Where("NOT " + activeKnowledgeBase).
		Delete(&types.TaskPendingOp{})
	if cleanup.Error != nil {
		logger.Warnf(ctx, "[DocumentRecovery] failed to clear deleted KB queues: %v", cleanup.Error)
		return
	}

	var scopes []pendingDocumentScope
	if err := db.WithContext(ctx).
		Model(&types.TaskPendingOp{}).
		Distinct("tenant_id", "scope_id").
		Where("scope = ? AND task_type = ?", types.TaskScopeKnowledgeBase, types.TypeDocumentProcess).
		Where(activeKnowledgeBase).
		Find(&scopes).Error; err != nil {
		logger.Warnf(ctx, "[DocumentRecovery] failed to list pending queues: %v", err)
		return
	}

	recovered := 0
	for _, scope := range scopes {
		if scope.TenantID == 0 || scope.ScopeID == "" {
			continue
		}
		if _, err := service.EnqueueDocumentDispatchTrigger(ctx, task, scope.TenantID, scope.ScopeID); err != nil {
			logger.Warnf(ctx, "[DocumentRecovery] enqueue trigger for KB %s failed: %v", scope.ScopeID, err)
			continue
		}
		recovered++
	}
	if recovered > 0 {
		logger.Infof(ctx, "[DocumentRecovery] recreated %d document dispatcher trigger(s)", recovered)
	}
}
