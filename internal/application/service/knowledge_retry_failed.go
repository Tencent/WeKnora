package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/Tencent/WeKnora/internal/application/repository"
	werrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/hibiken/asynq"
	"github.com/redis/go-redis/v9"
)

const (
	knowledgeRetryFailedProgressKeyPrefix = "knowledge_retry_failed_progress:"
	knowledgeRetryFailedProgressTTL       = 24 * time.Hour
)

func getKnowledgeRetryFailedProgressKey(taskID string) string {
	return knowledgeRetryFailedProgressKeyPrefix + taskID
}

func (s *knowledgeService) saveKnowledgeRetryFailedProgress(ctx context.Context, progress *types.KnowledgeRetryFailedProgress) error {
	if progress == nil {
		return nil
	}
	if s.redisClient == nil {
		copied := *progress
		s.memRetryFailedProgress.Store(progress.TaskID, &copied)
		return nil
	}
	key := getKnowledgeRetryFailedProgressKey(progress.TaskID)
	data, err := json.Marshal(progress)
	if err != nil {
		return fmt.Errorf("failed to marshal retry failed progress: %w", err)
	}
	return s.redisClient.Set(ctx, key, data, knowledgeRetryFailedProgressTTL).Err()
}

// SaveKnowledgeRetryFailedProgress saves retry submission progress.
func (s *knowledgeService) SaveKnowledgeRetryFailedProgress(ctx context.Context, progress *types.KnowledgeRetryFailedProgress) error {
	return s.saveKnowledgeRetryFailedProgress(ctx, progress)
}

// GetKnowledgeRetryFailedProgress retrieves retry submission progress.
func (s *knowledgeService) GetKnowledgeRetryFailedProgress(ctx context.Context, taskID string) (*types.KnowledgeRetryFailedProgress, error) {
	if s.redisClient == nil {
		if v, ok := s.memRetryFailedProgress.Load(taskID); ok {
			copied := *(v.(*types.KnowledgeRetryFailedProgress))
			return &copied, nil
		}
		return nil, werrors.NewNotFoundError("Retry failed documents task not found")
	}
	key := getKnowledgeRetryFailedProgressKey(taskID)
	data, err := s.redisClient.Get(ctx, key).Bytes()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return nil, werrors.NewNotFoundError("Retry failed documents task not found")
		}
		return nil, fmt.Errorf("failed to get retry failed progress from Redis: %w", err)
	}

	var progress types.KnowledgeRetryFailedProgress
	if err := json.Unmarshal(data, &progress); err != nil {
		return nil, fmt.Errorf("failed to unmarshal retry failed progress: %w", err)
	}
	return &progress, nil
}

// ProcessKnowledgeRetryFailed submits single-knowledge reparse jobs for the
// originally failed documents that are still failed when the worker reaches
// them. The progress is submission progress, not final parse completion.
func (s *knowledgeService) ProcessKnowledgeRetryFailed(ctx context.Context, t *asynq.Task) error {
	var payload types.KnowledgeRetryFailedPayload
	if err := json.Unmarshal(t.Payload(), &payload); err != nil {
		return fmt.Errorf("failed to unmarshal retry failed payload: %w", err)
	}

	ctx = context.WithValue(ctx, types.TenantIDContextKey, payload.TenantID)
	if s.tenantRepo != nil {
		if tenantInfo, err := s.tenantRepo.GetTenantByID(ctx, payload.TenantID); err == nil && tenantInfo != nil {
			ctx = context.WithValue(ctx, types.TenantInfoContextKey, tenantInfo)
		} else if err != nil {
			logger.Warnf(ctx, "ProcessKnowledgeRetryFailed: failed to get tenant info: %v", err)
		}
	}

	now := time.Now().Unix()
	progress := &types.KnowledgeRetryFailedProgress{
		TaskID:    payload.TaskID,
		KBID:      payload.KBID,
		Status:    types.KBCloneStatusProcessing,
		Total:     len(payload.KnowledgeIDs),
		Message:   "Submitting retry tasks...",
		CreatedAt: now,
		UpdatedAt: now,
	}
	if existing, err := s.GetKnowledgeRetryFailedProgress(ctx, payload.TaskID); err == nil && existing != nil {
		progress.CreatedAt = existing.CreatedAt
	}
	_ = s.saveKnowledgeRetryFailedProgress(ctx, progress)

	for _, knowledgeID := range payload.KnowledgeIDs {
		knowledge, err := s.GetKnowledgeByID(ctx, knowledgeID)
		if err != nil && !isKnowledgeRetrySkippableLoadError(err) {
			logger.Warnf(ctx, "ProcessKnowledgeRetryFailed: failed to load knowledge %s: %v", knowledgeID, err)
			progress.Failed++
			progress.Message = "Failed to load one document before retry submission"
			s.updateRetryFailedProgress(ctx, progress)
			continue
		}
		if skip, message := shouldSkipRetryFailedKnowledge(knowledge, payload.KBID, err); skip {
			progress.Skipped++
			progress.Message = message
			s.updateRetryFailedProgress(ctx, progress)
			continue
		}

		if _, err := s.ReparseKnowledge(ctx, knowledgeID, nil); err != nil {
			progress.Failed++
			progress.Message = "Failed to submit one retry task"
			progress.Error = err.Error()
		} else {
			progress.Processed++
			progress.Message = "Retry task submitted"
		}
		s.updateRetryFailedProgress(ctx, progress)
	}

	progress.Status = types.KBCloneStatusCompleted
	progress.Progress = 100
	progress.Message = fmt.Sprintf("Retry submission completed: %d submitted, %d skipped, %d failed",
		progress.Processed, progress.Skipped, progress.Failed)
	progress.UpdatedAt = time.Now().Unix()
	if err := s.saveKnowledgeRetryFailedProgress(ctx, progress); err != nil {
		logger.Warnf(ctx, "ProcessKnowledgeRetryFailed: failed to save completed progress: %v", err)
	}
	return nil
}

func isKnowledgeRetrySkippableLoadError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, repository.ErrKnowledgeNotFound) {
		return true
	}
	var appErr *werrors.AppError
	return errors.As(err, &appErr) && appErr.Code == werrors.ErrNotFound
}

func shouldSkipRetryFailedKnowledge(knowledge *types.Knowledge, kbID string, loadErr error) (bool, string) {
	if loadErr != nil || knowledge == nil {
		return true, "Skipped document that no longer exists or is inaccessible"
	}
	if knowledge.KnowledgeBaseID != kbID {
		return true, "Skipped document that no longer belongs to this knowledge base"
	}
	if knowledge.ParseStatus != types.ParseStatusFailed {
		return true, "Skipped document that is no longer failed"
	}
	return false, ""
}

func (s *knowledgeService) updateRetryFailedProgress(ctx context.Context, progress *types.KnowledgeRetryFailedProgress) {
	done := progress.Processed + progress.Failed + progress.Skipped
	if progress.Total > 0 {
		progress.Progress = done * 100 / progress.Total
	}
	progress.UpdatedAt = time.Now().Unix()
	if err := s.saveKnowledgeRetryFailedProgress(ctx, progress); err != nil {
		logger.Warnf(ctx, "ProcessKnowledgeRetryFailed: failed to save progress: %v", err)
	}
}
