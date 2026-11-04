package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/tracing/langfuse"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/hibiken/asynq"
)

const (
	documentLeaseDuration = 2 * time.Minute
	documentDispatchDelay = 2 * time.Second
)

var errDocumentScopeInactive = errors.New("document scope is no longer active")

type documentDispatchPayload struct {
	TenantID uint64 `json:"tenant_id"`
	Phase    int    `json:"phase"`
}

func documentDispatchID(tenantID uint64, phase int) string {
	return fmt.Sprintf("document-dispatch-%d-%d", tenantID, phase)
}

// StopDocumentDispatches prevents admission, cancels active document work and
// waits for heartbeat exit and fenced bookkeeping within the caller's deadline.
func (s *knowledgeService) StopDocumentDispatches(ctx context.Context) error {
	return s.documentDispatches.stop(ctx)
}

// enqueueDocument persists the complete original task before sending a wakeup.
// A lost wakeup does not undo acceptance: periodic reconciliation recreates it.
func (s *knowledgeService) enqueueDocument(ctx context.Context, task *asynq.Task) (*asynq.TaskInfo, error) {
	return s.enqueueDocumentMode(ctx, task, 0, false)
}

func (s *knowledgeService) enqueueDocumentWithFailures(
	ctx context.Context, task *asynq.Task, failures int,
) (*asynq.TaskInfo, error) {
	return s.enqueueDocumentMode(ctx, task, failures, true)
}

func (s *knowledgeService) enqueueDocumentMode(
	ctx context.Context, task *asynq.Task, failures int, legacy bool,
) (*asynq.TaskInfo, error) {
	scheduler, ok := s.taskPendingRepo.(interfaces.DocumentSchedulerRepository)
	if !ok {
		return s.task.Enqueue(task)
	} // Compatibility with embedded/test implementations.
	var payload types.DocumentProcessPayload
	if err := json.Unmarshal(task.Payload(), &payload); err != nil {
		return nil, err
	}
	if payload.TenantID == 0 || payload.KnowledgeID == "" || payload.KnowledgeBaseID == "" {
		return nil, errors.New("document task requires tenant, knowledge and knowledge base")
	}
	op := &types.TaskPendingOp{
		TenantID: payload.TenantID, TaskType: types.TypeDocumentProcess,
		Scope: types.TaskScopeKnowledgeBase, ScopeID: payload.KnowledgeBaseID, Op: "process",
		DedupKey: payload.KnowledgeID, Payload: append([]byte(nil), task.Payload()...), FailCount: failures,
	}
	var accepted bool
	var err error
	if generations, supported := s.taskPendingRepo.(interfaces.DocumentGenerationRepository); supported && !legacy {
		token, exists := types.DocumentGenerationFromContext(ctx, payload.TenantID, payload.KnowledgeID)
		if !exists {
			token, err = generations.InitialDocumentGeneration(
				ctx, payload.TenantID, payload.KnowledgeID, payload.KnowledgeBaseID)
		}
		if err == nil && token != "" {
			accepted, err = generations.EnqueueDocumentGeneration(ctx, op, token)
		}
		if err == nil && !accepted {
			return &asynq.TaskInfo{Type: task.Type(), Payload: task.Payload()}, nil
		}
	} else {
		accepted, err = scheduler.EnqueueDocument(ctx, op)
	}
	if err != nil {
		return nil, err
	}
	if !accepted {
		return nil, errDocumentScopeInactive
	}
	if err := s.enqueueDocumentDispatch(ctx, payload.TenantID, 0, 0); err != nil {
		logger.Warnf(ctx, "Document accepted durably; wakeup will be reconciled: %v", err)
	}
	return &asynq.TaskInfo{
		ID: fmt.Sprintf("document-pending-%d", op.ID), Type: types.TypeDocumentProcess,
		Queue: types.QueueDefault, Payload: task.Payload(),
	}, nil
}

