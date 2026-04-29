package interfaces

import (
	"context"

	"github.com/Tencent/WeKnora/internal/types"
)

// WikiPageService defines the wiki page service interface.
// Provides high-level operations for wiki page CRUD, link management,
// and chunk synchronization.
type WikiPageService interface {
	// CreatePage creates a new wiki page, parses outbound links, updates
	// bidirectional link references, and syncs to chunks for retrieval.
	CreatePage(ctx context.Context, page *types.WikiPage) (*types.WikiPage, error)

	// UpdatePage updates an existing wiki page, re-parses links, and updates
	// bidirectional references. The `version` field is incremented only when
	// a user-visible content field (title, content, summary, page_type,
	// status) actually differs from the stored value — bookkeeping-only
	// writes (e.g. refreshing source_refs after a same-content re-ingest) are
	// persisted without bumping the version.
	UpdatePage(ctx context.Context, page *types.WikiPage) (*types.WikiPage, error)

	// UpdatePageMeta updates only metadata (status, source_refs, etc.) without
	// incrementing version or re-parsing links. Use for publish/archive/source
	// ref changes driven by internal reconciliation.
	UpdatePageMeta(ctx context.Context, page *types.WikiPage) error

	// UpdateAutoLinkedContent persists content changes that come from
	// machine-only link decoration — cross-link injection and dead-link
	// cleanup — without bumping `version`. Out-links are re-parsed from the
	// new body and bidirectional in-link references on target pages are
	// refreshed. Conceptually the body still represents the same revision
	// the user saw before, just with wiki-link markup added or stripped, so
	// surfacing a version bump to end users is misleading.
	UpdateAutoLinkedContent(ctx context.Context, page *types.WikiPage) error

	// GetPageBySlug retrieves a wiki page by its slug within a knowledge base.
	GetPageBySlug(ctx context.Context, kbID string, slug string) (*types.WikiPage, error)

	// GetPageByID retrieves a wiki page by its unique ID.
	GetPageByID(ctx context.Context, id string) (*types.WikiPage, error)

	// ListPages lists wiki pages with optional filtering and pagination.
	ListPages(ctx context.Context, req *types.WikiPageListRequest) (*types.WikiPageListResponse, error)

	// DeletePage soft-deletes a wiki page and removes its chunk sync.
	DeletePage(ctx context.Context, kbID string, slug string) error

	// GetIndex returns the index page for a knowledge base.
	// Creates a default one if it doesn't exist.
	GetIndex(ctx context.Context, kbID string) (*types.WikiPage, error)

	// GetLog returns the log page for a knowledge base.
	// Creates a default one if it doesn't exist.
	GetLog(ctx context.Context, kbID string) (*types.WikiPage, error)

	// GetGraph returns the link graph data for visualization.
	GetGraph(ctx context.Context, kbID string) (*types.WikiGraphData, error)

	// GetStats returns aggregate statistics about the wiki.
	GetStats(ctx context.Context, kbID string) (*types.WikiStats, error)

	// RebuildLinks re-parses all pages and rebuilds bidirectional link references.
	RebuildLinks(ctx context.Context, kbID string) error

	// InjectCrossLinks scans specified pages and injects [[wiki-links]] for mentions
	// of other wiki page titles/aliases in the content.
	InjectCrossLinks(ctx context.Context, kbID string, affectedSlugs []string)

	// RebuildIndexPage regenerates the index page.
	RebuildIndexPage(ctx context.Context, kbID string) error

	// ListAllPages retrieves all wiki pages in a knowledge base without pagination.
	// Used for index rebuild, graph generation, cross-link injection, etc.
	ListAllPages(ctx context.Context, kbID string) ([]*types.WikiPage, error)

	// ListPagesBySourceRef retrieves all wiki pages whose source_refs reference
	// the given knowledge ID. Used by delete/ingest reconciliation paths that
	// need to find pages touched by a specific document at read time (rather
	// than relying on a caller-provided stale snapshot).
	ListPagesBySourceRef(ctx context.Context, kbID string, knowledgeID string) ([]*types.WikiPage, error)

	// SearchPages performs full-text search over wiki pages.
	SearchPages(ctx context.Context, kbID string, query string, limit int) ([]*types.WikiPage, error)

	// CreateIssue logs a new issue for a wiki page.
	CreateIssue(ctx context.Context, issue *types.WikiPageIssue) (*types.WikiPageIssue, error)

	// ListIssues retrieves issues for a knowledge base, optionally filtered by slug and status.
	ListIssues(ctx context.Context, kbID string, slug string, status string) ([]*types.WikiPageIssue, error)

	// UpdateIssueStatus updates the status of an issue (e.g. pending -> resolved/ignored).
	UpdateIssueStatus(ctx context.Context, issueID string, status string) error
}

