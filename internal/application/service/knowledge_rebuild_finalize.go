package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/application/service/retriever"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/tracing/langfuse"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/hibiken/asynq"
)

const rebuildFinalizePollInterval = 5 * time.Second

func (s *knowledgeService) enqueueKnowledgeRebuildFinalize(
	ctx context.Context,
	payload types.KnowledgeRebuildFinalizePayload,
	delay time.Duration,
) error {
	langfuse.InjectTracing(ctx, &payload)
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	opts := []asynq.Option{
		asynq.Queue(types.QueueLow),
		asynq.MaxRetry(3),
		asynq.Timeout(2 * time.Minute),
	}
	if delay > 0 {
		opts = append(opts, asynq.ProcessIn(delay))
	}
	_, err = s.task.Enqueue(asynq.NewTask(types.TypeKnowledgeRebuildFinalize, payloadBytes, opts...))
	return err
}

// ProcessKnowledgeRebuildFinalize is the commit barrier for an incremental
// rebuild. It polls until only the reserved commit/wiki slots remain, verifies
// all selective artifacts succeeded, then deletes stale indexes before stale
// rows and finally schedules Wiki Reduce against the clean candidate set.
func (s *knowledgeService) ProcessKnowledgeRebuildFinalize(ctx context.Context, task *asynq.Task) error {
	var payload types.KnowledgeRebuildFinalizePayload
	if err := json.Unmarshal(task.Payload(), &payload); err != nil {
		return fmt.Errorf("unmarshal rebuild finalize payload: %w", err)
	}
	ctx = context.WithValue(ctx, types.TenantIDContextKey, payload.TenantID)
	if payload.Language != "" {
		ctx = context.WithValue(ctx, types.LanguageContextKey, payload.Language)
	}
	if payload.RebuildRunID == "" || s.rebuildRunRepo == nil {
		return nil
	}

	current, err := s.rebuildRunRepo.IsCurrent(
		ctx, payload.TenantID, payload.KnowledgeID, payload.RebuildRunID, payload.Attempt,
	)
	if err != nil {
		return fmt.Errorf("validate rebuild finalize run: %w", err)
	}
	if !current {
		logger.Infof(ctx, "[RebuildFinalize] Skip superseded run=%s knowledge=%s", payload.RebuildRunID, payload.KnowledgeID)
		return nil
	}

	knowledge, err := s.repo.GetKnowledgeByID(ctx, payload.TenantID, payload.KnowledgeID)
	if err != nil {
		return fmt.Errorf("get knowledge for rebuild finalize: %w", err)
	}
	if knowledge == nil {
		return fmt.Errorf("knowledge %s not found for rebuild finalize", payload.KnowledgeID)
	}
	if knowledge.ParseStatus == types.ParseStatusCancelled || knowledge.ParseStatus == types.ParseStatusDeleting {
		_ = s.rebuildRunRepo.SetStatus(ctx, payload.TenantID, payload.RebuildRunID, types.RebuildRunStatusCancelled, knowledge.ParseStatus)
		return nil
	}
	if knowledge.ParseStatus != types.ParseStatusFinalizing {
		logger.Infof(ctx, "[RebuildFinalize] Knowledge %s status=%s, skipping", knowledge.ID, knowledge.ParseStatus)
		return nil
	}
	if knowledge.PendingSubtasksCount > payload.ReservedSubtasks {
		if err := s.enqueueKnowledgeRebuildFinalize(ctx, payload, rebuildFinalizePollInterval); err != nil {
			return fmt.Errorf("reschedule rebuild finalize: %w", err)
		}
		return nil
	}
	if knowledge.PendingSubtasksCount < payload.ReservedSubtasks {
		return fmt.Errorf(
			"rebuild finalize counter underflow: pending=%d reserved=%d",
			knowledge.PendingSubtasksCount, payload.ReservedSubtasks,
		)
	}

	run, err := s.rebuildRunRepo.Get(ctx, payload.TenantID, payload.RebuildRunID)
	if err != nil {
		return fmt.Errorf("get rebuild run for commit: %w", err)
	}
	if run == nil {
		return fmt.Errorf("rebuild run %s not found for commit", payload.RebuildRunID)
	}
	if run.ImagesFailed > 0 || run.ArtifactsFailed > 0 || run.ArtifactsCompleted < run.ArtifactsTotal {
		reason := fmt.Sprintf(
			"rebuild artifacts failed or incomplete: images_failed=%d artifacts=%d/%d failed=%d",
			run.ImagesFailed, run.ArtifactsCompleted, run.ArtifactsTotal, run.ArtifactsFailed,
		)
		if err := s.rebuildRunRepo.FailRun(ctx, payload.TenantID, payload.RebuildRunID, payload.KnowledgeID, reason); err != nil {
			return err
		}
		return nil
	}

	if run.StaleCleanupAt == nil {
		if err := s.commitRebuildStaleData(ctx, payload, knowledge, run); err != nil {
			return err
		}
		if err := s.rebuildRunRepo.MarkStaleCleanupComplete(ctx, payload.TenantID, payload.RebuildRunID); err != nil {
			return fmt.Errorf("mark stale cleanup complete: %w", err)
		}
	}

	if run.WikiReduceRequired && run.WikiReduceEnqueuedAt == nil {
		enqueued, enqueueErr := EnqueueWikiIngest(
			ctx, s.task, s.taskPendingRepo,
			payload.TenantID, payload.KnowledgeBaseID, payload.KnowledgeID, payload.RebuildRunID, payload.Attempt,
		)
		if enqueueErr != nil || !enqueued {
			if enqueueErr != nil {
				return fmt.Errorf("enqueue wiki reduce: %w", enqueueErr)
			}
			return fmt.Errorf("enqueue wiki reduce returned no task")
		}
		if err := s.rebuildRunRepo.MarkWikiReduceEnqueued(ctx, payload.TenantID, payload.RebuildRunID); err != nil {
			return fmt.Errorf("mark wiki reduce enqueued: %w", err)
		}
	}

	if _, err := s.rebuildRunRepo.FinalizeCommit(
		ctx, payload.TenantID, payload.RebuildRunID, payload.KnowledgeID,
	); err != nil {
		return fmt.Errorf("finalize rebuild commit: %w", err)
	}
	logger.Infof(ctx, "[RebuildFinalize] Committed run=%s knowledge=%s", payload.RebuildRunID, payload.KnowledgeID)
	return nil
}

