package service

import (
	"context"
	"errors"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

type wikiProvenanceLifecycleService struct {
	repo interfaces.WikiProvenanceLifecycleRepository
}

func NewWikiProvenanceLifecycleService(
	repo interfaces.WikiProvenanceLifecycleRepository,
) interfaces.WikiProvenanceLifecycleService {
	return &wikiProvenanceLifecycleService{repo: repo}
}

func (s *wikiProvenanceLifecycleService) ListKnowledgePageImpacts(
	ctx context.Context,
	tenantID uint64,
	kbID, knowledgeID string,
) ([]types.WikiKnowledgePageImpact, error) {
	if s.repo == nil {
		return nil, errors.New("wiki provenance lifecycle repository is not configured")
	}
	return s.repo.ListKnowledgePageImpacts(ctx, tenantID, kbID, knowledgeID)
}

func (s *wikiProvenanceLifecycleService) DeleteKnowledgeSources(
	ctx context.Context,
	tenantID uint64,
	kbID, knowledgeID string,
	at time.Time,
) (*types.WikiKnowledgeSourceCleanupResult, error) {
	if s.repo == nil {
		return nil, errors.New("wiki provenance lifecycle repository is not configured")
	}
	return s.repo.DeleteKnowledgeSources(ctx, tenantID, kbID, knowledgeID, at)
}

var _ interfaces.WikiProvenanceLifecycleService = (*wikiProvenanceLifecycleService)(nil)
