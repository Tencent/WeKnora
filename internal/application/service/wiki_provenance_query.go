package service

import (
	"context"
	"errors"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

type wikiProvenanceQueryService struct {
	repo interfaces.WikiProvenanceRepository
}

func NewWikiProvenanceQueryService(
	repo interfaces.WikiProvenanceRepository,
) interfaces.WikiProvenanceQueryService {
	return &wikiProvenanceQueryService{repo: repo}
}

func (s *wikiProvenanceQueryService) GetPageProvenance(
	ctx context.Context,
	tenantID uint64,
	kbID, pageID string,
) (*types.WikiPageProvenanceResponse, error) {
	if s.repo == nil {
		return nil, errors.New("wiki provenance repository is not configured")
	}
	return s.repo.GetPageProvenance(ctx, tenantID, kbID, pageID)
}

var _ interfaces.WikiProvenanceQueryService = (*wikiProvenanceQueryService)(nil)
