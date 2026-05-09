package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"gorm.io/gorm"
)

// ErrWikiPageNotFound is returned when a wiki page is not found
var ErrWikiPageNotFound = errors.New("wiki page not found")

// ErrWikiPageConflict is returned when an optimistic lock conflict is detected
var ErrWikiPageConflict = errors.New("wiki page version conflict")

// wikiPageRepository implements the WikiPageRepository interface
type wikiPageRepository struct {
	db *gorm.DB
}

// NewWikiPageRepository creates a new wiki page repository
func NewWikiPageRepository(db *gorm.DB) interfaces.WikiPageRepository {
	return &wikiPageRepository{db: db}
}

// Create inserts a new wiki page record
func (r *wikiPageRepository) Create(ctx context.Context, page *types.WikiPage) error {
	return r.db.WithContext(ctx).Create(page).Error
}

// Update updates an existing wiki page record with optimistic locking.
// Increments version — use only for content changes visible to the user.
// The caller must set page.Version to the expected current version.
func (r *wikiPageRepository) Update(ctx context.Context, page *types.WikiPage) error {
	expectedVersion := page.Version
	page.Version = expectedVersion + 1

	result := r.db.WithContext(ctx).
		Model(page).
		Where("id = ? AND version = ?", page.ID, expectedVersion).
		Updates(page)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		// Could be not found or version conflict — check which
		var count int64
		r.db.WithContext(ctx).Model(&types.WikiPage{}).Where("id = ?", page.ID).Count(&count)
		if count == 0 {
			return ErrWikiPageNotFound
		}
		return ErrWikiPageConflict
	}
	return nil
}

// UpdateAutoLinkedContent persists content changes produced by the automatic
// link decorators (cross-link injection, dead-link cleanup) without bumping
// `version`. These passes rewrite the same revision with wiki-link markup
// added or removed; treating them as real edits would make newly-ingested
// pages appear as v2 on first view and confuse users who expect `version` to
// correspond to the number of intentional revisions.
func (r *wikiPageRepository) UpdateAutoLinkedContent(ctx context.Context, page *types.WikiPage) error {
	result := r.db.WithContext(ctx).
		Model(page).
		Where("id = ?", page.ID).
		Updates(map[string]interface{}{
			"content":    page.Content,
			"out_links":  page.OutLinks,
			"updated_at": page.UpdatedAt,
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrWikiPageNotFound
	}
	return nil
}

// UpdateMeta updates bookkeeping / provenance fields WITHOUT incrementing the
// version number. "Content" for versioning purposes is the user-visible page
// body (title/content/summary/page_type/status); everything else — links,
// source refs, chunk refs, page_metadata — is considered bookkeeping and is
// refreshed here so the version counter only advances on real edits.
//
// Used by link maintenance, re-ingest (same-content case), and status changes.
func (r *wikiPageRepository) UpdateMeta(ctx context.Context, page *types.WikiPage) error {
	result := r.db.WithContext(ctx).
		Model(page).
		Where("id = ?", page.ID).
		Updates(map[string]interface{}{
			"in_links":      page.InLinks,
			"out_links":     page.OutLinks,
			"status":        page.Status,
			"source_refs":   page.SourceRefs,
			"chunk_refs":    page.ChunkRefs,
			"page_metadata": page.PageMetadata,
			"updated_at":    page.UpdatedAt,
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrWikiPageNotFound
	}
	return nil
}

// GetByID retrieves a wiki page by its unique ID
func (r *wikiPageRepository) GetByID(ctx context.Context, id string) (*types.WikiPage, error) {
	var page types.WikiPage
	if err := r.db.WithContext(ctx).Where("id = ?", id).First(&page).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrWikiPageNotFound
		}
		return nil, err
	}
	return &page, nil
}

// GetBySlug retrieves a wiki page by slug within a knowledge base
func (r *wikiPageRepository) GetBySlug(ctx context.Context, kbID string, slug string) (*types.WikiPage, error) {
	var page types.WikiPage
	if err := r.db.WithContext(ctx).
		Where("knowledge_base_id = ? AND slug = ?", kbID, slug).
		First(&page).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrWikiPageNotFound
		}
		return nil, err
	}
	return &page, nil
}

