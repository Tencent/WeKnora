package service

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/tracing/langfuse"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/hibiken/asynq"
	"github.com/redis/go-redis/v9"
)

// KnowledgePostProcessService acts as an orchestrator for all post-processing tasks
// after a document has been parsed and split into chunks (including multimodal OCR/Caption).
type KnowledgePostProcessService struct {
	knowledgeRepo interfaces.KnowledgeRepository
	kbService     interfaces.KnowledgeBaseService
	chunkService  interfaces.ChunkService
	taskEnqueuer  interfaces.TaskEnqueuer
	pendingRepo   interfaces.TaskPendingOpsRepository
	redisClient   *redis.Client
	spanTracker   SpanTracker
}

func NewKnowledgePostProcessService(
	knowledgeRepo interfaces.KnowledgeRepository,
	kbService interfaces.KnowledgeBaseService,
	chunkService interfaces.ChunkService,
	taskEnqueuer interfaces.TaskEnqueuer,
	pendingRepo interfaces.TaskPendingOpsRepository,
	redisClient *redis.Client,
	spanTracker SpanTracker,
) interfaces.TaskHandler {
	return &KnowledgePostProcessService{
		knowledgeRepo: knowledgeRepo,
		kbService:     kbService,
		chunkService:  chunkService,
		taskEnqueuer:  taskEnqueuer,
		pendingRepo:   pendingRepo,
		redisClient:   redisClient,
		spanTracker:   spanTracker,
	}
}

func (s *KnowledgePostProcessService) tracker() SpanTracker {
	if s.spanTracker == nil {
		return noopSpanTracker{}
	}
	return s.spanTracker
}

