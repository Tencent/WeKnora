package repository

import (
	"context"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// The DI container exposes *wikiPageRepository behind WikiPageRepository.
// These forwards make that same runtime value implement the optional
// WikiProvenanceRepository extension; no second repository needs to be wired
// into every service constructor.
func (r *wikiPageRepository) provenance() *wikiProvenanceRepository {
	return &wikiProvenanceRepository{db: r.db}
}

func (r *wikiPageRepository) SaveStagedBlockSet(ctx context.Context, set *types.WikiPageBlockSet) error {
	return r.provenance().SaveStagedBlockSet(ctx, set)
}

func (r *wikiPageRepository) MarkStagedBlockSetFailed(
	ctx context.Context, kbID, blockSetID string,
) error {
	return r.provenance().MarkStagedBlockSetFailed(ctx, kbID, blockSetID)
}

func (r *wikiPageRepository) GetBlockSet(
	ctx context.Context, kbID, blockSetID string,
) (*types.WikiPageBlockSet, error) {
	return r.provenance().GetBlockSet(ctx, kbID, blockSetID)
}

func (r *wikiPageRepository) GetCurrentBlockSet(
	ctx context.Context, kbID, pageID string,
) (*types.WikiPageBlockSet, error) {
	return r.provenance().GetCurrentBlockSet(ctx, kbID, pageID)
}

func (r *wikiPageRepository) ListBlockReferencesByKnowledge(
	ctx context.Context, kbID, knowledgeID string,
) ([]*types.WikiKnowledgeBlockReference, error) {
	return r.provenance().ListBlockReferencesByKnowledge(ctx, kbID, knowledgeID)
}

func (r *wikiPageRepository) CreatePageWithBlockSet(
	ctx context.Context, page *types.WikiPage, blockSetID string,
) error {
	return r.provenance().CreatePageWithBlockSet(ctx, page, blockSetID)
}

func (r *wikiPageRepository) PublishStagedBlockSet(
	ctx context.Context, page *types.WikiPage, blockSetID string,
) error {
	return r.provenance().PublishStagedBlockSet(ctx, page, blockSetID)
}

func (r *wikiPageRepository) CloneBlockSetToStaged(
	ctx context.Context, sourceBlockSetID string, target *types.WikiPageBlockSet,
) error {
	return r.provenance().CloneBlockSetToStaged(ctx, sourceBlockSetID, target)
}

func (r *wikiPageRepository) RemoveKnowledgeSourcesFromStaged(
	ctx context.Context, kbID, blockSetID, knowledgeID string,
) ([]string, error) {
	return r.provenance().RemoveKnowledgeSourcesFromStaged(ctx, kbID, blockSetID, knowledgeID)
}

func (r *wikiPageRepository) DeleteBlocksFromStaged(
	ctx context.Context, kbID, blockSetID string, blockIDs []string,
) error {
	return r.provenance().DeleteBlocksFromStaged(ctx, kbID, blockSetID, blockIDs)
}

func (r *wikiPageRepository) UpdateStagedBlockSetRender(
	ctx context.Context, kbID, blockSetID, content, summary string,
) error {
	return r.provenance().UpdateStagedBlockSetRender(ctx, kbID, blockSetID, content, summary)
}

func (r *wikiPageRepository) DeletePageIfVersion(
	ctx context.Context, kbID, pageID string, expectedVersion int,
) error {
	return r.provenance().DeletePageIfVersion(ctx, kbID, pageID, expectedVersion)
}

func (r *wikiPageRepository) DeleteBlockSetsByPage(ctx context.Context, pageID string) error {
	return r.provenance().DeleteBlockSetsByPage(ctx, pageID)
}

var _ interfaces.WikiProvenanceRepository = (*wikiPageRepository)(nil)
