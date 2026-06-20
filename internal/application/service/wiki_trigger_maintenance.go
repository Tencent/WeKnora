package service

import (
	"context"
	"os"
	"strconv"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

const (
	defaultWikiTriggerSweepInterval = 2 * time.Minute
	defaultWikiTriggerSweepLimit    = 500
)

type WikiTriggerMaintenanceRunner struct {
	pendingRepo   interfaces.TaskPendingOpsRepository
	inspector     interfaces.TaskInspector
	enqueuer      interfaces.TaskEnqueuer
	sweepInterval time.Duration
	limit         int
}

func NewWikiTriggerMaintenanceRunner(
	pendingRepo interfaces.TaskPendingOpsRepository,
	inspector interfaces.TaskInspector,
	enqueuer interfaces.TaskEnqueuer,
) *WikiTriggerMaintenanceRunner {
	return &WikiTriggerMaintenanceRunner{
		pendingRepo:   pendingRepo,
		inspector:     inspector,
		enqueuer:      enqueuer,
		sweepInterval: wikiTriggerSweepIntervalFromEnv(),
		limit:         defaultWikiTriggerSweepLimit,
	}
}

func (r *WikiTriggerMaintenanceRunner) Start(ctx context.Context) {
	if r == nil || r.pendingRepo == nil || r.enqueuer == nil {
		return
	}
	go func() {
		ticker := time.NewTicker(r.sweepInterval)
		defer ticker.Stop()
		r.sweep(ctx)
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				r.sweep(ctx)
			}
		}
	}()
}

func (r *WikiTriggerMaintenanceRunner) sweep(ctx context.Context) {
	refs, err := r.pendingRepo.ListPendingWikiKnowledgeBases(ctx, r.limit)
	if err != nil {
		logger.Warnf(ctx, "[WikiTriggerMaintenance] pending KB scan failed: %v", err)
		return
	}
	for _, ref := range refs {
		if ref.KnowledgeBaseID == "" {
			continue
		}
		if r.inspector != nil {
			queued, err := r.inspector.HasQueuedWikiForKnowledgeBase(ctx, ref.KnowledgeBaseID)
			if err != nil {
				logger.Warnf(ctx, "[WikiTriggerMaintenance] trigger probe failed kb=%s: %v", ref.KnowledgeBaseID, err)
				continue
			}
			if queued {
				continue
			}
		}
		if err := enqueueWikiIngestTrigger(ctx, r.enqueuer, WikiIngestPayload{
			TenantID:        ref.TenantID,
			KnowledgeBaseID: ref.KnowledgeBaseID,
		}, 5*time.Second); err != nil {
			logger.Warnf(ctx, "[WikiTriggerMaintenance] trigger enqueue failed kb=%s: %v", ref.KnowledgeBaseID, err)
			continue
		}
		logger.Warnf(ctx, "[WikiTriggerMaintenance] re-enqueued missing wiki ingest trigger kb=%s", ref.KnowledgeBaseID)
	}
}

func wikiTriggerSweepIntervalFromEnv() time.Duration {
	raw := os.Getenv("WEKNORA_WIKI_TRIGGER_SWEEP_INTERVAL_SECONDS")
	if raw == "" {
		return defaultWikiTriggerSweepInterval
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return defaultWikiTriggerSweepInterval
	}
	return time.Duration(n) * time.Second
}