// Handle implements asynq handler for TypeKnowledgePostProcess.
func (s *KnowledgePostProcessService) Handle(ctx context.Context, task *asynq.Task) error {
	var payload types.KnowledgePostProcessPayload
	if err := json.Unmarshal(task.Payload(), &payload); err != nil {
		return fmt.Errorf("unmarshal knowledge post process payload: %w", err)
	}

	logger.Infof(ctx, "[KnowledgePostProcess] Orchestrating post processing for knowledge: %s", payload.KnowledgeID)

	ctx = context.WithValue(ctx, types.TenantIDContextKey, payload.TenantID)
	if payload.Language != "" {
		ctx = context.WithValue(ctx, types.LanguageContextKey, payload.Language)
	}

	// Resolve attempt: payload carries it from the upstream stage, but
	// fall back to the latest known attempt for compatibility with
	// in-flight tasks queued before this code shipped.
	attempt := payload.Attempt
	if attempt <= 0 {
		attempt = s.tracker().LatestAttempt(ctx, payload.KnowledgeID)
	}

	// Close the multimodal stage span (parent enqueued it as "running"
	// and we never see the per-image fan-in here other than by reaching
	// post-process). If the parent skipped multimodal entirely, the
	// stage row will already be in "skipped" state and EndSpan is a
	// no-op for missing rows. Per-image success/failure counts are NOT
	// aggregated here — the frontend already walks the children when
	// rendering the multimodal stage detail and counts them itself,
	// avoiding an extra query path.
	if mm := s.tracker().LookupStage(ctx, payload.KnowledgeID, attempt, types.StageMultimodal); mm != nil &&
		mm.Kind == types.SpanKindStage {
		s.tracker().EndSpan(ctx, mm, nil)
	}

	postSpan := s.tracker().BeginStage(ctx, payload.KnowledgeID, attempt, types.StagePostProcess, nil)

	// 1. Fetch Knowledge and KB
	knowledge, err := s.knowledgeRepo.GetKnowledgeByIDOnly(ctx, payload.KnowledgeID)
	if err != nil {
		return fmt.Errorf("get knowledge %s: %w", payload.KnowledgeID, err)
	}
	if knowledge == nil {
		logger.Warnf(ctx, "[KnowledgePostProcess] Knowledge %s not found, aborting.", payload.KnowledgeID)
		return nil
	}

	// Skip post-processing entirely when the knowledge has been cancelled
	// by the user or marked for deletion. We must NOT enqueue summary /
	// question / graph / wiki child tasks for an aborted knowledge. We
	// MUST also close postSpan before returning, otherwise it stays in
	// running state forever and the trace viewer shows an orange bar
	// long after the user cancelled (the AbortAttempt sweep ran before
	// we opened postSpan, so the sweep didn't catch this row).
	switch knowledge.ParseStatus {
	case types.ParseStatusCancelled, types.ParseStatusDeleting:
		logger.Infof(ctx,
			"[KnowledgePostProcess] Knowledge %s aborted (%s), skipping post-processing.",
			payload.KnowledgeID, knowledge.ParseStatus,
		)
		s.tracker().SkipSpan(ctx, postSpan,
			"knowledge "+knowledge.ParseStatus+" before postprocess started")
		return nil
	}

	kb, err := s.kbService.GetKnowledgeBaseByIDOnly(ctx, payload.KnowledgeBaseID)
	if err != nil || kb == nil {
		return fmt.Errorf("get knowledge base %s: %w", payload.KnowledgeBaseID, err)
	}

	// 2. Fetch all chunks
	chunks, err := s.chunkService.ListChunksByKnowledgeID(ctx, payload.KnowledgeID)
	if err != nil {
		return fmt.Errorf("list chunks for knowledge %s: %w", payload.KnowledgeID, err)
	}

	// Gather all text-like chunks (including newly added OCR and Caption from multimodal tasks)
	var textChunks []*types.Chunk
	for _, c := range chunks {
		if c.ChunkType == types.ChunkTypeText || c.ChunkType == types.ChunkTypeImageOCR || c.ChunkType == types.ChunkTypeImageCaption {
			textChunks = append(textChunks, c)
		}
	}

	// 3. Compute the enrichment subtask count up front so we can flip to
	//    "finalizing" with the right counter BEFORE spawning any subtasks.
	//    Each subtask handler atomically decrements pending_subtasks_count
	//    on its terminal exit; the row promotes itself to "completed" when
	//    the counter hits zero (see knowledgeRepository.FinalizeSubtask).
	//
	//    Wiki ingest is NOT counted here — it's a KB-scoped debounced
	//    batch with its own dedup queue; per-knowledge accounting is not
	//    meaningful for it.
	willSpawnSummary := len(textChunks) > 0
	willSpawnQuestion := willSpawnSummary && kb.NeedsEmbeddingModel() &&
		kb.QuestionGenerationConfig != nil && kb.QuestionGenerationConfig.Enabled
	graphChunkCount := 0
	if kb.IsGraphEnabled() {
		graphChunkCount = len(textChunks)
	}
	expectedSubtasks := 0
	if willSpawnSummary {
		expectedSubtasks++
	}
	if willSpawnQuestion {
		expectedSubtasks++
	}
	expectedSubtasks += graphChunkCount

	switch {
	case knowledge.ParseStatus != types.ParseStatusProcessing:
		// The row was already in some other state (deleting / cancelled /
		// failed / completed) when we arrived. Don't touch parse_status
		// and don't spawn enrichment — the upstream that put the row in
		// that state has already decided this attempt is over.
		logger.Infof(ctx, "[KnowledgePostProcess] Knowledge %s is in %s, skipping enrichment fan-out.",
			payload.KnowledgeID, knowledge.ParseStatus)
		s.tracker().EndSpan(ctx, postSpan, types.JSONMap{
			"skipped":         "non_processing_status",
			"observed_status": knowledge.ParseStatus,
		})
		s.tracker().FinalizeAttempt(ctx, payload.KnowledgeID, attempt,
			types.SpanStatusDone, types.JSONMap{
				"skipped":         "non_processing_status",
				"observed_status": knowledge.ParseStatus,
			}, "", "")
		return nil
	case expectedSubtasks == 0:
		// Nothing to enrich — fast path keeps the previous behavior so
		// users without summary/question/graph see 'completed' immediately.
		updates := map[string]interface{}{
			"parse_status": types.ParseStatusCompleted,
			"updated_at":   time.Now(),
		}
		if len(textChunks) > 0 {
			updates["summary_status"] = types.SummaryStatusNone
		}
		if err := s.knowledgeRepo.UpdateKnowledgeColumns(ctx, payload.KnowledgeID, updates); err != nil {
			logger.Warnf(ctx, "[KnowledgePostProcess] Failed to mark %s completed (no subtasks): %v",
				payload.KnowledgeID, err)
		} else {
			logger.Infof(ctx, "[KnowledgePostProcess] Knowledge %s marked completed (no enrichment subtasks).",
				payload.KnowledgeID)
		}
	default:
		// Flip processing → finalizing in one statement so a parallel
		// cancel/delete cannot race us into completed.
		promoted, err := s.knowledgeRepo.SetFinalizing(ctx, payload.KnowledgeID, expectedSubtasks)
		if err != nil {
			logger.Warnf(ctx, "[KnowledgePostProcess] SetFinalizing failed for %s: %v",
				payload.KnowledgeID, err)
		}
		if promoted {
			// Reflect summary status separately so the UI shows the
			// summary as queued for users who already had it visible.
			summaryStatus := types.SummaryStatusNone
			if willSpawnSummary {
				summaryStatus = types.SummaryStatusPending
			}
			if err := s.knowledgeRepo.UpdateKnowledgeColumn(ctx,
				payload.KnowledgeID, "summary_status", summaryStatus); err != nil {
				logger.Warnf(ctx, "[KnowledgePostProcess] Failed to update summary_status for %s: %v",
					payload.KnowledgeID, err)
			}
			logger.Infof(ctx,
				"[KnowledgePostProcess] Knowledge %s entered finalizing (pending_subtasks=%d).",
				payload.KnowledgeID, expectedSubtasks)
		} else {
			// Row was no longer 'processing' (cancel / delete won the race).
			// Skip enrichment entirely so we don't waste LLM quota on a row
			// the user already abandoned.
			logger.Infof(ctx,
				"[KnowledgePostProcess] Knowledge %s no longer in processing, skipping enrichment fan-out.",
				payload.KnowledgeID)
			s.tracker().EndSpan(ctx, postSpan, types.JSONMap{
				"skipped": "knowledge_no_longer_processing",
			})
			s.tracker().FinalizeAttempt(ctx, payload.KnowledgeID, attempt,
				types.SpanStatusDone, types.JSONMap{
					"skipped": "knowledge_no_longer_processing",
				}, "", "")
			return nil
		}
	}

	// 4. Spawn Summary and Question Tasks
	enqueuedSummary := false
	enqueuedQuestion := false
	if willSpawnSummary {
		s.enqueueSummaryGenerationTask(ctx, payload, attempt)
		enqueuedSummary = true
		if willSpawnQuestion {
			enqueuedQuestion = s.enqueueQuestionGenerationIfEnabled(ctx, payload, kb, attempt)
		}
	}

	// 5. Spawn Graph RAG Tasks — only when graph indexing is enabled in IndexingStrategy
	enqueuedGraph := false
	if graphChunkCount > 0 {
		logger.Infof(ctx, "[KnowledgePostProcess] Spawning Graph RAG extract tasks for %d text-like chunks", len(textChunks))
		for i, chunk := range textChunks {
			err := NewChunkExtractTask(ctx, s.taskEnqueuer, payload.TenantID, chunk.ID, kb.SummaryModelID,
				payload.KnowledgeID, attempt, i)
			if err != nil {
				logger.Errorf(ctx, "[KnowledgePostProcess] Failed to create chunk extract task for %s: %v", chunk.ID, err)
			}
		}
		enqueuedGraph = true
	}

	// 6. Spawn Wiki Ingest Task if wiki indexing is enabled in IndexingStrategy
	enqueuedWiki := false
	if kb.IndexingStrategy.WikiEnabled && len(textChunks) > 0 {
		EnqueueWikiIngest(ctx, s.taskEnqueuer, s.pendingRepo, payload.TenantID, payload.KnowledgeBaseID, payload.KnowledgeID)
		logger.Infof(ctx, "[KnowledgePostProcess] Enqueued wiki ingest task for %s", payload.KnowledgeID)
		enqueuedWiki = true
	}
	postOutput := types.JSONMap{
		"chunks_total":      len(textChunks),
		"enqueued_summary":  enqueuedSummary,
		"enqueued_question": enqueuedQuestion,
		"enqueued_wiki":     enqueuedWiki,
		"enqueued_graph":    enqueuedGraph,
	}
	s.tracker().EndSpan(ctx, postSpan, postOutput)
	// Close the root span — the parse pipeline is done. Async
	// downstream stages (summary/question/wiki/graph) record their
	// own spans independently; their finishing extends the trace's
	// end-time but does not reopen the root. A late failure in one
	// of those stages does not poison the parse result.
	s.tracker().FinalizeAttempt(ctx, payload.KnowledgeID, attempt,
		types.SpanStatusDone, postOutput, "", "")
	return nil
}