// List retrieves wiki pages with filtering and pagination
func (r *wikiPageRepository) List(ctx context.Context, req *types.WikiPageListRequest) ([]*types.WikiPage, int64, error) {
	query := r.db.WithContext(ctx).Model(&types.WikiPage{}).
		Where("knowledge_base_id = ?", req.KnowledgeBaseID)

	if req.PageType != "" {
		query = query.Where("page_type = ?", req.PageType)
	}
	if req.Status != "" {
		query = query.Where("status = ?", req.Status)
	}
	if req.Query != "" {
		// Use PostgreSQL full-text search + ILIKE for aliases
		query = query.Where(
			"(to_tsvector('simple', coalesce(title, '') || ' ' || coalesce(content, '')) @@ plainto_tsquery('simple', ?) OR aliases::text ILIKE ?)",
			req.Query,
			"%"+req.Query+"%",
		)
	}

	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	// Sort
	sortBy := "updated_at"
	if req.SortBy != "" {
		switch req.SortBy {
		case "title", "created_at", "updated_at", "page_type":
			sortBy = req.SortBy
		}
	}
	sortOrder := "DESC"
	if req.SortOrder == "asc" {
		sortOrder = "ASC"
	}
	query = query.Order(fmt.Sprintf("%s %s", sortBy, sortOrder))

	// Pagination
	page := req.Page
	if page < 1 {
		page = 1
	}
	pageSize := req.PageSize
	if pageSize < 1 {
		pageSize = 20
	}
	offset := (page - 1) * pageSize
	query = query.Offset(offset).Limit(pageSize)

	var pages []*types.WikiPage
	if err := query.Find(&pages).Error; err != nil {
		return nil, 0, err
	}
	return pages, total, nil
}

// ListByType retrieves all wiki pages of a given type within a knowledge base
func (r *wikiPageRepository) ListByType(ctx context.Context, kbID string, pageType string) ([]*types.WikiPage, error) {
	var pages []*types.WikiPage
	if err := r.db.WithContext(ctx).
		Where("knowledge_base_id = ? AND page_type = ?", kbID, pageType).
		Order("updated_at DESC").
		Find(&pages).Error; err != nil {
		return nil, err
	}
	return pages, nil
}

// ListByTypeLight projects only the columns needed to render an index
// directory entry (slug, title, summary) and paginates by title ASC.
// This keeps the GET /wiki/index response cheap on KBs with tens of
// thousands of pages — the old path loaded every row including its TEXT
// content just to throw the content away on the way out.
//
// Archived pages are excluded. `limit` clamps to [1, 200]; `offset` is
// honored as-is. Returns the total non-archived count for the type
// alongside the page so the caller can render "showing N of M".
func (r *wikiPageRepository) ListByTypeLight(
	ctx context.Context,
	kbID string,
	pageType string,
	limit int,
	offset int,
) ([]types.WikiIndexEntry, int64, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	if offset < 0 {
		offset = 0
	}

	base := r.db.WithContext(ctx).
		Model(&types.WikiPage{}).
		Where("knowledge_base_id = ? AND page_type = ? AND status <> ?",
			kbID, pageType, types.WikiPageStatusArchived)

	var total int64
	if err := base.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	if total == 0 {
		return nil, 0, nil
	}

	var entries []types.WikiIndexEntry
	if err := base.
		Select("slug", "title", "summary").
		Order("title ASC").
		Limit(limit).
		Offset(offset).
		Scan(&entries).Error; err != nil {
		return nil, 0, err
	}
	return entries, total, nil
}