// WikiPageRepository defines the wiki page data persistence interface.
type WikiPageRepository interface {
	// Create inserts a new wiki page record.
	Create(ctx context.Context, page *types.WikiPage) error

	// Update rewrites a wiki page record with optimistic locking and
	// unconditionally increments `version`. Callers are responsible for
	// deciding whether the edit is user-visible — the service layer uses
	// UpdateMeta for bookkeeping-only writes instead.
	Update(ctx context.Context, page *types.WikiPage) error

	// UpdateMeta updates bookkeeping / provenance fields (in/out links,
	// status, source_refs, chunk_refs, page_metadata, updated_at) without
	// touching `version`. Safe for link maintenance, re-ingest with an
	// unchanged body, and status-only transitions.
	UpdateMeta(ctx context.Context, page *types.WikiPage) error

	// UpdateAutoLinkedContent rewrites `content`, `out_links` and
	// `updated_at` in place while leaving `version` untouched. Intended for
	// machine-only link markup changes (cross-link injection / dead-link
	// cleanup) so the first-ingest page doesn't jump to v2 just because the
	// post-processor added a `[[...]]` wrapper.
	UpdateAutoLinkedContent(ctx context.Context, page *types.WikiPage) error

	// GetByID retrieves a wiki page by its unique ID.
	GetByID(ctx context.Context, id string) (*types.WikiPage, error)

	// GetBySlug retrieves a wiki page by slug within a knowledge base.
	GetBySlug(ctx context.Context, kbID string, slug string) (*types.WikiPage, error)

	// List retrieves wiki pages with filtering and pagination.
	List(ctx context.Context, req *types.WikiPageListRequest) ([]*types.WikiPage, int64, error)

	// ListByType retrieves all wiki pages of a given type within a knowledge base.
	ListByType(ctx context.Context, kbID string, pageType string) ([]*types.WikiPage, error)

	// ListBySourceRef retrieves all wiki pages that reference a given source knowledge ID.
	ListBySourceRef(ctx context.Context, kbID string, sourceKnowledgeID string) ([]*types.WikiPage, error)

	// ListAll retrieves all wiki pages in a knowledge base (for link rebuilding, graph generation).
	ListAll(ctx context.Context, kbID string) ([]*types.WikiPage, error)

	// ListRecentForSuggestions returns recent user-visible wiki pages under the given
	// knowledge bases, used to produce fallback suggested questions for Wiki-only KBs
	// that do not have AI-generated document questions or recommended FAQ entries.
	// Excludes index/log pages and archived pages. Returns up to `limit` rows sorted
	// by updated_at descending.
	ListRecentForSuggestions(ctx context.Context, tenantID uint64, kbIDs []string, limit int) ([]*types.WikiPage, error)

	// Delete soft-deletes a wiki page by knowledge base ID and slug.
	Delete(ctx context.Context, kbID string, slug string) error

	// DeleteByID soft-deletes a wiki page by ID.
	DeleteByID(ctx context.Context, id string) error

	// Search performs full-text search on wiki pages within a knowledge base.
	Search(ctx context.Context, kbID string, query string, limit int) ([]*types.WikiPage, error)

	// CountByType returns page counts grouped by type for a knowledge base.
	CountByType(ctx context.Context, kbID string) (map[string]int64, error)

	// CountOrphans returns the number of pages with no inbound links.
	CountOrphans(ctx context.Context, kbID string) (int64, error)

	// CreateIssue inserts a new wiki page issue record.
	CreateIssue(ctx context.Context, issue *types.WikiPageIssue) error

	// ListIssues retrieves issues with optional filtering by slug and status.
	ListIssues(ctx context.Context, kbID string, slug string, status string) ([]*types.WikiPageIssue, error)

	// UpdateIssueStatus updates an issue's status.
	UpdateIssueStatus(ctx context.Context, issueID string, status string) error
}
