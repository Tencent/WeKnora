package service

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/Tencent/WeKnora/internal/tracing/langfuse"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/hibiken/asynq"
)

type summaryKnowledgeBaseReader interface {
	GetKnowledgeBaseByID(ctx context.Context, id string) (*types.KnowledgeBase, error)
}

// restoreSummaryRefreshTenantInfo rebuilds the tenant context normally added
// by the HTTP authentication middleware. Summary refreshes run in an Asynq
// worker, and the retrieve-engine factory needs the full tenant configuration,
// not only TenantIDContextKey, when it reindexes the summary chunk.
func restoreSummaryRefreshTenantInfo(
	ctx context.Context,
	tenantRepo interfaces.TenantRepository,
	tenantID uint64,
) (context.Context, error) {
	if tenantRepo == nil {
		return ctx, fmt.Errorf("tenant repository is unavailable")
	}
	tenant, err := tenantRepo.GetTenantByID(ctx, tenantID)
	if err != nil {
		return ctx, fmt.Errorf("get tenant %d for summary refresh: %w", tenantID, err)
	}
	if tenant == nil {
		return ctx, fmt.Errorf("tenant %d not found for summary refresh", tenantID)
	}
	return context.WithValue(ctx, types.TenantInfoContextKey, tenant), nil
}

// enqueueSummaryRefresh marks an existing summary as queued and enqueues an
// independent refresh task. The pending status is written before Enqueue
// because the Lite executor may run the task synchronously inside Enqueue.
func enqueueSummaryRefresh(
	ctx context.Context,
	repo interfaces.KnowledgeRepository,
	taskEnqueuer interfaces.TaskEnqueuer,
	kbReader summaryKnowledgeBaseReader,
	tracker SpanTracker,
	knowledge *types.Knowledge,
) error {
	if knowledge == nil || knowledge.SummaryStatus == "" || knowledge.SummaryStatus == types.SummaryStatusNone {
		return nil
	}
	markFailed := func() {
		if repo != nil {
			_ = repo.UpdateKnowledgeColumn(ctx, knowledge.ID, "summary_status", types.SummaryStatusFailed)
		}
	}
	if repo == nil || taskEnqueuer == nil || kbReader == nil {
		markFailed()
		return fmt.Errorf("summary refresh dependencies are unavailable")
	}
	kb, err := kbReader.GetKnowledgeBaseByID(ctx, knowledge.KnowledgeBaseID)
	if err != nil {
		markFailed()
		return err
	}
	if kb.SummaryModelID == "" {
		markFailed()
		return fmt.Errorf("summary model is not configured")
	}
	if tracker == nil {
		tracker = noopSpanTracker{}
	}
	language, _ := types.LanguageFromContext(ctx)
	payload := types.SummaryGenerationPayload{
		TenantID:        knowledge.TenantID,
		KnowledgeBaseID: knowledge.KnowledgeBaseID,
		KnowledgeID:     knowledge.ID,
		Language:        language,
		Attempt:         tracker.LatestAttempt(ctx, knowledge.ID),
		Refresh:         true,
	}
	langfuse.InjectTracing(ctx, &payload)
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		markFailed()
		return err
	}
	task := asynq.NewTask(types.TypeSummaryGeneration, payloadBytes,
		asynq.Queue(types.QueueSummary), asynq.MaxRetry(3), asynq.Timeout(30*time.Minute))
	_ = repo.UpdateKnowledgeColumn(ctx, knowledge.ID, "summary_status", types.SummaryStatusPending)
	if _, err = taskEnqueuer.Enqueue(task); err != nil {
		markFailed()
		return err
	}
	return nil
}