// ListBySourceRef retrieves all wiki pages that reference a given source knowledge ID.
// Handles both old format ("knowledgeID") and new format ("knowledgeID|title") in source_refs JSON array.
func (r *wikiPageRepository) ListBySourceRef(ctx context.Context, kbID string, sourceKnowledgeID string) ([]*types.WikiPage, error) {
	// Build the JSON needle safely so arbitrary IDs cannot break out of the
	// quoted string (e.g. ids containing quotes or backslashes).
	needle, err := json.Marshal([]string{sourceKnowledgeID})
	if err != nil {
		return nil, fmt.Errorf("marshal source ref needle: %w", err)
	}

	// For the "knowledgeID|title" prefix form, match against the JSON-encoded
	// value: json.Marshal escapes special chars so the LIKE pattern is safe.
	prefix, err := json.Marshal(sourceKnowledgeID + "|")
	if err != nil {
		return nil, fmt.Errorf("marshal source ref prefix: %w", err)
	}
	// prefix is a JSON string including the surrounding quotes; e.g. "abc|".
	// We strip the trailing quote so LIKE can continue into the title portion.
	prefixStr := string(prefix)
	if len(prefixStr) >= 2 && prefixStr[len(prefixStr)-1] == '"' {
		prefixStr = prefixStr[:len(prefixStr)-1]
	}
	// Escape LIKE metacharacters in the already-JSON-escaped prefix, then wrap
	// with %…% to match anywhere in the serialized JSON array.
	likePattern := "%" + escapeLikePattern(prefixStr) + "%"

	var pages []*types.WikiPage
	if err := r.db.WithContext(ctx).
		Where("knowledge_base_id = ? AND (source_refs @> ?::jsonb OR source_refs::text LIKE ?)",
			kbID,
			string(needle),
			likePattern,
		).
		Find(&pages).Error; err != nil {
		return nil, err
	}
	return pages, nil
}

// ListSlugsBySourceRef returns just the slugs of pages that reference the
// given knowledge id. Same predicate as ListBySourceRef (both forms
// "knowledgeID" and "knowledgeID|title"), but projected down to a single
// column so the wiki ingest pipeline doesn't have to load full rows when
// it only needs a "before" set of slugs.
//
// Backed by idx_wiki_pages_source_refs (GIN jsonb_path_ops) for the
// containment branch and idx_wiki_pages_source_refs_text for the legacy
// text-LIKE branch — both added in migration 000041.
func (r *wikiPageRepository) ListSlugsBySourceRef(ctx context.Context, kbID string, sourceKnowledgeID string) ([]string, error) {
	needle, err := json.Marshal([]string{sourceKnowledgeID})
	if err != nil {
		return nil, fmt.Errorf("marshal source ref needle: %w", err)
	}
	prefix, err := json.Marshal(sourceKnowledgeID + "|")
	if err != nil {
		return nil, fmt.Errorf("marshal source ref prefix: %w", err)
	}
	prefixStr := string(prefix)
	if len(prefixStr) >= 2 && prefixStr[len(prefixStr)-1] == '"' {
		prefixStr = prefixStr[:len(prefixStr)-1]
	}
	likePattern := "%" + escapeLikePattern(prefixStr) + "%"

	var slugs []string
	if err := r.db.WithContext(ctx).
		Model(&types.WikiPage{}).
		Where("knowledge_base_id = ? AND (source_refs @> ?::jsonb OR source_refs::text LIKE ?)",
			kbID,
			string(needle),
			likePattern,
		).
		Pluck("slug", &slugs).Error; err != nil {
		return nil, err
	}
	return slugs, nil
}

// ListBySlugs returns lightweight projections (slug, title, page_type,
// status, aliases, out_links) for the given slugs in one IN query.
// Used by wiki ingest's lazy fetcher path to resolve slug -> title /
// out-links during Map/Reduce without paying for a full ListAll scan.
//
// Empty input returns nil, nil. Slugs not present in the KB are silently
// dropped from the returned map (caller treats absent slugs as "no
// such page" — the same shape ListAll had via missing keys).
func (r *wikiPageRepository) ListBySlugs(
	ctx context.Context,
	kbID string,
	slugs []string,
) (map[string]*types.WikiPageLite, error) {
	if len(slugs) == 0 {
		return nil, nil
	}
	var rows []types.WikiPageLite
	if err := r.db.WithContext(ctx).
		Model(&types.WikiPage{}).
		Select("slug", "title", "page_type", "status", "aliases", "out_links").
		Where("knowledge_base_id = ? AND slug IN ?", kbID, slugs).
		Scan(&rows).Error; err != nil {
		return nil, err
	}
	out := make(map[string]*types.WikiPageLite, len(rows))
	for i := range rows {
		r := rows[i]
		out[r.Slug] = &r
	}
	return out, nil
}