func (s *knowledgeService) commitRebuildStaleData(
	ctx context.Context,
	payload types.KnowledgeRebuildFinalizePayload,
	knowledge *types.Knowledge,
	run *types.KnowledgeRebuildRun,
) error {
	staleResults, err := s.rebuildRunRepo.ListChunkResults(
		ctx, payload.TenantID, payload.RebuildRunID,
		[]string{types.RebuildChunkClassStale}, rebuildManagedChunkTypes,
	)
	if err != nil {
		return fmt.Errorf("list stale rebuild chunks: %w", err)
	}
	staleIDs := make([]string, 0, len(staleResults))
	for _, result := range staleResults {
		if result != nil && result.ChunkID != "" {
			staleIDs = append(staleIDs, result.ChunkID)
		}
	}

	var derivedSummaryIDs []string
	if run.SummaryRequired {
		summaryChunks, listErr := s.chunkService.ListChunksByKnowledgeIDAndTypes(
			ctx, payload.KnowledgeID, []types.ChunkType{types.ChunkTypeSummary},
		)
		if listErr != nil {
			return fmt.Errorf("list summary chunks for cleanup: %w", listErr)
		}
		expectedContent := ""
		if strings.TrimSpace(knowledge.Description) != "" {
			expectedContent = fmt.Sprintf("# Summary\n%s", knowledge.Description)
		}
		for _, chunk := range summaryChunks {
			if chunk != nil && (expectedContent == "" || chunk.Content != expectedContent) {
				derivedSummaryIDs = append(derivedSummaryIDs, chunk.ID)
			}
		}
	}

	indexIDs := append(append([]string{}, staleIDs...), derivedSummaryIDs...)
	kb, err := s.kbService.GetKnowledgeBaseByIDOnly(ctx, payload.KnowledgeBaseID)
	if err != nil {
		return fmt.Errorf("get knowledge base for stale cleanup: %w", err)
	}
	if kb == nil {
		return fmt.Errorf("knowledge base %s not found for stale cleanup", payload.KnowledgeBaseID)
	}
	if len(indexIDs) > 0 && kb.NeedsEmbeddingModel() {
		embeddingModel, modelErr := s.modelService.GetEmbeddingModel(ctx, kb.EmbeddingModelID)
		if modelErr != nil {
			return fmt.Errorf("get embedding model for stale cleanup: %w", modelErr)
		}
		tenantInfo, tenantErr := s.tenantRepo.GetTenantByID(ctx, payload.TenantID)
		if tenantErr != nil {
			return fmt.Errorf("get tenant for stale cleanup: %w", tenantErr)
		}
		ctx = context.WithValue(ctx, types.TenantInfoContextKey, tenantInfo)
		engine, engineErr := retriever.CreateRetrieveEngineForKB(
			ctx, s.retrieveEngine, s.ownership, payload.TenantID, kb.VectorStoreID,
		)
		if engineErr != nil {
			return fmt.Errorf("create retrieve engine for stale cleanup: %w", engineErr)
		}
		if err := engine.DeleteByChunkIDList(ctx, indexIDs, embeddingModel.GetDimensions(), kb.Type); err != nil {
			return fmt.Errorf("delete stale chunk indexes: %w", err)
		}
	}

	// Stale graph attribution must be removed even when graph indexing is now
	// disabled. Otherwise chunks deleted while graph is off survive forever and
	// reappear if graph indexing is enabled again later.
	if len(staleIDs) > 0 && s.graphEngine != nil {
		if err := s.graphEngine.DelGraphChunks(ctx, types.NameSpace{
			KnowledgeBase: payload.KnowledgeBaseID,
			Knowledge:     payload.KnowledgeID,
		}, staleIDs); err != nil {
			return fmt.Errorf("delete stale graph contributions: %w", err)
		}
	}
	if len(indexIDs) > 0 {
		if err := s.chunkService.DeleteChunks(ctx, indexIDs); err != nil {
			return fmt.Errorf("delete stale chunk rows: %w", err)
		}
	}
	return nil
}
