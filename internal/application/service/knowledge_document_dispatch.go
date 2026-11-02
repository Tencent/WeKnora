package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/tracing/langfuse"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/hibiken/asynq"
)

const (
	documentDispatchOp = "process"
	// One document at a time is deliberate: the dispatcher is the fairness
	// boundary. The normal document worker pool still controls total work.
	documentDispatchBatchSize = 1
	documentDispatchDelay     = 50 * time.Millisecond
)

// DocumentDispatchPayload is the small wake-up payload for one knowledge-base
// document lane. The durable rows carry the actual document payload.
type DocumentDispatchPayload struct {
	types.TracingContext
	TenantID        uint64 `json:"tenant_id"`
	KnowledgeBaseID string `json:"knowledge_base_id"`
}

// enqueueDocumentProcessTask is the single admission point for document
// processing. Deployments with task_pending_ops use a durable per-KB lane;
// test doubles and legacy Lite wiring without that repository retain the
// direct Asynq path.
func (s *knowledgeService) enqueueDocumentProcessTask(ctx context.Context, task *asynq.Task) (*asynq.TaskInfo, error) {
	if task == nil {
		return nil, errors.New("document process task is nil")
	}
	if task.Type() != types.TypeDocumentProcess || s.taskPendingRepo == nil {
		return s.task.Enqueue(task)
	}

	var payload types.DocumentProcessPayload
	if err := json.Unmarshal(task.Payload(), &payload); err != nil {
		return nil, fmt.Errorf("decode document process payload: %w", err)
	}
	if payload.TenantID == 0 || payload.KnowledgeBaseID == "" || payload.KnowledgeID == "" {
		return nil, errors.New("document process payload is missing tenant, knowledge base, or knowledge ID")
	}

	op := &types.TaskPendingOp{
		TenantID: payload.TenantID,
		TaskType: types.TypeDocumentProcess,
		Scope:    types.TaskScopeKnowledgeBase,
		ScopeID:  payload.KnowledgeBaseID,
		Op:       documentDispatchOp,
		DedupKey: payload.KnowledgeID,
		Payload:  append([]byte(nil), task.Payload()...),
	}
	accepted := true
	var err error
	if guard, ok := s.taskPendingRepo.(interfaces.TaskPendingOpsKnowledgeBaseGuard); ok {
		accepted, err = guard.EnqueueIfKnowledgeBaseActive(ctx, op)
	} else {
		err = s.taskPendingRepo.Enqueue(ctx, op)
	}
	if err != nil {
		return nil, fmt.Errorf("enqueue document process pending op: %w", err)
	}
	if !accepted {
		return nil, fmt.Errorf("knowledge base %s is no longer active", payload.KnowledgeBaseID)
	}

	info, err := s.enqueueDocumentDispatchTrigger(ctx, payload.TenantID, payload.KnowledgeBaseID)
	if err != nil {
		// The pending row is intentionally retained. A later recovery pass or
		// another document admission will re-arm the lane without losing work.
		return nil, err
	}
	return info, nil
}

// EnqueueDocumentDispatchTrigger schedules the coalesced wake-up task for one
// knowledge-base document lane. It is exported for startup recovery, while
// normal producers call the knowledge service wrapper below.
func EnqueueDocumentDispatchTrigger(
	ctx context.Context,
	taskEnqueuer interfaces.TaskEnqueuer,
	tenantID uint64,
	kbID string,
) (*asynq.TaskInfo, error) {
	if taskEnqueuer == nil {
		return nil, errors.New("document dispatch task enqueuer is nil")
	}
	payload := DocumentDispatchPayload{TenantID: tenantID, KnowledgeBaseID: kbID}
	langfuse.InjectTracing(ctx, &payload)
	b, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal document dispatch payload: %w", err)
	}

	// Alternate two IDs when a running dispatcher re-arms itself. Asynq keeps
	// the current task ID occupied until the handler returns, so using the same
	// ID would silently drop the continuation. Producers still use the base ID,
	// which coalesces a burst of admissions into one queued trigger.
	baseID := "document-dispatch:" + kbID
	id := baseID
	if current, ok := asynq.GetTaskID(ctx); ok {
		switch current {
		case baseID:
			id = baseID + ":next"
		case baseID + ":next":
			id = baseID
		}
	}
	task := asynq.NewTask(
		types.TypeDocumentDispatch,
		b,
		asynq.Queue(types.QueueDocumentDispatch),
		asynq.MaxRetry(10),
		asynq.Timeout(10*time.Minute),
		asynq.ProcessIn(documentDispatchDelay),
		asynq.TaskID(id),
	)
	info, err := taskEnqueuer.Enqueue(task)
	if err != nil {
		if errors.Is(err, asynq.ErrTaskIDConflict) || errors.Is(err, asynq.ErrDuplicateTask) {
			return info, nil
		}
		return nil, fmt.Errorf("enqueue document dispatch trigger: %w", err)
	}
	return info, nil
}

