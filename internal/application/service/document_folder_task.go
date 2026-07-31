package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/tracing/langfuse"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/google/uuid"
	"github.com/hibiken/asynq"
)

// SubmitDeleteFolderTree validates the current subtree and enqueues the
// potentially expensive cleanup on the maintenance queue.
func (s *documentFolderService) SubmitDeleteFolderTree(
	ctx context.Context,
	kbID string,
	tenantID uint64,
	id string,
	mode types.DocumentFolderDeleteMode,
) (string, error) {
	if !mode.IsValid() {
		return "", fmt.Errorf("unknown document folder delete mode %q", mode)
	}
	if s.task == nil {
		return "", fmt.Errorf("task queue is unavailable")
	}

	snapshot, err := s.loadDeleteSnapshot(ctx, s.repo, kbID, tenantID, id)
	if err != nil {
		return "", err
	}
	if mode == types.DocumentFolderDeleteModeKeepDocuments && snapshot.activeCount > 0 {
		return "", ErrFolderDocumentsProcessing
	}

	payload := types.DocumentFolderDeletePayload{
		TenantID:        tenantID,
		KnowledgeBaseID: kbID,
		FolderID:        id,
		Mode:            mode,
		Initiator:       types.TaskInitiatorFromContext(ctx),
	}
	langfuse.InjectTracing(ctx, &payload)
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal document folder delete task: %w", err)
	}

	taskID := uuid.NewString()
	task := asynq.NewTask(
		types.TypeDocumentFolderDelete,
		payloadBytes,
		asynq.TaskID(taskID),
		asynq.Queue(types.QueueMaintenance),
		asynq.MaxRetry(10),
		asynq.Timeout(2*time.Hour),
	)
	info, err := s.task.Enqueue(task)
	if err != nil {
		return "", fmt.Errorf("enqueue document folder delete task: %w", err)
	}
	if info != nil && info.ID != "" {
		return info.ID, nil
	}
	return taskID, nil
}

// ProcessDeleteFolderTree restores tenant and initiator context before running
// the idempotent deletion worker.
func (s *documentFolderService) ProcessDeleteFolderTree(ctx context.Context, task *asynq.Task) error {
	var payload types.DocumentFolderDeletePayload
	if err := json.Unmarshal(task.Payload(), &payload); err != nil {
		return fmt.Errorf("unmarshal document folder delete payload: %w", err)
	}
	if payload.TenantID == 0 || payload.KnowledgeBaseID == "" || payload.FolderID == "" || !payload.Mode.IsValid() {
		return fmt.Errorf("invalid document folder delete payload")
	}
	if s.tenantRepo == nil {
		return fmt.Errorf("tenant repository is unavailable")
	}

	tenant, err := s.tenantRepo.GetTenantByID(ctx, payload.TenantID)
	if err != nil {
		return fmt.Errorf("load tenant for document folder delete: %w", err)
	}
	ctx = context.WithValue(ctx, types.TenantIDContextKey, payload.TenantID)
	ctx = context.WithValue(ctx, types.TenantInfoContextKey, tenant)
	ctx = payload.Initiator.Apply(ctx)

	err = s.DeleteFolderTree(
		ctx,
		payload.KnowledgeBaseID,
		payload.TenantID,
		payload.FolderID,
		payload.Mode,
	)
	// If a worker completed and lost its acknowledgement, the replay sees no
	// folder. Treat that as success instead of exhausting retries.
	if errors.Is(err, repository.ErrDocumentFolderNotFound) {
		return nil
	}
	return err
}