// ListSummariesByKnowledgeIDs returns summary-page content keyed by the
// knowledge id that authored it. The page_type filter is applied first
// (only summary pages have content suitable for retract framing); within
// that subset we look at source_refs for either the bare knowledge id
// or the "knowledgeID|title" legacy form.
//
// Empty kids returns nil, nil. A knowledge id with no surviving summary
// page is silently absent from the result map.
//
// Used by reduceSlugUpdates' retract branch so it can frame "what did
// the now-departed sibling document contribute?" for the WikiPageModify
// LLM call without needing to keep the whole batchCtx.SummaryContent
// map in memory ahead of time.
func (r *wikiPageRepository) ListSummariesByKnowledgeIDs(
	ctx context.Context,
	kbID string,
	kids []string,
) (map[string]string, error) {
	if len(kids) == 0 {
		return nil, nil
	}

	// Build a JSONB containment-OR with one needle per knowledge id,
	// plus a single text-LIKE OR over the legacy prefix forms. The
	// containment branches each get their own GIN index probe; the
	// LIKE branch falls back to the text fulltext GIN.
	type row struct {
		Content    string            `gorm:"column:content"`
		SourceRefs types.StringArray `gorm:"column:source_refs"`
	}

	q := r.db.WithContext(ctx).
		Model(&types.WikiPage{}).
		Select("content", "source_refs").
		Where("knowledge_base_id = ? AND page_type = ? AND status <> ?",
			kbID, types.WikiPageTypeSummary, types.WikiPageStatusArchived)

	// Build OR clauses without using overly-clever GORM tricks: assemble
	// raw SQL fragments + args. Keeping this defensive because source_refs
	// patterns include user-controlled knowledge ids.
	clauses := make([]string, 0, len(kids)*2)
	args := make([]interface{}, 0, len(kids)*2)
	for _, kid := range kids {
		if kid == "" {
			continue
		}
		needle, err := json.Marshal([]string{kid})
		if err != nil {
			return nil, fmt.Errorf("marshal kid needle: %w", err)
		}
		clauses = append(clauses, "source_refs @> ?::jsonb")
		args = append(args, string(needle))

		prefix, err := json.Marshal(kid + "|")
		if err != nil {
			return nil, fmt.Errorf("marshal kid prefix: %w", err)
		}
		prefixStr := string(prefix)
		if len(prefixStr) >= 2 && prefixStr[len(prefixStr)-1] == '"' {
			prefixStr = prefixStr[:len(prefixStr)-1]
		}
		clauses = append(clauses, "source_refs::text LIKE ?")
		args = append(args, "%"+escapeLikePattern(prefixStr)+"%")
	}
	if len(clauses) == 0 {
		return nil, nil
	}
	q = q.Where("("+strings.Join(clauses, " OR ")+")", args...)

	var rows []row
	if err := q.Scan(&rows).Error; err != nil {
		return nil, err
	}

	// Map each row's content to every kid in its source_refs (a single
	// summary may carry multiple sources after a previous merge / re-
	// ingest). Caller looks up by kid, so duplicates resolve to the
	// same content string.
	kidSet := make(map[string]struct{}, len(kids))
	for _, kid := range kids {
		if kid != "" {
			kidSet[kid] = struct{}{}
		}
	}
	out := make(map[string]string, len(rows))
	for _, r := range rows {
		for _, ref := range r.SourceRefs {
			refKID := ref
			if pipeIdx := strings.Index(ref, "|"); pipeIdx > 0 {
				refKID = ref[:pipeIdx]
			}
			if _, want := kidSet[refKID]; !want {
				continue
			}
			if _, exists := out[refKID]; !exists {
				out[refKID] = r.Content
			}
		}
	}
	return out, nil
}