// AdmitLegacyDocument converts already queued payloads into durable work. Duplicate
// deliveries retain their existing attempt and durable retry budget.
func (s *knowledgeService) AdmitLegacyDocument(ctx context.Context, task *asynq.Task) error {
	if _, ok := s.taskPendingRepo.(interfaces.DocumentSchedulerRepository); !ok {
		return s.ProcessDocument(ctx, task)
	}
	var payload types.DocumentProcessPayload
	if err := json.Unmarshal(task.Payload(), &payload); err != nil {
		return fmt.Errorf("invalid legacy document: %w", asynq.SkipRetry)
	}
	if attemptSuperseded(ctx, s.tracker(), payload.KnowledgeID, payload.Attempt) {
		return nil
	}
	knowledge, err := s.repo.GetKnowledgeByID(ctx, payload.TenantID, payload.KnowledgeID)
	if errors.Is(err, repository.ErrKnowledgeNotFound) || (err == nil && knowledge == nil) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := validateProcessingKnowledge(
		knowledge, payload.TenantID, payload.KnowledgeBaseID, payload.KnowledgeID,
	); err != nil {
		return nil
	}
	switch knowledge.ParseStatus {
	case types.ParseStatusCompleted, types.ParseStatusFinalizing, types.ParseStatusCancelled, types.ParseStatusDeleting:
		return nil
	}
	failures, _ := asynq.GetRetryCount(ctx)
	if count, _, ok := types.TaskRetryMetadataFromContext(ctx); ok {
		failures = count
	}
	_, err = s.enqueueDocumentWithFailures(ctx, task, failures)
	if errors.Is(err, errDocumentScopeInactive) {
		return nil // The scope or attempt became obsolete while admission was waiting.
	}
	return err
}

func (s *knowledgeService) enqueueDocumentDispatch(
	ctx context.Context, tenantID uint64, phase int, delay time.Duration,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.documentDispatches.stopping() {
		return context.Canceled
	}
	payload, _ := json.Marshal(documentDispatchPayload{TenantID: tenantID, Phase: phase})
	task := asynq.NewTask(types.TypeDocumentDispatch, payload,
		asynq.Queue(types.QueueDefault), asynq.TaskID(documentDispatchID(tenantID, phase)),
		asynq.ProcessIn(delay), asynq.MaxRetry(3), asynq.Retention(0),
		asynq.Timeout(config.DocumentProcessTimeout(s.config)+time.Minute))
	_, err := s.task.Enqueue(task)
	if errors.Is(err, asynq.ErrTaskIDConflict) || errors.Is(err, asynq.ErrDuplicateTask) {
		return nil
	}
	return err
}

func documentOutcome(err error, failures int) string {
	if err == nil {
		return "success"
	}
	if failures >= 3 || errors.Is(err, asynq.SkipRetry) {
		return "terminal"
	}
	return "retry"
}

func invokeDocumentTask(ctx context.Context, failures int, execute func(context.Context) error) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("document processing panic: %v", recovered)
		}
	}()
	return execute(types.WithTaskRetryMetadata(ctx, failures, 3))
}

