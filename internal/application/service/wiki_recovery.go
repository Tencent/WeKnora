package service

import (
	"context"
	"fmt"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// WikiRecoveryService reuses the ordinary Wiki worker for operator-requested
// recovery. Durable pending operations survive a trigger or process failure.
type WikiRecoveryService struct {
	repo repository.WikiRecoveryRepository
	task interfaces.TaskEnqueuer
}

// NewWikiRecoveryService connects durable recovery to the Wiki scheduler.
func NewWikiRecoveryService(repo repository.WikiRecoveryRepository, task interfaces.TaskEnqueuer) *WikiRecoveryService {
	return &WikiRecoveryService{repo: repo, task: task}
}

// ListDocuments returns separate source and Wiki results for one knowledge base.
func (s *WikiRecoveryService) ListDocuments(
	ctx context.Context, tenantID uint64, kbID, status string, page, size int,
) (*types.WikiDocumentList, error) {
	return s.repo.ListDocuments(ctx, tenantID, kbID, status, page, size)
}

// RetryDocuments restores failures and schedules their existing Wiki worker.
func (s *WikiRecoveryService) RetryDocuments(
	ctx context.Context, tenantID uint64, kbID string, ids []string,
) (int, error) {
	n, err := s.repo.RetryDocuments(ctx, tenantID, kbID, ids)
	if err != nil {
		return 0, err
	}
	// Also trigger on repeated requests: the previous request may have committed
	// the pending operation but failed to reach the scheduler.
	if err := enqueueWikiIngestTrigger(ctx, s.task, tenantID, kbID); err != nil {
		return n, fmt.Errorf("wiki operations are saved; worker scheduling failed, retry this request: %w", err)
	}
	return n, nil
}