// ExistsSlugs reports which of the given slugs are live (non-archived,
// non-deleted) in the KB. Used by cleanDeadLinks to validate out-link
// targets without loading the referenced pages' content. Slugs not
// present in the KB at all map to false; archived slugs also map to
// false so dead-link cleanup treats them as gone.
//
// Empty input returns nil, nil so callers can branch cheaply.
func (r *wikiPageRepository) ExistsSlugs(
	ctx context.Context,
	kbID string,
	slugs []string,
) (map[string]bool, error) {
	if len(slugs) == 0 {
		return nil, nil
	}
	var live []string
	if err := r.db.WithContext(ctx).
		Model(&types.WikiPage{}).
		Where("knowledge_base_id = ? AND slug IN ? AND status <> ?",
			kbID, slugs, types.WikiPageStatusArchived).
		Pluck("slug", &live).Error; err != nil {
		return nil, err
	}
	out := make(map[string]bool, len(slugs))
	for _, s := range slugs {
		out[s] = false
	}
	for _, s := range live {
		out[s] = true
	}
	return out, nil
}

// ListAllSlugs returns every non-archived page slug in the KB. Used by
// the lint pipeline to compute the "live slug set" for broken-link
// detection without paying for ListAll's full row materialization.
func (r *wikiPageRepository) ListAllSlugs(
	ctx context.Context,
	kbID string,
) ([]string, error) {
	var slugs []string
	if err := r.db.WithContext(ctx).
		Model(&types.WikiPage{}).
		Where("knowledge_base_id = ? AND status <> ?", kbID, types.WikiPageStatusArchived).
		Pluck("slug", &slugs).Error; err != nil {
		return nil, err
	}
	return slugs, nil
}

// ListPagesCursor returns up to `limit` pages for the KB ordered by
// (knowledge_base_id, id) ascending, paginated by an opaque numeric
// cursor. The cursor is the stringified id of the last row from the
// previous page; "" starts from the beginning. Empty nextCursor =
// end-of-stream.
//
// Used by lint to walk the entire KB without ever holding the full
// page set in memory. `limit` is clamped to [1, 500].
func (r *wikiPageRepository) ListPagesCursor(
	ctx context.Context,
	kbID string,
	cursor string,
	limit int,
) ([]*types.WikiPage, string, error) {
	if limit <= 0 {
		limit = 100
	}
	if limit > 500 {
		limit = 500
	}
	q := r.db.WithContext(ctx).
		Where("knowledge_base_id = ?", kbID).
		Order("id ASC").
		Limit(limit)
	if cursor != "" {
		q = q.Where("id > ?", cursor)
	}
	var pages []*types.WikiPage
	if err := q.Find(&pages).Error; err != nil {
		return nil, "", err
	}
	nextCursor := ""
	if len(pages) == limit {
		nextCursor = pages[len(pages)-1].ID
	}
	return pages, nextCursor, nil
}

// ListByTypeRecent returns up to `limit` summary-typed pages ordered
// by updated_at DESC, projected to slug/title/summary. Used by the
// rebuildIndexPage first-time generation path — historically that
// loaded EVERY summary page and concatenated them into the prompt,
// which broke the LLM context window once a KB grew past a few
// thousand documents. The recent-N projection caps the prompt size
// at the cost of intro framing for very old documents (which are
// unlikely to be the most-relevant index introductions anyway).
//
// `limit` is clamped to [1, 1000]; 0 falls back to 200.
func (r *wikiPageRepository) ListByTypeRecent(
	ctx context.Context,
	kbID string,
	pageType string,
	limit int,
) ([]types.WikiIndexEntry, error) {
	if limit <= 0 {
		limit = 200
	}
	if limit > 1000 {
		limit = 1000
	}
	var entries []types.WikiIndexEntry
	if err := r.db.WithContext(ctx).
		Model(&types.WikiPage{}).
		Select("slug", "title", "summary").
		Where("knowledge_base_id = ? AND page_type = ? AND status <> ?",
			kbID, pageType, types.WikiPageStatusArchived).
		Order("updated_at DESC").
		Limit(limit).
		Scan(&entries).Error; err != nil {
		return nil, err
	}
	return entries, nil
}