func (s *knowledgeService) enqueueDocumentDispatchTrigger(
	ctx context.Context,
	tenantID uint64,
	kbID string,
) (*asynq.TaskInfo, error) {
	return EnqueueDocumentDispatchTrigger(ctx, s.task, tenantID, kbID)
}

func (s *knowledgeService) rearmDocumentDispatchIfPending(
	ctx context.Context,
	payload *types.DocumentProcessPayload,
) {
	if payload == nil || payload.TenantID == 0 || payload.KnowledgeBaseID == "" || s.taskPendingRepo == nil {
		return
	}
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	count, err := s.taskPendingRepo.PendingCount(
		cleanupCtx,
		types.TypeDocumentProcess,
		types.TaskScopeKnowledgeBase,
		payload.KnowledgeBaseID,
	)
	if err != nil {
		logger.Warnf(ctx, "count pending document ops for KB %s: %v", payload.KnowledgeBaseID, err)
		return
	}
	if count == 0 {
		return
	}
	if _, err := s.enqueueDocumentDispatchTrigger(cleanupCtx, payload.TenantID, payload.KnowledgeBaseID); err != nil {
		logger.Warnf(ctx, "re-arm document dispatch for KB %s: %v", payload.KnowledgeBaseID, err)
	}
}

// scrubDocumentPendingProcess removes queued document admissions for a
// knowledge item that was cancelled or deleted. An already-claimed worker is
// still guarded by the knowledge status check; deleting the durable row keeps
// a later dispatcher from recreating work for a terminal document.
func (s *knowledgeService) scrubDocumentPendingProcess(
	ctx context.Context,
	kbID string,
	knowledgeID string,
	reason string,
) {
	if s.taskPendingRepo == nil || kbID == "" || knowledgeID == "" {
		return
	}
	if err := s.taskPendingRepo.DeleteByDedupKey(
		ctx,
		types.TypeDocumentProcess,
		types.TaskScopeKnowledgeBase,
		kbID,
		knowledgeID,
		documentDispatchOp,
	); err != nil {
		logger.Warnf(ctx, "document %s: failed to scrub pending process ops for knowledge %s: %v", reason, knowledgeID, err)
	}
}

