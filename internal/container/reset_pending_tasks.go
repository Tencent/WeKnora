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

// resetPendingTasks resets the state of any knowledge items or sync logs stuck in processing
// due to an unexpected application restart.
//
// In Lite mode (no REDIS_ADDR) every queued task lived in process memory, so
// any "processing" row at startup is definitively orphaned and must be marked
// failed wholesale. In distributed mode (Asynq on Redis) the active queue
// survives restart, but the *currently executing* task on the dead instance
// is lost — Asynq won't reschedule it until at-least-once retry kicks in,
// which can take minutes or never (e.g. if the deadline has passed). To bound
// the worst case we mark only "long-stale" rows failed: anything that hasn't
// been touched for 30 minutes is well past any reasonable in-flight window.
// Newer rows are left alone so we don't race a peer instance that's mid-process.
func resetPendingTasks(db *gorm.DB) {
	distributed := os.Getenv("REDIS_ADDR") != ""
	ctx := context.Background()
	spanRepo := repository.NewKnowledgeSpanRepository(db)

	var staleCutoff time.Time
	if distributed {
		staleCutoff = time.Now().Add(-resetPendingStaleWindow)
	}

	// Cancel orphaned trace spans for knowledge rows we are about to mark
	// failed. resetPendingTasks does not touch asynq queues; this only
	// prevents the UI from showing duplicate running postprocess.*
	// subspans when a later retry also opens fresh spans.
	var stuckKnowledge []types.Knowledge
	if err := stuckKnowledgeParseQuery(db, distributed, staleCutoff).
		Select("id").Find(&stuckKnowledge).Error; err != nil {
		logger.Warnf(ctx, "resetPendingTasks: list stuck knowledge failed: %v", err)
	} else {
		for _, k := range stuckKnowledge {
			attempt, err := spanRepo.LatestAttempt(ctx, k.ID)
			if err != nil || attempt <= 0 {
				continue
			}
			if n, err := spanRepo.CancelAllOpenSpans(ctx, k.ID, attempt,
				"SERVER_RESTART", "task interrupted due to application restart"); err != nil {
				logger.Warnf(ctx, "resetPendingTasks: cancel spans for %s failed: %v", k.ID, err)
			} else if n > 0 {
				logger.Infof(ctx, "resetPendingTasks: cancelled %d open span(s) for knowledge %s attempt %d",
					n, k.ID, attempt)
			}
		}
	}

	// 1. Reset knowledge parsing tasks (including finalizing rows whose
	// enrichment subtasks were lost with the process).
	// Fresh query — reusing the *gorm.DB chain after Find() makes GORM emit
	// UPDATE ... FROM knowledges which PostgreSQL rejects (SQLSTATE 42712).
	result := stuckKnowledgeParseQuery(db, distributed, staleCutoff).Updates(map[string]interface{}{
		"parse_status":           types.ParseStatusFailed,
		"error_message":          "Task interrupted due to application restart",
		"pending_subtasks_count": 0,
	})
	if result.Error != nil {
		logger.Warnf(context.Background(), "Failed to reset pending knowledge tasks: %v", result.Error)
	} else if result.RowsAffected > 0 {
		logger.Infof(context.Background(),
			"Reset %d stuck knowledge parsing tasks to failed state (distributed=%v)",
			result.RowsAffected, distributed)
	}

	// 2. Reset knowledge summary tasks
	resultSummary := stuckKnowledgeSummaryQuery(db, distributed, staleCutoff).Updates(map[string]interface{}{
		"summary_status": types.SummaryStatusFailed,
	})
	if resultSummary.Error != nil {
		logger.Warnf(context.Background(), "Failed to reset pending summary tasks: %v", resultSummary.Error)
	} else if resultSummary.RowsAffected > 0 {
		logger.Infof(context.Background(),
			"Reset %d stuck summary generation tasks to failed state (distributed=%v)",
			resultSummary.RowsAffected, distributed)
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

func stuckKnowledgeParseQuery(db *gorm.DB, distributed bool, staleCutoff time.Time) *gorm.DB {
	q := db.Model(&types.Knowledge{}).
		Where("parse_status IN ?", []string{
			types.ParseStatusPending,
			types.ParseStatusProcessing,
			types.ParseStatusFinalizing,
			types.ParseStatusDeleting,
		})
	if distributed {
		q = q.Where("updated_at < ?", staleCutoff)
	}
	return q
}

func stuckKnowledgeSummaryQuery(db *gorm.DB, distributed bool, staleCutoff time.Time) *gorm.DB {
	q := db.Model(&types.Knowledge{}).
		Where("summary_status IN ?", []string{types.SummaryStatusPending, types.SummaryStatusProcessing})
	if distributed {
		q = q.Where("updated_at < ?", staleCutoff)
	}
	return q
}

func stuckSyncLogQuery(db *gorm.DB, distributed bool, staleCutoff time.Time) *gorm.DB {
	q := db.Model(&types.SyncLog{}).
		Where("status = ?", types.SyncLogStatusRunning)
	if distributed {
		q = q.Where("started_at < ?", staleCutoff)
	}
	return q
}