func (s *KnowledgePostProcessService) enqueueSummaryGenerationTask(ctx context.Context, payload types.KnowledgePostProcessPayload, attempt int) {
	if s.taskEnqueuer == nil {
		return
	}

	taskPayload := types.SummaryGenerationPayload{
		TenantID:        payload.TenantID,
		KnowledgeBaseID: payload.KnowledgeBaseID,
		KnowledgeID:     payload.KnowledgeID,
		Language:        payload.Language,
		Attempt:         attempt,
	}
	langfuse.InjectTracing(ctx, &taskPayload)
	payloadBytes, err := json.Marshal(taskPayload)
	if err != nil {
		logger.Warnf(ctx, "[KnowledgePostProcess] Failed to marshal summary generation payload: %v", err)
		return
	}

	task := asynq.NewTask(types.TypeSummaryGeneration, payloadBytes, asynq.Queue("low"), asynq.MaxRetry(3))
	if _, err := s.taskEnqueuer.Enqueue(task); err != nil {
		logger.Warnf(ctx, "[KnowledgePostProcess] Failed to enqueue summary generation for %s: %v", payload.KnowledgeID, err)
	} else {
		logger.Infof(ctx, "[KnowledgePostProcess] Enqueued summary generation task for %s", payload.KnowledgeID)
	}
}

