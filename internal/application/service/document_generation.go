package service

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/hibiken/asynq"
)

type documentGenerationCheckKey struct{}

type documentGenerationCheckState struct {
	mu  sync.Mutex
	err error
}

func (s *documentGenerationCheckState) record(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err == nil {
		s.err = err
	}
}

func (s *documentGenerationCheckState) failure() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.err
}

func invokeDocumentGenerationTask(ctx context.Context, failures int, handler func(context.Context) error) error {
	checks := &documentGenerationCheckState{}
	ctx = context.WithValue(ctx, documentGenerationCheckKey{}, checks)
	err := invokeDocumentTask(ctx, failures, handler)
	if checkErr := checks.failure(); checkErr != nil {
		return checkErr
	}
	return err
}

func (s *knowledgeService) bindInitialDocumentGeneration(
	ctx context.Context, knowledge *types.Knowledge,
) (context.Context, error) {
	r, ok := s.taskPendingRepo.(interfaces.DocumentGenerationRepository)
	if !ok || knowledge.IsManual() {
		return ctx, nil
	}
	token, err := r.InitialDocumentGeneration(ctx, knowledge.TenantID, knowledge.ID, knowledge.KnowledgeBaseID)
	if err != nil {
		// The row has already committed. If the first token read failed, retry
		// only the initial generation before settling; never adopt a reparse.
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		initial, initialErr := r.InitialDocumentGeneration(
			cleanup, knowledge.TenantID, knowledge.ID, knowledge.KnowledgeBaseID)
		if initialErr == nil && initial != "" {
			_, settleErr := r.UpdateDocumentGeneration(cleanup, knowledge.TenantID, knowledge.ID, initial,
				map[string]any{
					"parse_status": types.ParseStatusFailed, "error_message": err.Error(),
				})
			if settleErr != nil {
				logger.Errorf(cleanup, "Failed to settle document creation after token error: %v", settleErr)
			}
		} else if initialErr != nil {
			logger.Errorf(cleanup, "Failed to recover initial document token after creation: %v", initialErr)
		}
		return ctx, err
	}
	if token == "" {
		return ctx, types.ErrDocumentGenerationSuperseded
	}
	return types.WithDocumentGeneration(ctx, knowledge.TenantID, knowledge.ID, token), nil
}

// failDocumentGeneration settles preparation failures after ownership advanced
// but before replacement work was accepted. A newer generation always wins.
func (s *knowledgeService) failDocumentGeneration(
	ctx context.Context, knowledge *types.Knowledge, attempt int, cause error,
) {
	if knowledge == nil || cause == nil {
		return
	}
	token, exists := types.DocumentGenerationFromContext(ctx, knowledge.TenantID, knowledge.ID)
	r, supported := s.taskPendingRepo.(interfaces.DocumentGenerationRepository)
	if !exists || !supported {
		return
	}
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	settled, err := r.UpdateDocumentGeneration(cleanup, knowledge.TenantID, knowledge.ID, token, map[string]any{
		"parse_status": types.ParseStatusFailed, "error_message": cause.Error(),
	})
	if err != nil {
		logger.Errorf(cleanup, "Failed to settle document generation preparation: %v", err)
		return
	}
	if settled && attempt > 0 {
		s.tracker().FinalizeAttempt(cleanup, knowledge.ID, attempt,
			types.SpanStatusFailed, nil, "PROCESSING_START_FAILED", cause.Error())
	}
}

// beginDocumentGeneration runs after authorization and before changing source or
// parse state. Nested replacement/reparse calls share one durable generation.
func (s *knowledgeService) beginDocumentGeneration(
	ctx context.Context, knowledge *types.Knowledge, targetKB string,
) (context.Context, error) {
	r, ok := s.taskPendingRepo.(interfaces.DocumentGenerationRepository)
	if !ok || knowledge.IsManual() {
		return ctx, nil
	}
	if _, exists := types.DocumentGenerationFromContext(ctx, knowledge.TenantID, knowledge.ID); exists {
		return ctx, nil
	}
	token, err := r.BeginDocumentGeneration(ctx, knowledge.TenantID, knowledge.ID, knowledge.KnowledgeBaseID, targetKB)
	if err != nil {
		return ctx, err
	}
	if token == "" {
		return ctx, types.ErrDocumentGenerationSuperseded
	}
	return types.WithDocumentGeneration(ctx, knowledge.TenantID, knowledge.ID, token), nil
}

func (s *knowledgeService) updateDocumentGenerationColumns(
	ctx context.Context, tenantID uint64, knowledgeID string, columns map[string]any,
) error {
	if token, exists := types.DocumentGenerationFromContext(ctx, tenantID, knowledgeID); exists {
		if r, ok := s.taskPendingRepo.(interfaces.DocumentGenerationRepository); ok {
			updated, err := r.UpdateDocumentGeneration(ctx, tenantID, knowledgeID, token, columns)
			if err != nil {
				return err
			}
			if !updated {
				return types.ErrDocumentGenerationSuperseded
			}
			return nil
		}
	}
	if len(columns) == 1 {
		for column, value := range columns {
			return s.repo.UpdateKnowledgeColumn(ctx, knowledgeID, column, value)
		}
	}
	return s.repo.UpdateKnowledgeColumns(ctx, knowledgeID, columns)
}

func (s *knowledgeService) saveDocumentGeneration(ctx context.Context, knowledge *types.Knowledge) error {
	if token, exists := types.DocumentGenerationFromContext(ctx, knowledge.TenantID, knowledge.ID); exists {
		if r, ok := s.taskPendingRepo.(interfaces.DocumentGenerationRepository); ok {
			updated, err := r.SaveDocumentGeneration(ctx, token, knowledge)
			if err != nil {
				return err
			}
			if !updated {
				return types.ErrDocumentGenerationSuperseded
			}
			return nil
		}
	}
	return s.repo.UpdateKnowledge(ctx, knowledge)
}

func (s *knowledgeService) documentGenerationSuperseded(ctx context.Context, knowledgeID string) bool {
	tenantID, token, exists := types.DocumentGenerationOwnerFromContext(ctx, knowledgeID)
	if !exists {
		return false
	}
	r, ok := s.taskPendingRepo.(interfaces.DocumentGenerationRepository)
	if !ok {
		return false
	}
	current, err := r.CheckDocumentGeneration(ctx, tenantID, knowledgeID, token)
	if err != nil {
		if state, ok := ctx.Value(documentGenerationCheckKey{}).(*documentGenerationCheckState); ok {
			state.record(err)
		}
	}
	return err != nil || !current
}

// FailLegacyDocument fences the transport's terminal admission failure against
// modern reparses, including unversioned payloads whose Attempt is zero.
func (s *knowledgeService) FailLegacyDocument(ctx context.Context, task *asynq.Task, cause error) (bool, error) {
	r, ok := s.taskPendingRepo.(interfaces.DocumentGenerationRepository)
	if !ok {
		return false, nil
	}
	var payload types.DocumentProcessPayload
	if err := json.Unmarshal(task.Payload(), &payload); err != nil {
		return true, err
	}
	settled, err := r.FailLegacyDocumentGeneration(ctx, &payload, cause.Error())
	if err == nil && settled && payload.Attempt > 0 &&
		!attemptSuperseded(ctx, s.tracker(), payload.KnowledgeID, payload.Attempt) {
		s.tracker().FinalizeAttempt(ctx, payload.KnowledgeID, payload.Attempt,
			types.SpanStatusFailed, nil, "TASK_TIMEOUT", cause.Error())
	}
	return true, err
}
