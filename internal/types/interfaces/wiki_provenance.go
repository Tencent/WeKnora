package interfaces

import (
	"context"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
)

// WikiProvenanceRepository persists immutable Wiki revisions and their source
// ledger. Every method is explicitly tenant and knowledge-base scoped. The
// repository passed to WithTransaction is bound to that database transaction.
type WikiProvenanceRepository interface {
	WithTransaction(ctx context.Context, fn func(WikiProvenanceRepository) error) error
	GetPageProvenance(ctx context.Context, tenantID uint64, kbID, pageID string) (*types.WikiPageProvenanceResponse, error)
	EnsureCurrentPage(ctx context.Context, page *types.WikiPage) error
	LockPublishScope(ctx context.Context, tenantID uint64, kbID, pageID string, knowledgeIDs []string) error

	FindPageRevisionByPublishKey(ctx context.Context, tenantID uint64, kbID, pageID, publishKey string) (*types.WikiProvenancePageRevision, error)
	GetKnowledgeRevision(ctx context.Context, tenantID uint64, kbID, revisionID string) (*types.KnowledgeRevision, error)
	FindKnowledgeRevisionByContentHash(ctx context.Context, tenantID uint64, kbID, knowledgeID, contentHash string, parseAttempt int) (*types.KnowledgeRevision, error)
	NextPageRevisionNo(ctx context.Context, tenantID uint64, kbID, pageID string) (int, error)
	NextKnowledgeRevisionNo(ctx context.Context, tenantID uint64, kbID, knowledgeID string) (int, error)

	CreateKnowledgeRevision(ctx context.Context, revision *types.KnowledgeRevision) error
	CreatePageRevision(ctx context.Context, revision *types.WikiProvenancePageRevision) error
	CreateBlocks(ctx context.Context, blocks []types.WikiPageBlock) error
	CreateBlockSources(ctx context.Context, sources []types.WikiBlockSource) error
	ReplacePageSources(ctx context.Context, tenantID uint64, kbID, pageID string, sources []types.WikiPageSource) error

	PublishKnowledgeRevision(ctx context.Context, tenantID uint64, kbID, knowledgeID, revisionID string, at time.Time) error
	PublishPageRevision(ctx context.Context, tenantID uint64, kbID, pageID, revisionID string, at time.Time) error
	UpdateCurrentPage(ctx context.Context, tenantID uint64, kbID string, page *types.WikiPage, revision *types.WikiProvenancePageRevision, at time.Time) error
}

// WikiProvenancePublishService validates and publishes a complete page/source
// snapshot in one database transaction.
type WikiProvenancePublishService interface {
	Publish(ctx context.Context, request *types.WikiProvenancePublishRequest) (*types.WikiProvenancePublishResult, error)
}

// WikiProvenanceQueryService exposes the current, tenant-scoped block/source
// projection used by the Wiki reader.
type WikiProvenanceQueryService interface {
	GetPageProvenance(ctx context.Context, tenantID uint64, kbID, pageID string) (*types.WikiPageProvenanceResponse, error)
}

// WikiProvenanceLifecycleRepository owns source-impact lookup and the atomic
// removal of a deleted knowledge's provenance rows.
type WikiProvenanceLifecycleRepository interface {
	ListKnowledgePageImpacts(ctx context.Context, tenantID uint64, kbID, knowledgeID string) ([]types.WikiKnowledgePageImpact, error)
	DeleteKnowledgeSources(ctx context.Context, tenantID uint64, kbID, knowledgeID string, at time.Time) (*types.WikiKnowledgeSourceCleanupResult, error)
}

// WikiProvenanceLifecycleService is the application boundary used by file
// update/delete and Wiki ingest flows. Callers never inspect legacy
// wiki_pages.source_refs to determine impact.
type WikiProvenanceLifecycleService interface {
	ListKnowledgePageImpacts(ctx context.Context, tenantID uint64, kbID, knowledgeID string) ([]types.WikiKnowledgePageImpact, error)
	DeleteKnowledgeSources(ctx context.Context, tenantID uint64, kbID, knowledgeID string, at time.Time) (*types.WikiKnowledgeSourceCleanupResult, error)
}