func (s *KnowledgePostProcessService) enqueueQuestionGenerationIfEnabled(ctx context.Context, payload types.KnowledgePostProcessPayload, kb *types.KnowledgeBase, attempt int) bool {
	if s.taskEnqueuer == nil {
		return false
	}

	if kb.QuestionGenerationConfig == nil || !kb.QuestionGenerationConfig.Enabled {
		return false
	}

	questionCount := kb.QuestionGenerationConfig.QuestionCount
	if questionCount <= 0 {
		questionCount = 3
	}
	if questionCount > 10 {
		questionCount = 10
	}

	taskPayload := types.QuestionGenerationPayload{
		TenantID:        payload.TenantID,
		KnowledgeBaseID: payload.KnowledgeBaseID,
		KnowledgeID:     payload.KnowledgeID,
		QuestionCount:   questionCount,
		Language:        payload.Language,
		Attempt:         attempt,
	}
	langfuse.InjectTracing(ctx, &taskPayload)
	payloadBytes, err := json.Marshal(taskPayload)
	if err != nil {
		logger.Warnf(ctx, "[KnowledgePostProcess] Failed to marshal question generation payload: %v", err)
		return false
	}

	task := asynq.NewTask(types.TypeQuestionGeneration, payloadBytes, asynq.Queue("low"), asynq.MaxRetry(3))
	if _, err := s.taskEnqueuer.Enqueue(task); err != nil {
		logger.Warnf(ctx, "[KnowledgePostProcess] Failed to enqueue question generation for %s: %v", payload.KnowledgeID, err)
		return false
	}
	logger.Infof(ctx, "[KnowledgePostProcess] Enqueued question generation task for %s (count=%d)", payload.KnowledgeID, questionCount)
	return true
}
