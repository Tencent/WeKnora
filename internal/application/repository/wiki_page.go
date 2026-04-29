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
func (r *wikiPageRepository) Search(ctx context.Context, kbID string, query string, limit int) ([]*types.WikiPage, error) {
	if limit <= 0 {
		limit = 10
	}
	if limit > 50 {
		limit = 50
	}

	var pages []*types.WikiPage
	if err := r.db.WithContext(ctx).
		Where("knowledge_base_id = ? AND (title ~* ? OR content ~* ? OR summary ~* ? OR slug ~* ?)",
			kbID, query, query, query, query).
		Where("status != ?", "archived").
		Order("updated_at DESC").
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