// FindSimilarPages returns the top-k entity/concept pages whose lowercase
// title is most similar to the given query under PostgreSQL pg_trgm
// trigram similarity. Backed by idx_wiki_pages_title_trgm (GIN
// gin_trgm_ops, migration 000041). Used by the dedup pre-filter to
// surface candidate merge targets without loading every entity/concept
// page into Go.
//
// types is an optional page_type allow-list; empty means entity+concept.
// limit is clamped to [1, 50]. Pages whose title similarity is below
// 0.1 are dropped server-side via the `%` operator (which respects
// pg_trgm.similarity_threshold).
func (r *wikiPageRepository) FindSimilarPages(
	ctx context.Context,
	kbID string,
	query string,
	pageTypes []string,
	limit int,
) ([]*types.WikiPageLite, error) {
	if strings.TrimSpace(query) == "" {
		return nil, nil
	}
	if limit <= 0 {
		limit = 20
	}
	if limit > 50 {
		limit = 50
	}
	if len(pageTypes) == 0 {
		pageTypes = []string{types.WikiPageTypeEntity, types.WikiPageTypeConcept}
	}

	q := strings.ToLower(strings.TrimSpace(query))

	var rows []types.WikiPageLite
	if err := r.db.WithContext(ctx).
		Model(&types.WikiPage{}).
		Select("slug, title, page_type, status, aliases, out_links, similarity(lower(title), ?) AS sim", q).
		Where("knowledge_base_id = ? AND page_type IN ? AND status <> ? AND lower(title) % ?",
			kbID, pageTypes, types.WikiPageStatusArchived, q).
		Order("sim DESC").
		Limit(limit).
		Scan(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]*types.WikiPageLite, len(rows))
	for i := range rows {
		r := rows[i]
		out[i] = &r
	}
	return out, nil
}

// ListAll retrieves all wiki pages in a knowledge base
func (r *wikiPageRepository) ListAll(ctx context.Context, kbID string) ([]*types.WikiPage, error) {
	var pages []*types.WikiPage
	if err := r.db.WithContext(ctx).
		Where("knowledge_base_id = ?", kbID).
		Order("page_type ASC, title ASC").
		Find(&pages).Error; err != nil {
		return nil, err
	}
	return pages, nil
}

// ListRecentForSuggestions returns recent user-visible wiki pages across the given
// knowledge bases, used as a fallback source for agent suggested questions when
// the KB has no FAQ entries or AI-generated document questions (typical for
// Wiki-only KBs). Excludes index/log pages and archived pages.
func (r *wikiPageRepository) ListRecentForSuggestions(
	ctx context.Context,
	tenantID uint64,
	kbIDs []string,
	limit int,
) ([]*types.WikiPage, error) {
	if len(kbIDs) == 0 || limit <= 0 {
		return nil, nil
	}
	var pages []*types.WikiPage
	if err := r.db.WithContext(ctx).
		Where("tenant_id = ?", tenantID).
		Where("knowledge_base_id IN ?", kbIDs).
		Where("page_type NOT IN ?", []string{types.WikiPageTypeIndex, types.WikiPageTypeLog}).
		Where("status = ?", types.WikiPageStatusPublished).
		Where("title <> ''").
		Order("updated_at DESC").
		Limit(limit).
		Find(&pages).Error; err != nil {
		return nil, err
	}
	return pages, nil
}

// Delete soft-deletes a wiki page by knowledge base ID and slug
func (r *wikiPageRepository) Delete(ctx context.Context, kbID string, slug string) error {
	result := r.db.WithContext(ctx).
		Where("knowledge_base_id = ? AND slug = ?", kbID, slug).
		Delete(&types.WikiPage{})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrWikiPageNotFound
	}
	return nil
}

// DeleteByID soft-deletes a wiki page by ID
func (r *wikiPageRepository) DeleteByID(ctx context.Context, id string) error {
	result := r.db.WithContext(ctx).
		Where("id = ?", id).
		Delete(&types.WikiPage{})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrWikiPageNotFound
	}
	return nil
}