// ProcessDocumentDispatch admits one globally eligible tenant only when a worker
// starts. Origin is only a bounded wakeup owner, never an assignment to a document.
func (s *knowledgeService) ProcessDocumentDispatch(ctx context.Context, task *asynq.Task) error {
	var token documentDispatchPayload
	decodeErr := json.Unmarshal(task.Payload(), &token)
	if decodeErr != nil || token.TenantID == 0 || token.Phase < 0 || token.Phase > 1 {
		return fmt.Errorf("invalid document dispatcher token: %w", asynq.SkipRetry)
	}
	scheduler, ok := s.taskPendingRepo.(interfaces.DocumentSchedulerRepository)
	if !ok {
		return errors.New("document scheduler repository unavailable")
	}
	ctx, release, admitted := s.documentDispatches.begin(ctx)
	if !admitted {
		return nil // Durable work is rearmed after restart; no new lease is claimed.
	}
	defer release()
	defer func() {
		if s.documentDispatches.stopping() {
			return
		}
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		pending, err := scheduler.HasDocumentWork(cleanup, token.TenantID)
		if err == nil && pending {
			err = s.enqueueDocumentDispatch(cleanup, token.TenantID, 1-token.Phase, documentDispatchDelay)
		}
		if err != nil {
			logger.Warnf(cleanup, "Document dispatcher rearm failed: %v", err)
		}
	}()
	claim, err := scheduler.ClaimDocument(ctx, config.DocumentTenantInflightLimit(s.config), documentLeaseDuration)
	if err != nil || claim == nil {
		return err
	}
	outcome, lastError := "release", ""
	var payload types.DocumentProcessPayload
	defer func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		next := time.Now().Add(time.Duration(claim.Op.FailCount+1) * 5 * time.Second)
		settled, finishErr := scheduler.FinishDocument(cleanup, claim, outcome, next, lastError)
		if finishErr != nil {
			logger.Errorf(cleanup, "Document lease completion failed; stale recovery will reconcile: %v", finishErr)
			return
		}
		if settled && outcome == "terminal" && payload.Attempt > 0 &&
			!attemptSuperseded(cleanup, s.tracker(), payload.KnowledgeID, payload.Attempt) {
			s.tracker().FinalizeAttempt(cleanup, payload.KnowledgeID, payload.Attempt,
				types.SpanStatusFailed, nil, "TASK_TIMEOUT", lastError)
		}
	}()
	if err = json.Unmarshal(claim.Op.Payload, &payload); err != nil {
		outcome, lastError = "terminal", err.Error()
		return nil
	}
	if claim.Generation != "" {
		ctx = types.WithDocumentGeneration(ctx, payload.TenantID, payload.KnowledgeID, claim.Generation)
	}
	// Repository checks scope existence. The service additionally checks the parse
	// attempt and source identity before entering the unchanged processing pipeline.
	knowledge, err := s.repo.GetKnowledgeByID(ctx, payload.TenantID, payload.KnowledgeID)
	if errors.Is(err, repository.ErrKnowledgeNotFound) {
		outcome = "success"
		return nil
	}
	if err != nil {
		return err
	}
	if knowledge == nil {
		outcome = "success"
		return nil
	}
	if err = validateProcessingKnowledge(
		knowledge, payload.TenantID, payload.KnowledgeBaseID, payload.KnowledgeID,
	); err != nil {
		outcome = "success"
		return nil
	}
	switch knowledge.ParseStatus {
	case types.ParseStatusCompleted, types.ParseStatusFinalizing, types.ParseStatusCancelled, types.ParseStatusDeleting:
		outcome = "success"
		return nil
	}
	if attemptSuperseded(ctx, s.tracker(), payload.KnowledgeID, payload.Attempt) ||
		(payload.FilePath != "" && knowledge.FilePath != "" && payload.FilePath != knowledge.FilePath) {
		outcome = "success"
		return nil
	}
	// Preflight may have waited longer than the lease. Recheck ownership before
	// entering processing so an expired owner cannot start new model work.
	held, err := scheduler.RenewDocument(ctx, claim, documentLeaseDuration)
	if err != nil || !held {
		return err
	}
	runCtx, cancel := context.WithTimeout(ctx, config.DocumentProcessTimeout(s.config))
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		ticker := time.NewTicker(documentLeaseDuration / 3)
		defer ticker.Stop()
		for {
			select {
			case <-runCtx.Done():
				return
			case <-ticker.C:
				renewCtx, renewCancel := context.WithTimeout(runCtx, 10*time.Second)
				held, renewErr := scheduler.RenewDocument(renewCtx, claim, documentLeaseDuration)
				renewCancel()
				if renewErr != nil || !held {
					cancel()
					return
				}
			}
		}
	}()
	err = invokeDocumentGenerationTask(runCtx, claim.Op.FailCount, func(execCtx context.Context) error {
		return langfuse.AsynqMiddleware()(asynq.HandlerFunc(s.ProcessDocument)).ProcessTask(
			execCtx, asynq.NewTask(types.TypeDocumentProcess, claim.Op.Payload))
	})
	if err == nil && runCtx.Err() != nil {
		err = runCtx.Err()
	}
	cancel()
	<-stopped
	outcome = documentOutcome(err, claim.Op.FailCount)
	if err != nil {
		lastError = err.Error()
	}
	// The database owns the retry budget; the generic token itself succeeded.
	return nil
}

