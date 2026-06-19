package service

import (
	"context"
	"encoding/json"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/tracing/langfuse"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/google/uuid"
	"github.com/hibiken/asynq"
)

func (s *knowledgeService) dispatchDocumentRootTask(
	ctx context.Context,
	knowledge *types.Knowledge,
	payload types.DocumentProcessPayload,
	kind types.TaskJobKind,
	sourceType string,
	opts ...asynq.Option,
) (*asynq.TaskInfo, error) {
	if payload.Attempt <= 0 {
		if root, n, err := s.tracker().OpenAttempt(ctx, knowledge.ID, payload.LangfuseTraceID); err == nil && root != nil {
			payload.Attempt = n
		} else if err != nil {
			logger.Warnf(ctx, "task ledger: OpenAttempt failed for %s: %v", knowledge.ID, err)
		}
	}
	if payload.Attempt <= 0 {
		payload.Attempt = s.tracker().LatestAttempt(ctx, knowledge.ID) + 1
		if payload.Attempt <= 0 {
			payload.Attempt = 1
		}
	}

	langfuse.InjectTracing(ctx, &payload)
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	task := asynq.NewTask(types.TypeDocumentProcess, payloadBytes)

	if s.taskDispatcher == nil {
		return s.task.Enqueue(task, opts...)
	}

	jobID := uuid.NewString()
	executionID := uuid.NewString()
	metadata := taskJobJSON(map[string]any{
		"file_name":         payload.FileName,
		"file_type":         payload.FileType,
		"knowledge_id":      knowledge.ID,
		"knowledge_base_id": knowledge.KnowledgeBaseID,
		"source_type":       sourceType,
	})
	replaySpec := taskJobJSON(map[string]any{
		"version": 1,
		"kind":    kind,
		"source_ref": map[string]any{
			"type": sourceType,
			"id":   documentReplaySourceID(payload, knowledge),
		},
		"scope": map[string]any{
			"knowledge_id":      knowledge.ID,
			"knowledge_base_id": knowledge.KnowledgeBaseID,
		},
	})

	createdBy := ""
	if userID, ok := types.UserIDFromContext(ctx); ok && !types.IsSyntheticUserID(userID) {
		createdBy = userID
	}

	return s.taskDispatcher.DispatchUserRoot(ctx, UserRootDispatchRequest{
		JobID:          jobID,
		ExecutionID:    executionID,
		TenantID:       knowledge.TenantID,
		CreatedBy:      createdBy,
		Kind:           kind,
		Scope:          types.TaskScopeKnowledge,
		ScopeID:        knowledge.ID,
		RelatedID:      knowledge.KnowledgeBaseID,
		ProcessAttempt: payload.Attempt,
		DisplayName:    taskJobDisplayName(knowledge, payload),
		Metadata:       metadata,
		ReplaySpec:     replaySpec,
		Task:           task,
		Options:        opts,
	})
}

func taskJobJSON(v any) types.JSON {
	b, err := json.Marshal(v)
	if err != nil || len(b) == 0 {
		return types.JSON(`{}`)
	}
	return types.JSON(b)
}

func taskJobDisplayName(knowledge *types.Knowledge, payload types.DocumentProcessPayload) string {
	switch {
	case payload.FileName != "":
		return payload.FileName
	case knowledge.Title != "":
		return knowledge.Title
	case payload.URL != "":
		return payload.URL
	case payload.FileURL != "":
		return payload.FileURL
	default:
		return knowledge.ID
	}
}

func documentReplaySourceID(payload types.DocumentProcessPayload, knowledge *types.Knowledge) string {
	switch {
	case payload.FilePath != "":
		return payload.FilePath
	case payload.FileURL != "":
		return payload.FileURL
	case payload.URL != "":
		return payload.URL
	default:
		return knowledge.ID
	}
}

func markDocumentJobSucceeded(ctx context.Context, repo interfaces.TaskJobRepository, tenantID uint64, knowledgeID string, attempt int) {
	if repo == nil || attempt <= 0 {
		return
	}
	_, _ = repo.MarkJobSucceededIfCurrentAttempt(ctx, interfaces.TaskJobAttemptSelector{
		TenantID:       tenantID,
		Scope:          types.TaskScopeKnowledge,
		ScopeID:        knowledgeID,
		ProcessAttempt: attempt,
	}, time.Now())
}

func (s *knowledgeService) syncDocumentJobFromKnowledgeStatus(ctx context.Context, tenantID uint64, knowledgeID string, attempt int) {
	if s == nil || s.taskJobRepo == nil || s.repo == nil || attempt <= 0 || knowledgeID == "" {
		return
	}
	dctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	knowledge, err := s.repo.GetKnowledgeByID(dctx, tenantID, knowledgeID)
	if err != nil || knowledge == nil {
		return
	}
	sel := interfaces.TaskJobAttemptSelector{
		TenantID:       tenantID,
		Scope:          types.TaskScopeKnowledge,
		ScopeID:        knowledgeID,
		ProcessAttempt: attempt,
	}
	now := time.Now()
	switch knowledge.ParseStatus {
	case types.ParseStatusProcessing:
		_, _ = s.taskJobRepo.MarkJobProcessingIfCurrentAttempt(dctx, sel)
	case types.ParseStatusFinalizing:
		_, _ = s.taskJobRepo.MarkJobFinalizingIfCurrentAttempt(dctx, sel)
	case types.ParseStatusCompleted:
		_, _ = s.taskJobRepo.MarkJobSucceededIfCurrentAttempt(dctx, sel, now)
	case types.ParseStatusFailed:
		_, _ = s.taskJobRepo.MarkJobFailedIfCurrentAttempt(dctx, sel, interfaces.TaskLedgerFailure{
			ErrorClass:     types.TaskErrorClassTerminal,
			LastError:      knowledge.ErrorMessage,
			FailedTaskType: types.TypeDocumentProcess,
		}, now)
	case types.ParseStatusCancelled:
		_, _ = s.taskJobRepo.MarkJobCanceledIfCurrentAttempt(dctx, sel, interfaces.TaskLedgerFailure{
			ErrorClass:     types.TaskErrorClassCanceled,
			LastError:      knowledge.ErrorMessage,
			FailedTaskType: types.TypeDocumentProcess,
		}, now)
	}
}
