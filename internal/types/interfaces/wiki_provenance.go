package interfaces

import (
	"context"

	"github.com/Tencent/WeKnora/internal/types"
)

// WikiProvenanceRepository persists immutable block sets independently from
// the legacy WikiPageRepository. Keeping this surface separate lets existing
// page CRUD remain compatible while provenance-aware ingest adopts it
// incrementally.
type WikiProvenanceRepository interface {
	// SaveStagedBlockSet atomically inserts a complete staged set, including
	// its blocks and source evidence.
	SaveStagedBlockSet(ctx context.Context, set *types.WikiPageBlockSet) error
	// MarkStagedBlockSetFailed atomically changes only a staged candidate to
	// failed. Published, superseded, failed, missing, and cross-KB sets are not
	// modified.
	MarkStagedBlockSetFailed(ctx context.Context, kbID, blockSetID string) error
	// GetBlockSet returns one set with blocks and sources in display order.
	GetBlockSet(ctx context.Context, kbID, blockSetID string) (*types.WikiPageBlockSet, error)
	// GetCurrentBlockSet resolves the current pointer on a page and returns its
	// complete set. Legacy pages with an empty pointer return not found.
	GetCurrentBlockSet(ctx context.Context, kbID, pageID string) (*types.WikiPageBlockSet, error)
	// ListBlockReferencesByKnowledge finds only current published blocks that
	// reference the document; historical/superseded sets are excluded.
	ListBlockReferencesByKnowledge(
		ctx context.Context, kbID, knowledgeID string,
	) ([]*types.WikiKnowledgeBlockReference, error)
	// CreatePageWithBlockSet atomically creates a page and publishes a staged
	// version-1 set as its current structured representation. Publication is
	// rejected when any tracked source no longer belongs to its latest parse
	// attempt, allowing the ingest caller to rebuild and retry.
	CreatePageWithBlockSet(ctx context.Context, page *types.WikiPage, blockSetID string) error
	// PublishStagedBlockSet atomically snapshots the current page, applies the
	// optimistic page update, supersedes its old set, and publishes the staged
	// replacement. Its final pointer switch is guarded by the latest parse
	// attempt of every tracked source.
	PublishStagedBlockSet(ctx context.Context, page *types.WikiPage, blockSetID string) error
	// CloneBlockSetToStaged copies an immutable set to target using fresh row
	// IDs while retaining stable LogicalBlockIDs.
	CloneBlockSetToStaged(
		ctx context.Context, sourceBlockSetID string, target *types.WikiPageBlockSet,
	) error
	// RemoveKnowledgeSourcesFromStaged removes one document's evidence from a
	// staged set and returns affected blocks that now have no sources. It never
	// mutates a published set or decides whether an orphan block should remain.
	RemoveKnowledgeSourcesFromStaged(
		ctx context.Context, kbID, blockSetID, knowledgeID string,
	) ([]string, error)
	// DeleteBlocksFromStaged removes selected blocks and their evidence from a
	// staged set after the service has made the content-level decision.
	DeleteBlocksFromStaged(ctx context.Context, kbID, blockSetID string, blockIDs []string) error
	// UpdateStagedBlockSetRender refreshes the compatibility Markdown cache
	// after block-level edits and before publication.
	UpdateStagedBlockSetRender(ctx context.Context, kbID, blockSetID, content, summary string) error
	// DeletePageIfVersion atomically soft-deletes exactly the page revision
	// inspected by deterministic source cleanup together with its revisions,
	// provenance block sets and synchronized retrieval chunk. A concurrent
	// publish changes version and must surface a conflict instead of deleting
	// the newly published page.
	DeletePageIfVersion(ctx context.Context, kbID, pageID string, expectedVersion int) error
	// DeleteBlockSetsByPage hard-deletes all sets, blocks and sources belonging
	// to a page when provenance needs explicit administrative cleanup.
	DeleteBlockSetsByPage(ctx context.Context, pageID string) error
}
