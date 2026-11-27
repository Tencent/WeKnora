package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/Tencent/WeKnora/internal/application/access"
	werrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/tracing/langfuse"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/hibiken/asynq"
)

type summaryKnowledgeBaseReader interface {
	GetKnowledgeBaseByID(ctx context.Context, id string) (*types.KnowledgeBase, error)
}

// ErrSummaryRefreshStale means the source chunks or metadata changed while a
// summary refresh was running. The caller should discard the result and leave
// the current summary_status untouched so a newer refresh can finish.
var ErrSummaryRefreshStale = errors.New("summary refresh superseded")

// summarySourceChanged reports whether chunk bodies or document metadata changed
// after a summary job captured its inputs. Database lookup failures are
// returned separately so callers do not treat transient read errors as stale
// work that can be silently discarded.
func summarySourceChanged(
	ctx context.Context,
	repo interfaces.KnowledgeRepository,
	chunkRepo interfaces.ChunkRepository,
	tenantID uint64,
	knowledgeID string,
	metadataVersion string,
	sourceChunks []*types.Chunk,
) (bool, error) {
	latestKnowledge, err := repo.GetKnowledgeByID(ctx, tenantID, knowledgeID)
	if err != nil {
		return false, err
	}
	if string(latestKnowledge.CustomMetadata) != metadataVersion {
		return true, nil
	}
	for _, sourceChunk := range sourceChunks {
		latestChunk, getErr := chunkRepo.GetChunkByID(ctx, tenantID, sourceChunk.ID)
		if getErr != nil {
			return false, getErr
		}
		if latestChunk.ContentRevision != sourceChunk.ContentRevision ||
			latestChunk.IsEnabled != sourceChunk.IsEnabled {
			return true, nil
		}
	}
	return false, nil
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
	task, err := newSummaryRefreshTask(ctx, tracker, knowledge)
	if err != nil {
		markFailed()
		return err
	}
	if err := repo.UpdateKnowledgeColumn(ctx, knowledge.ID, "summary_status", types.SummaryStatusPending); err != nil {
		return err
	}
	if _, err := taskEnqueuer.Enqueue(task); err != nil {
		markFailed()
		return err
	}
	return nil
}

func newSummaryRefreshTask(ctx context.Context, tracker SpanTracker, knowledge *types.Knowledge) (*asynq.Task, error) {
	if tracker == nil {
		tracker = noopSpanTracker{}
	}
	payload := types.SummaryGenerationPayload{
		TenantID:        knowledge.TenantID,
		KnowledgeBaseID: knowledge.KnowledgeBaseID,
		KnowledgeID:     knowledge.ID,
		Language:        types.LanguageFromContextOrDefault(ctx),
		Attempt:         tracker.LatestAttempt(ctx, knowledge.ID),
		Refresh:         true,
	}
	langfuse.InjectTracing(ctx, &payload)
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	return asynq.NewTask(types.TypeSummaryGeneration, payloadBytes,
		asynq.Queue(types.QueueSummary), asynq.MaxRetry(3), asynq.Timeout(30*time.Minute)), nil
}

// RequestKnowledgeSummaryRefresh enqueues an async summary refresh for
// documents that already have summary enrichment enabled.
func (s *knowledgeService) RequestKnowledgeSummaryRefresh(
	ctx context.Context, knowledgeID string,
) error {
	tenantID := types.MustTenantIDFromContext(ctx)
	knowledge, err := s.repo.GetKnowledgeByID(ctx, tenantID, knowledgeID)
	if err != nil {
		return err
	}
	return enqueueSummaryRefresh(ctx, s.repo, s.task, s.kbService, s.tracker(), knowledge)
}

// RetryFailedKnowledgeSummaries schedules independent refreshes for parsed
// documents with failed summaries in the requested knowledge base.
func (s *knowledgeService) RetryFailedKnowledgeSummaries(
	ctx context.Context, kbID string,
) (submitted, failed int, err error) {
	tenantID := types.MustTenantIDFromContext(ctx)
	rows, err := s.repo.ListFailedKnowledgeSummaries(ctx, tenantID, kbID)
	if err != nil || len(rows) == 0 {
		return 0, 0, err
	}
	kb, err := s.kbService.GetKnowledgeBaseByID(ctx, kbID)
	if err != nil {
		return 0, 0, err
	}
	if kb.SummaryModelID == "" {
		return 0, 0, werrors.NewBadRequestError("summary model is not configured")
	}
	for _, knowledge := range rows {
		if err := ctx.Err(); err != nil {
			return submitted, failed, err
		}
		queued, err := s.retryFailedKnowledgeSummary(ctx, knowledge)
		if err != nil {
			failed++
			logger.Warnf(ctx, "Failed to retry summary for %s: %v", knowledge.ID, err)
		} else if queued {
			submitted++
		}
	}
	return submitted, failed, nil
}

func (s *knowledgeService) retryFailedKnowledgeSummary(ctx context.Context, knowledge *types.Knowledge) (bool, error) {
	if err := access.RejectMovingKnowledge(knowledge); err != nil {
		return false, err
	}
	task, err := newSummaryRefreshTask(ctx, s.tracker(), knowledge)
	if err != nil {
		return false, err
	}
	claimed, err := s.repo.TransitionKnowledgeSummaryStatus(
		ctx, knowledge, types.SummaryStatusFailed, types.SummaryStatusPending,
	)
	if err != nil || !claimed {
		return false, err
	}
	if _, err := s.task.Enqueue(task); err != nil {
		_, restoreErr := s.repo.TransitionKnowledgeSummaryStatus(
			context.WithoutCancel(ctx), knowledge, types.SummaryStatusPending, types.SummaryStatusFailed)
		return false, errors.Join(err, restoreErr)
	}
	return true, nil
}
