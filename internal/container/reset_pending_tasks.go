package container

import (
	"context"
	"os"
	"time"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"gorm.io/gorm"
)

const resetPendingStaleWindow = 30 * time.Minute

const restartInterruptedMessage = "Task interrupted due to application restart"

// resetPendingTasks resets the state of any knowledge items or sync logs stuck in processing
// due to an unexpected application restart.
//
// In Lite mode (no REDIS_ADDR) normal queued tasks live in process memory, so
// a "processing" row at startup is orphaned unless it has a durable wiki op
// that can be re-triggered.
//
// Distributed mode is intentionally different: Asynq persists queued/retry
// tasks, and another replica may still be executing the same knowledge. A
// startup hook cannot safely distinguish an orphan from a backlogged task that
// has not opened a span yet. HousekeepingService owns that decision because it
// checks BOTH recent span activity and the real Asynq queue. Consequently this
// hook never resets knowledge/summary rows in distributed mode; it only keeps
// the separate sync-log cleanup below.
func resetPendingTasks(db *gorm.DB) {
	distributed := os.Getenv("REDIS_ADDR") != ""
	ctx := context.Background()
	spanRepo := repository.NewKnowledgeSpanRepository(db)

	var staleCutoff time.Time
	if distributed {
		staleCutoff = time.Now().Add(-resetPendingStaleWindow)
	}

	// Resolve Lite-mode orphaned knowledge rows first. A finalizing row whose
	// ONLY remaining slot is backed by a durable wiki op is excluded and resumed
	// by recoverPendingWikiTasks after handlers are registered. Rows with other
	// outstanding in-memory subtasks still fail: the wiki op cannot recover them.
	var stuckKnowledge []types.Knowledge
	if !distributed {
		if err := stuckKnowledgeParseQuery(db).
			Select("id").Find(&stuckKnowledge).Error; err != nil {
			logger.Warnf(ctx, "resetPendingTasks: list stuck knowledge failed: %v", err)
		}
	}

	// 1. Reset knowledge parsing tasks (including finalizing rows whose
	// enrichment subtasks were lost with the process).
	// Update by the resolved ids rather than reusing the GORM chain after
	// Find() (which makes PostgreSQL emit an invalid UPDATE ... FROM self).
	stuckIDs := knowledgeIDs(stuckKnowledge)
	var resetErr error
	var resetCount int64
	if len(stuckIDs) > 0 {
		// Rebuild the query instead of reusing the chain after Find(); reusing it
		// makes PostgreSQL emit an invalid UPDATE ... FROM self statement.
		result := stuckKnowledgeParseQuery(db).
			Where("id IN ?", stuckIDs).
			Updates(map[string]interface{}{
				"parse_status":           types.ParseStatusFailed,
				"error_message":          restartInterruptedMessage,
				"pending_subtasks_count": 0,
			})
		resetErr = result.Error
		resetCount = result.RowsAffected
	}
	if resetErr != nil {
		logger.Warnf(context.Background(), "Failed to reset pending knowledge tasks: %v", resetErr)
	} else if resetCount > 0 {
		logger.Infof(context.Background(),
			"Reset %d stuck knowledge parsing tasks to failed state (distributed=%v)",
			resetCount, distributed)

		// Cancel orphaned trace spans only after the owning knowledge rows are
		// terminal. This prevents the UI from showing duplicate running
		// postprocess.* subspans when a later manual retry opens fresh spans.
		// Re-read the successfully reset ids so a row whose status changed in
		// the small SELECT/UPDATE gap is not accidentally cancelled.
		var resetKnowledge []types.Knowledge
		if err := db.Select("id").
			Where("id IN ? AND parse_status = ? AND error_message = ?",
				stuckIDs, types.ParseStatusFailed, restartInterruptedMessage).
			Find(&resetKnowledge).Error; err != nil {
			logger.Warnf(ctx, "resetPendingTasks: list reset knowledge failed: %v", err)
		}
		for _, k := range resetKnowledge {
			attempt, err := spanRepo.LatestAttempt(ctx, k.ID)
			if err != nil || attempt <= 0 {
				continue
			}
			if n, err := spanRepo.CancelAllOpenSpans(ctx, k.ID, attempt,
				"SERVER_RESTART", restartInterruptedMessage); err != nil {
				logger.Warnf(ctx, "resetPendingTasks: cancel spans for %s failed: %v", k.ID, err)
			} else if n > 0 {
				logger.Infof(ctx, "resetPendingTasks: cancelled %d open span(s) for knowledge %s attempt %d",
					n, k.ID, attempt)
			}
		}
	}

	// 2. Lite summary tasks are process-local too. Distributed summary tasks
	// stay in Asynq and must not be failed merely because this replica started.
	if !distributed {
		resultSummary := stuckKnowledgeSummaryQuery(db).Updates(map[string]interface{}{
			"summary_status": types.SummaryStatusFailed,
		})
		if resultSummary.Error != nil {
			logger.Warnf(context.Background(), "Failed to reset pending summary tasks: %v", resultSummary.Error)
		} else if resultSummary.RowsAffected > 0 {
			logger.Infof(context.Background(),
				"Reset %d stuck summary generation tasks to failed state (distributed=false)",
				resultSummary.RowsAffected)
		}
	}

	// 3. Reset data source sync tasks
	now := time.Now()
	resultSync := stuckSyncLogQuery(db, distributed, staleCutoff).Updates(map[string]interface{}{
		"status":        types.SyncLogStatusFailed,
		"error_message": "Sync interrupted due to application restart",
		"finished_at":   &now,
	})
	if resultSync.Error != nil {
		logger.Warnf(context.Background(), "Failed to reset pending data source sync tasks: %v", resultSync.Error)
	} else if resultSync.RowsAffected > 0 {
		logger.Infof(context.Background(),
			"Reset %d stuck data source sync tasks to failed state (distributed=%v)",
			resultSync.RowsAffected, distributed)
	}
}

