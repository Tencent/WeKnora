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
	//
	// The index row now carries only the LLM-generated intro in its content
	// column — the heavy directory listing was moved out of wiki_pages.
	// Callers that want a renderable directory should use GetIndexView,
	// which assembles {intro, groups} structured output on demand.
	GetIndex(ctx context.Context, kbID string) (*types.WikiPage, error)

	// GetIndexView returns the structured index response — intro (from the
	// index row) plus a paginated window of directory entries per requested
	// page_type. `types` restricts which page types to include (empty =
	// all), `limit` bounds the per-type window size, `cursor` is an opaque
	// offset string resumed from a previous response.
	//
	// Keeping the directory as JSON rather than a multi-MB markdown blob
	// means a 40k-page KB no longer pays O(N) transport + rendering cost
	// on every index open. See /wiki/index handler + WikiBrowser.vue for
	// the consumer.
	GetIndexView(ctx context.Context, kbID string, pageTypes []string, limit int, cursor string) (*types.WikiIndexResponse, error)

	// GetLog returns the log page for a knowledge base.
	//
	// Wiki operation events now live in the dedicated wiki_log_entries
	// table, so this method no longer auto-creates a placeholder row on
	// miss and may legitimately return (nil, nil) for KBs that never had
	// the legacy row written. Retained for back-compat with callers that
	// still probe the row (lint, knowledge delete); new code should use
	// WikiLogEntryService.List for the event feed instead.
	GetLog(ctx context.Context, kbID string) (*types.WikiPage, error)

	// GetGraph returns the link graph data for visualization. The caller
	// supplies a WikiGraphRequest describing the desired slice of the graph
	// (overview top-N or ego neighborhood around a center slug). Callers
	// that need the full graph (e.g. wiki lint) can set Limit <= 0 to
	// disable the node cap; the HTTP handler always clamps Limit to a
	// safe range before invoking the service.
	GetGraph(ctx context.Context, req *types.WikiGraphRequest) (*types.WikiGraphData, error)

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

	// ListByType returns every wiki page of the given type. Used by
	// callers that need the full set for a specific type (e.g. intro
	// regeneration needs summary pages). For directory rendering, use
	// GetIndexView instead — it paginates via ListByTypeLight.
	ListByType(ctx context.Context, kbID string, pageType string) ([]*types.WikiPage, error)

	// ListPagesBySourceRef retrieves all wiki pages whose source_refs reference
	// the given knowledge ID. Used by delete/ingest reconciliation paths that
	// need to find pages touched by a specific document at read time (rather
	// than relying on a caller-provided stale snapshot).
	ListPagesBySourceRef(ctx context.Context, kbID string, knowledgeID string) ([]*types.WikiPage, error)

	// ListSlugsBySourceRef returns just the slugs of pages that cite the
	// given knowledge id. Cheaper than ListPagesBySourceRef when callers
	// only need the slug set (e.g. wiki ingest's "before" snapshot).
	ListSlugsBySourceRef(ctx context.Context, kbID string, knowledgeID string) ([]string, error)

	// ListBySlugs is the lazy fetcher used by the wiki ingest batch
	// context: returns lightweight projections (no content / source_refs)
	// for the requested slugs in a single IN query. Used in place of
	// the pre-batch ListAllPages dump.
	ListBySlugs(ctx context.Context, kbID string, slugs []string) (map[string]*types.WikiPageLite, error)

	// ListSummariesByKnowledgeIDs returns summary-page content keyed by
	// the knowledge id that authored it. Used by the retract / reparse
	// branches of reduceSlugUpdates for "what was this doc's
	// contribution?" framing.
	ListSummariesByKnowledgeIDs(ctx context.Context, kbID string, kids []string) (map[string]string, error)

	// ExistsSlugs reports which of the given slugs are live (non-archived).
	// Used by cleanDeadLinks to validate out-link targets cheaply.
	ExistsSlugs(ctx context.Context, kbID string, slugs []string) (map[string]bool, error)

	// ListAllSlugs returns every non-archived slug in the KB. Used by
	// lint to compute the live-slug set for broken-link detection.
	ListAllSlugs(ctx context.Context, kbID string) ([]string, error)

	// ListPagesCursor walks the KB in id-asc order with an opaque
	// cursor. Used by lint to stream the page set without holding it
	// all in memory at once.
	ListPagesCursor(ctx context.Context, kbID string, cursor string, limit int) ([]*types.WikiPage, string, error)

	// ListByTypeRecent returns the most-recently-updated pages of the
	// given type, projected to slug/title/summary, capped at `limit`.
	// Used by rebuildIndexPage's first-time generation path.
	ListByTypeRecent(ctx context.Context, kbID string, pageType string, limit int) ([]types.WikiIndexEntry, error)

	// FindSimilarPages performs a pg_trgm similarity search over
	// page titles. Used by the dedup pre-filter to surface candidate
	// merge targets server-side.
	FindSimilarPages(ctx context.Context, kbID string, query string, pageTypes []string, limit int) ([]*types.WikiPageLite, error)

	// CountByType returns page counts grouped by type for a knowledge
	// base. Re-exposed at the service layer so the index intro
	// generation path can frame the LLM prompt with "showing N of M"
	// metadata when the recent-N projection doesn't cover the full
	// summary set.
	CountByType(ctx context.Context, kbID string) (map[string]int64, error)

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

	// ListByTypeLight returns a paginated window of lightweight entries
	// (slug/title/summary only) for the given page_type plus the total
	// non-archived count. Used by the structured index API so reads do not
	// have to materialize TEXT content for every wiki_pages row.
	ListByTypeLight(ctx context.Context, kbID string, pageType string, limit int, offset int) ([]types.WikiIndexEntry, int64, error)

	// ListBySourceRef retrieves all wiki pages that reference a given source knowledge ID.
	ListBySourceRef(ctx context.Context, kbID string, sourceKnowledgeID string) ([]*types.WikiPage, error)

	// ListSlugsBySourceRef returns just the slugs of pages that reference
	// the given knowledge id. Same predicate as ListBySourceRef but
	// projected to a single column for the wiki ingest pipeline's
	// "before" snapshot path.
	ListSlugsBySourceRef(ctx context.Context, kbID string, sourceKnowledgeID string) ([]string, error)

	// ListBySlugs returns lightweight projections (slug/title/page_type/
	// status/aliases/out_links) for the given slugs in one IN query.
	// Used by the lazy fetcher path in wiki ingest's Map/Reduce phases.
	ListBySlugs(ctx context.Context, kbID string, slugs []string) (map[string]*types.WikiPageLite, error)

	// ListSummariesByKnowledgeIDs returns summary-page content keyed by
	// the knowledge id that authored it. Used by the retract branch of
	// reduceSlugUpdates for "what was this doc's contribution?" framing.
	ListSummariesByKnowledgeIDs(ctx context.Context, kbID string, kids []string) (map[string]string, error)

	// ExistsSlugs reports which of the given slugs are live (non-
	// archived). Used by cleanDeadLinks to validate out-link targets
	// without loading the referenced pages' content.
	ExistsSlugs(ctx context.Context, kbID string, slugs []string) (map[string]bool, error)

	// ListAllSlugs returns every non-archived slug in the KB. Used by
	// lint to compute the live-slug set without paying for ListAll's
	// full row materialization.
	ListAllSlugs(ctx context.Context, kbID string) ([]string, error)

	// ListPagesCursor returns up to `limit` pages ordered by id ASC,
	// paginated by an opaque numeric cursor. Used by lint to stream
	// the KB without loading every page at once.
	ListPagesCursor(ctx context.Context, kbID string, cursor string, limit int) ([]*types.WikiPage, string, error)

	// ListByTypeRecent returns the most-recently-updated pages of the
	// given type, projected to slug/title/summary, capped at `limit`.
	// Used by rebuildIndexPage's first-time generation path so the
	// LLM prompt size is bounded on large KBs.
	ListByTypeRecent(ctx context.Context, kbID string, pageType string, limit int) ([]types.WikiIndexEntry, error)

	// FindSimilarPages returns the top-k pages whose lowercase title
	// is most similar to the query under pg_trgm. `pageTypes` empty
	// defaults to entity+concept. Used by the dedup pre-filter to
	// surface candidate merge targets server-side.
	FindSimilarPages(ctx context.Context, kbID string, query string, pageTypes []string, limit int) ([]*types.WikiPageLite, error)

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