// ReconcileDocuments recovers abandoned execution and re-seeds only distinct
// pending tenants. Known archived stable IDs are reactivated without deleting
// a possibly active token. Inspection failures are treated as unknown.
func (s *knowledgeService) ReconcileDocuments(ctx context.Context) error {
	scheduler, ok := s.taskPendingRepo.(interfaces.DocumentSchedulerRepository)
	if !ok {
		return nil
	}
	if recovery, ok := scheduler.(interfaces.DocumentFailureRecoveryRepository); ok {
		failed, err := recovery.RecoverDocumentFailures(ctx)
		if err != nil {
			return err
		}
		for _, op := range failed {
			var payload types.DocumentProcessPayload
			if op == nil || json.Unmarshal(op.Payload, &payload) != nil || payload.Attempt <= 0 ||
				payload.TenantID != op.TenantID || payload.KnowledgeID != op.DedupKey ||
				payload.KnowledgeBaseID != op.ScopeID ||
				attemptSuperseded(ctx, s.tracker(), payload.KnowledgeID, payload.Attempt) {
				continue
			}
			s.tracker().FinalizeAttempt(ctx, payload.KnowledgeID, payload.Attempt,
				types.SpanStatusFailed, nil, "TASK_TIMEOUT", "document execution lease expired")
		}
	} else if err := scheduler.RecoverDocuments(ctx); err != nil {
		return err
	}
	tenants, err := scheduler.DocumentTenants(ctx)
	if err != nil {
		return err
	}
	var failures []error
	for _, tenantID := range tenants {
		for phase := 0; phase < 2; phase++ {
			if inspector, ok := s.taskInspector.(interfaces.RuntimeTaskInspector); ok {
				task, supported, inspectErr := inspector.GetRuntimeTask(
					ctx, types.QueueDefault, documentDispatchID(tenantID, phase))
				if inspectErr != nil && !errors.Is(inspectErr, asynq.ErrTaskNotFound) &&
					!errors.Is(inspectErr, asynq.ErrQueueNotFound) {
					failures = append(failures, inspectErr)
					continue
				}
				if supported && task != nil {
					if task.Type == types.TypeDocumentDispatch && task.State == types.RuntimeTaskArchived {
						_, err := inspector.RunRuntimeTask(ctx, types.QueueDefault, task.ID)
						if err != nil {
							failures = append(failures, err)
						}
					}
					continue
				}
			}
			if err := s.enqueueDocumentDispatch(ctx, tenantID, phase, 0); err != nil {
				failures = append(failures, err)
			}
		}
	}
	return errors.Join(failures...)
}

func (s *knowledgeService) snapshotDocumentPending(
	ctx context.Context, tenantID uint64, knowledgeID string,
) ([]int64, error) {
	cleanup, ok := s.taskPendingRepo.(interfaces.DocumentPendingCleanupRepository)
	if !ok {
		return nil, nil
	}
	return cleanup.SnapshotDocumentPending(ctx, tenantID, knowledgeID)
}

func (s *knowledgeService) scrubDocumentPendingIDs(
	ctx context.Context, tenantID uint64, knowledgeID string, ids []int64,
) {
	cleanup, ok := s.taskPendingRepo.(interfaces.DocumentPendingCleanupRepository)
	if !ok || len(ids) == 0 {
		return
	}
	if err := cleanup.DeleteDocumentPending(ctx, tenantID, knowledgeID, ids); err != nil {
		logger.Warnf(ctx, "Failed to scrub durable document work for %s: %v", knowledgeID, err)
	}
}
