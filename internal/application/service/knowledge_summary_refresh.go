package service

import (
	"context"
	"encoding/json"
	"errors"
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
	if repo == nil || taskEnqueuer == nil || kbReader == nil {
		return fmt.Errorf("summary refresh dependencies are unavailable")
	}
	kb, err := kbReader.GetKnowledgeBaseByID(ctx, knowledge.KnowledgeBaseID)
	if err != nil {
		return err
	}
	if kb.SummaryModelID == "" {
		return fmt.Errorf("summary model is not configured")
	}
	if tracker == nil {
		tracker = noopSpanTracker{}
	}
	attempt, err := latestKnowledgeAttempt(ctx, tracker, knowledge.ID)
	if err != nil {
		return fmt.Errorf("load summary refresh generation for knowledge %s: %w", knowledge.ID, err)
	}
	language, _ := types.LanguageFromContext(ctx)
	payload := types.SummaryGenerationPayload{
		TenantID:        knowledge.TenantID,
		KnowledgeBaseID: knowledge.KnowledgeBaseID,
		KnowledgeID:     knowledge.ID,
		Language:        language,
		Attempt:         attempt,
		Refresh:         true,
	}
	langfuse.InjectTracing(ctx, &payload)
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	task := asynq.NewTask(types.TypeSummaryGeneration, payloadBytes,
		asynq.Queue(types.QueueSummary), asynq.MaxRetry(3), asynq.Timeout(30*time.Minute))
	if err := updateSummaryRefreshStatus(
		ctx, repo, knowledge.ID, attempt, types.SummaryStatusPending,
	); err != nil {
		return err
	}
	if _, err = taskEnqueuer.Enqueue(task); err != nil {
		if statusErr := updateSummaryRefreshStatus(
			ctx, repo, knowledge.ID, attempt, types.SummaryStatusFailed,
		); statusErr != nil {
			return errors.Join(err, statusErr)
		}
		return err
	}
	return nil
}

func updateSummaryRefreshStatus(
	ctx context.Context,
	repo interfaces.KnowledgeRepository,
	knowledgeID string,
	attempt int,
	status string,
) error {
	return updateCompletedSummaryFields(ctx, repo, knowledgeID, attempt, map[string]interface{}{
		"summary_status": status,
		"updated_at":     time.Now(),
	})
}

func updateCompletedSummaryFields(
	ctx context.Context,
	repo interfaces.KnowledgeRepository,
	knowledgeID string,
	attempt int,
	values map[string]interface{},
) error {
	if repo == nil {
		return fmt.Errorf("knowledge repository is unavailable")
	}
	if attempt > 0 {
		transitioner, ok := repo.(interfaces.KnowledgeAttemptTransitioner)
		if !ok {
			return fmt.Errorf("knowledge repository does not support attempt-aware summary refresh")
		}
		updated, err := transitioner.TransitionKnowledgeForAttempt(
			ctx, knowledgeID, attempt, types.ParseStatusCompleted, values,
		)
		if err != nil {
			return err
		}
		if !updated {
			return ErrSummaryRefreshStale
		}
		return nil
	}
	// Compatibility for knowledge created before attempt tracking. Keep the
	// status predicate even here so a concurrent reparse/delete cannot be
	// overwritten by the refresh producer.
	conditional, ok := repo.(interfaces.KnowledgeConditionalUpdater)
	if !ok {
		return fmt.Errorf("knowledge repository does not support conditional summary refresh")
	}
	updated, err := conditional.UpdateKnowledgeColumnsIfUnchanged(
		ctx, knowledgeID, types.ParseStatusCompleted, time.Time{}, values,
	)
	if err != nil {
		return err
	}
	if !updated {
		return ErrSummaryRefreshStale
	}
	return nil
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