// ProcessDocumentDispatch releases one document from a KB's durable lane.
// The document task owns the pending row until it completes, so a crashed
// dispatcher cannot lose work between claim and Asynq enqueue.
func (s *knowledgeService) ProcessDocumentDispatch(ctx context.Context, t *asynq.Task) error {
	if s.taskPendingRepo == nil {
		return nil
	}
	if s.task == nil {
		return errors.New("document process task enqueuer is nil")
	}
	var payload DocumentDispatchPayload
	if err := json.Unmarshal(t.Payload(), &payload); err != nil {
		return fmt.Errorf("decode document dispatch payload: %w", err)
	}
	if payload.TenantID == 0 || payload.KnowledgeBaseID == "" {
		return errors.New("document dispatch payload is missing tenant or knowledge base ID")
	}
	ctx = types.WithExecutionTenant(ctx, payload.TenantID)

	staleAfter := config.DocumentProcessTimeout(s.config) + 30*time.Minute
	ops, err := s.taskPendingRepo.ClaimBatch(
		ctx,
		types.TypeDocumentProcess,
		types.TaskScopeKnowledgeBase,
		payload.KnowledgeBaseID,
		documentDispatchBatchSize,
		time.Now().Add(-staleAfter),
	)
	if err != nil {
		return fmt.Errorf("claim document process lane: %w", err)
	}
	if len(ops) == 0 {
		return nil
	}

	// ClaimBatch returns every row for a selected dedup key so concurrent
	// consumers cannot split one document's history. Keep only the newest
	// payload for each key; older queued attempts are superseded by it.
	latest := make(map[string]*types.TaskPendingOp, len(ops))
	for _, op := range ops {
		if current := latest[op.DedupKey]; current == nil || op.ID > current.ID {
			latest[op.DedupKey] = op
		}
	}
	for _, op := range ops {
		if latest[op.DedupKey] != op {
			_ = s.taskPendingRepo.DeleteByIDs(ctx, []int64{op.ID})
		}
	}
	for _, op := range latest {
		var document types.DocumentProcessPayload
		if err := json.Unmarshal(op.Payload, &document); err != nil {
			logger.Errorf(ctx, "dropping malformed document process pending op %d: %v", op.ID, err)
			_ = s.taskPendingRepo.DeleteByIDs(ctx, []int64{op.ID})
			continue
		}
		document.PendingOpID = op.ID
		b, err := json.Marshal(document)
		if err != nil {
			_ = s.taskPendingRepo.ReleaseByIDs(ctx, []int64{op.ID})
			return fmt.Errorf("marshal claimed document process payload: %w", err)
		}
		opts := documentProcessTaskOptions(s.config)
		opts = append(opts, asynq.TaskID(fmt.Sprintf("document-process-op:%d", op.ID)))
		task := asynq.NewTask(types.TypeDocumentProcess, b, opts...)
		if _, err := s.task.Enqueue(task); err != nil {
			if errors.Is(err, asynq.ErrTaskIDConflict) || errors.Is(err, asynq.ErrDuplicateTask) {
				// The deterministic task ID makes dispatch idempotent across a
				// dispatcher retry. Keep the claim owned by the existing worker;
				// releasing it here could create a second document task while the
				// first one is still running.
				continue
			}
			_ = s.taskPendingRepo.ReleaseByIDs(ctx, []int64{op.ID})
			return fmt.Errorf("enqueue claimed document process task: %w", err)
		}
	}

	// Continue the lane after the current document has been admitted. The
	// current row remains claimed until ProcessDocument settles it; ClaimBatch
	// therefore advances to another document key on the next trigger.
	s.rearmDocumentDispatchIfPending(ctx, &types.DocumentProcessPayload{
		TenantID:        payload.TenantID,
		KnowledgeBaseID: payload.KnowledgeBaseID,
	})
	return nil
}

func documentPendingTaskIsFinalRetry(ctx context.Context) bool {
	retried, maxRetry := 0, 0
	if value, limit, ok := types.TaskRetryMetadataFromContext(ctx); ok {
		retried, maxRetry = value, limit
	} else {
		retried, _ = asynq.GetRetryCount(ctx)
		maxRetry, _ = asynq.GetMaxRetry(ctx)
	}
	return retried >= maxRetry
}

func (s *knowledgeService) settleDocumentPendingOp(ctx context.Context, payload *types.DocumentProcessPayload, err error) {
	if payload == nil || payload.PendingOpID == 0 || s.taskPendingRepo == nil {
		return
	}
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	ids := []int64{payload.PendingOpID}
	if err == nil {
		if deleteErr := s.taskPendingRepo.DeleteByIDs(cleanupCtx, ids); deleteErr != nil {
			logger.Warnf(ctx, "delete completed document pending op %d: %v", payload.PendingOpID, deleteErr)
		}
		s.rearmDocumentDispatchIfPending(ctx, payload)
		return
	}
	if errors.Is(err, asynq.SkipRetry) || documentPendingTaskIsFinalRetry(ctx) {
		_, _ = s.taskPendingRepo.IncrFailCount(cleanupCtx, payload.PendingOpID)
		if releaseErr := s.taskPendingRepo.ReleaseByIDs(cleanupCtx, ids); releaseErr != nil {
			logger.Warnf(ctx, "release failed document pending op %d: %v", payload.PendingOpID, releaseErr)
		}
		// Re-arm the lane after the retry budget is exhausted. The dispatcher
		// that admitted this task may already have observed the fresh claim and
		// returned without scheduling another trigger.
		s.rearmDocumentDispatchIfPending(ctx, payload)
	}
}