// escapeLikePattern escapes LIKE / ILIKE metacharacters so the returned string
// can be safely concatenated with % wildcards without unintended matches.
// Order matters: escape the backslash first, then the wildcards.
func escapeLikePattern(s string) string {
	replacer := strings.NewReplacer(
		`\`, `\\`,
		`%`, `\%`,
		`_`, `\_`,
	)
	return replacer.Replace(s)
}

// Search performs case-insensitive POSIX regex search on wiki pages within a knowledge base.
// The query is interpreted as a PostgreSQL regular expression (via ~*).
//
// Results are ranked by where the query hit, highest-relevance first:
//
//	title    hit → rank 4 (most obvious intent: user typed what the page is called)
//	slug     hit → rank 3 (url-like identifiers, direct jump)
//	summary  hit → rank 2 (short authored abstract)
//	content  hit → rank 1 (body mention — often surfaces unrelated pages whose
//	                       prose merely mentions the query as trivia)
//
// Without this ranking, a user searching for "王新" on a 4万-page wiki will
// see pages like "华为" or "Index" ahead of the actual 王新 page just
// because they mention 王新 in their body and were updated more recently.
// updated_at stays as the tiebreaker so same-rank ties stay deterministic.
func (r *wikiPageRepository) Search(ctx context.Context, kbID string, query string, limit int) ([]*types.WikiPage, error) {
	if limit <= 0 {
		limit = 10
	}
	if limit > 50 {
		limit = 50
	}

	// CASE expression is evaluated per-row during SELECT; we order by the
	// alias so the DB only computes the rank once. Parameterized four
	// times with the same regex to avoid coupling to GORM's positional
	// arg rewriting quirks.
	rankExpr := "CASE " +
		"WHEN title ~* ? THEN 4 " +
		"WHEN slug ~* ? THEN 3 " +
		"WHEN summary ~* ? THEN 2 " +
		"WHEN content ~* ? THEN 1 " +
		"ELSE 0 END AS match_rank"

	var pages []*types.WikiPage
	if err := r.db.WithContext(ctx).
		Select("*, "+rankExpr, query, query, query, query).
		Where("knowledge_base_id = ? AND (title ~* ? OR content ~* ? OR summary ~* ? OR slug ~* ?)",
			kbID, query, query, query, query).
		Where("status != ?", "archived").
		Order("match_rank DESC, updated_at DESC").
		Limit(limit).
		Find(&pages).Error; err != nil {
		return nil, err
	}
	return pages, nil
}

// CountByType returns page counts grouped by type for a knowledge base
func (r *wikiPageRepository) CountByType(ctx context.Context, kbID string) (map[string]int64, error) {
	type result struct {
		PageType string
		Count    int64
	}
	var results []result
	if err := r.db.WithContext(ctx).
		Model(&types.WikiPage{}).
		Select("page_type, count(*) as count").
		Where("knowledge_base_id = ?", kbID).
		Group("page_type").
		Scan(&results).Error; err != nil {
		return nil, err
	}

	counts := make(map[string]int64)
	for _, r := range results {
		counts[r.PageType] = r.Count
	}
	return counts, nil
}

// CountOrphans returns the number of pages with no inbound links
func (r *wikiPageRepository) CountOrphans(ctx context.Context, kbID string) (int64, error) {
	var count int64
	if err := r.db.WithContext(ctx).
		Model(&types.WikiPage{}).
		Where("knowledge_base_id = ?", kbID).
		Where("(in_links IS NULL OR in_links = '[]'::JSONB)").
		// Exclude index and log pages as they are naturally root pages
		Where("page_type NOT IN ?", []string{types.WikiPageTypeIndex, types.WikiPageTypeLog}).
		Count(&count).Error; err != nil {
		return 0, err
	}
	return count, nil
}

func (r *wikiPageRepository) CreateIssue(ctx context.Context, issue *types.WikiPageIssue) error {
	return r.db.WithContext(ctx).Create(issue).Error
}

func (r *wikiPageRepository) ListIssues(ctx context.Context, kbID string, slug string, status string) ([]*types.WikiPageIssue, error) {
	query := r.db.WithContext(ctx).Where("knowledge_base_id = ?", kbID)
	if slug != "" {
		query = query.Where("slug = ?", slug)
	}
	if status != "" {
		query = query.Where("status = ?", status)
	}

	var issues []*types.WikiPageIssue
	if err := query.Order("created_at DESC").Find(&issues).Error; err != nil {
		return nil, err
	}
	return issues, nil
}

func (r *wikiPageRepository) UpdateIssueStatus(ctx context.Context, issueID string, status string) error {
	return r.db.WithContext(ctx).Model(&types.WikiPageIssue{}).
		Where("id = ?", issueID).
		Update("status", status).Error
}