func stuckKnowledgeParseQuery(db *gorm.DB) *gorm.DB {
	q := db.Model(&types.Knowledge{}).
		Where("parse_status IN ?", resettableParseStatuses()).
		// Wiki ingest ops are persisted independently from the task trigger.
		// When wiki owns the only outstanding slot, keeping this finalizing row
		// alive lets startup recreate the trigger and finish cleanly. A count
		// above one means at least one non-wiki in-memory subtask was also lost.
		Where(`NOT (parse_status = ? AND pending_subtasks_count = 1 AND EXISTS (
			SELECT 1 FROM task_pending_ops
			WHERE task_pending_ops.task_type = ?
			  AND task_pending_ops.scope = ?
			  AND task_pending_ops.dedup_key = knowledges.id
			  AND task_pending_ops.op = ?
		))`, types.ParseStatusFinalizing, types.TypeWikiIngest,
			types.TaskScopeKnowledgeBase, "ingest")
	return q
}

func stuckKnowledgeSummaryQuery(db *gorm.DB) *gorm.DB {
	return db.Model(&types.Knowledge{}).
		Where("summary_status IN ?", []string{types.SummaryStatusPending, types.SummaryStatusProcessing})
}

func stuckSyncLogQuery(db *gorm.DB, distributed bool, staleCutoff time.Time) *gorm.DB {
	q := db.Model(&types.SyncLog{}).
		Where("status = ?", types.SyncLogStatusRunning)
	if distributed {
		q = q.Where("started_at < ?", staleCutoff)
	}
	return q
}

func resettableParseStatuses() []string {
	return []string{
		types.ParseStatusPending,
		types.ParseStatusProcessing,
		types.ParseStatusFinalizing,
		types.ParseStatusDeleting,
	}
}

func knowledgeIDs(rows []types.Knowledge) []string {
	ids := make([]string, 0, len(rows))
	for _, row := range rows {
		if row.ID != "" {
			ids = append(ids, row.ID)
		}
	}
	return ids
}
