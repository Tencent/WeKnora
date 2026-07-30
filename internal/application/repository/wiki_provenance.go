package repository

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type wikiProvenanceRepository struct {
	db *gorm.DB
}

func NewWikiProvenanceRepository(db *gorm.DB) interfaces.WikiProvenanceRepository {
	return &wikiProvenanceRepository{db: db}
}

func NewWikiProvenanceLifecycleRepository(db *gorm.DB) interfaces.WikiProvenanceLifecycleRepository {
	return &wikiProvenanceRepository{db: db}
}

func (r *wikiProvenanceRepository) WithTransaction(
	ctx context.Context,
	fn func(interfaces.WikiProvenanceRepository) error,
) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return fn(&wikiProvenanceRepository{db: tx})
	})
}

const wikiEvidenceExcerptRunes = 800

type wikiProvenanceSourceReadRow struct {
	BlockID             string
	KnowledgeID         string
	KnowledgeRevisionID string
	KnowledgeRevisionNo int
	ParseAttempt        int
	KnowledgeTitle      string
	FileName            string
	FileType            string
	ChunkID             *string
	ChunkIndex          *int
	EvidenceContent     string
	EvidenceHash        string
	SourceRole          types.WikiSourceRole
	Confidence          float64
	ValidationStatus    types.WikiSourceValidationStatus
	SourceAvailable     bool
}

func (r *wikiProvenanceRepository) GetPageProvenance(
	ctx context.Context,
	tenantID uint64,
	kbID, pageID string,
) (*types.WikiPageProvenanceResponse, error) {
	if tenantID == 0 || kbID == "" || pageID == "" {
		return nil, errors.New("wiki provenance query requires tenant, knowledge base and page")
	}

	var pageCount int64
	if err := r.db.WithContext(ctx).Model(&types.WikiPage{}).
		Where("tenant_id = ? AND knowledge_base_id = ? AND id = ?", tenantID, kbID, pageID).
		Count(&pageCount).Error; err != nil {
		return nil, fmt.Errorf("verify wiki provenance page scope: %w", err)
	}
	if pageCount != 1 {
		return nil, types.ErrWikiPublishScopeNotFound
	}

	response := &types.WikiPageProvenanceResponse{
		PageID: pageID,
		Blocks: []types.WikiPageProvenanceBlock{},
	}
	var revision types.WikiProvenancePageRevision
	err := r.db.WithContext(ctx).
		Where(
			"tenant_id = ? AND knowledge_base_id = ? AND page_id = ? AND status = ?",
			tenantID, kbID, pageID, types.WikiPageRevisionPublished,
		).
		Order("revision_no DESC").
		First(&revision).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return response, nil
	}
	if err != nil {
		return nil, fmt.Errorf("load published wiki page revision: %w", err)
	}
	response.PageRevisionID = revision.ID
	response.RevisionNo = revision.RevisionNo
	response.ProvenanceStatus = revision.ProvenanceStatus

	var blocks []types.WikiPageBlock
	if err := r.db.WithContext(ctx).
		Where(
			"tenant_id = ? AND knowledge_base_id = ? AND page_id = ? AND page_revision_id = ?",
			tenantID, kbID, pageID, revision.ID,
		).
		Order("sort_order ASC, id ASC").
		Find(&blocks).Error; err != nil {
		return nil, fmt.Errorf("load wiki provenance blocks: %w", err)
	}
	if len(blocks) == 0 {
		return response, nil
	}

	blockIDs := make([]string, 0, len(blocks))
	blockIndex := make(map[string]int, len(blocks))
	for _, block := range blocks {
		blockIndex[block.ID] = len(response.Blocks)
		blockIDs = append(blockIDs, block.ID)
		response.Blocks = append(response.Blocks, types.WikiPageProvenanceBlock{
			ID:               block.ID,
			LogicalBlockID:   block.LogicalBlockID,
			BlockType:        block.BlockType,
			SortOrder:        block.SortOrder,
			Content:          block.Content,
			ProvenanceStatus: block.ProvenanceStatus,
			Sources:          []types.WikiPageProvenanceSource{},
		})
	}

	var sourceRows []wikiProvenanceSourceReadRow
	err = r.db.WithContext(ctx).
		Table("wiki_block_sources AS s").
		Select(`
			s.block_id,
			s.knowledge_id,
			s.knowledge_revision_id,
			kr.revision_no AS knowledge_revision_no,
			kr.parse_attempt,
			COALESCE(NULLIF(k.title, ''), NULLIF(k.file_name, ''), k.id) AS knowledge_title,
			k.file_name,
			k.file_type,
			s.chunk_id,
			c.chunk_index,
			COALESCE(c.content, '') AS evidence_content,
			s.evidence_hash,
			s.source_role,
			s.confidence,
			s.validation_status,
			CASE WHEN k.deleted_at IS NULL AND (s.chunk_id IS NULL OR c.id IS NOT NULL) THEN 1 ELSE 0 END AS source_available
		`).
		Joins("JOIN knowledge_revisions AS kr ON kr.id = s.knowledge_revision_id AND kr.tenant_id = s.tenant_id AND kr.knowledge_base_id = s.knowledge_base_id").
		Joins("JOIN knowledges AS k ON k.id = s.knowledge_id AND k.tenant_id = s.tenant_id AND k.knowledge_base_id = s.knowledge_base_id").
		Joins("LEFT JOIN chunks AS c ON c.id = s.chunk_id AND c.tenant_id = s.tenant_id AND c.knowledge_base_id = s.knowledge_base_id AND c.knowledge_id = s.knowledge_id AND c.deleted_at IS NULL").
		Where(
			"s.tenant_id = ? AND s.knowledge_base_id = ? AND s.page_id = ? AND s.block_id IN ?",
			tenantID, kbID, pageID, blockIDs,
		).
		Order("s.block_id ASC, s.knowledge_id ASC, s.id ASC").
		Scan(&sourceRows).Error
	if err != nil {
		return nil, fmt.Errorf("load wiki provenance sources: %w", err)
	}
	for _, row := range sourceRows {
		index, ok := blockIndex[row.BlockID]
		if !ok {
			continue
		}
		response.Blocks[index].Sources = append(response.Blocks[index].Sources, types.WikiPageProvenanceSource{
			KnowledgeID:         row.KnowledgeID,
			KnowledgeRevisionID: row.KnowledgeRevisionID,
			KnowledgeRevisionNo: row.KnowledgeRevisionNo,
			ParseAttempt:        row.ParseAttempt,
			KnowledgeTitle:      row.KnowledgeTitle,
			FileName:            row.FileName,
			FileType:            row.FileType,
			ChunkID:             row.ChunkID,
			ChunkIndex:          row.ChunkIndex,
			EvidenceExcerpt:     truncateWikiEvidenceExcerpt(row.EvidenceContent),
			EvidenceHash:        row.EvidenceHash,
			SourceRole:          row.SourceRole,
			Confidence:          row.Confidence,
			ValidationStatus:    row.ValidationStatus,
			SourceAvailable:     row.SourceAvailable,
		})
	}
	return response, nil
}

func truncateWikiEvidenceExcerpt(content string) string {
	content = strings.TrimSpace(content)
	runes := []rune(content)
	if len(runes) <= wikiEvidenceExcerptRunes {
		return content
	}
	return strings.TrimSpace(string(runes[:wikiEvidenceExcerptRunes])) + "…"
}

func (r *wikiProvenanceRepository) ListKnowledgePageImpacts(
	ctx context.Context,
	tenantID uint64,
	kbID, knowledgeID string,
) ([]types.WikiKnowledgePageImpact, error) {
	if tenantID == 0 || kbID == "" || knowledgeID == "" {
		return nil, errors.New("wiki provenance impact query requires tenant, knowledge base and knowledge")
	}
	return listKnowledgePageImpactsDB(r.db.WithContext(ctx), tenantID, kbID, knowledgeID)
}

func listKnowledgePageImpactsDB(
	db *gorm.DB,
	tenantID uint64,
	kbID, knowledgeID string,
) ([]types.WikiKnowledgePageImpact, error) {
	var impacts []types.WikiKnowledgePageImpact
	err := db.
		Table("wiki_page_sources AS source").
		Select(`
			page.id AS page_id,
			page.slug,
			page.title,
			page.summary,
			page.page_type,
			page.folder_id,
			source.supported_block_count,
			(
				SELECT COUNT(*)
				FROM wiki_page_sources AS all_sources
				WHERE all_sources.tenant_id = source.tenant_id
				  AND all_sources.knowledge_base_id = source.knowledge_base_id
				  AND all_sources.page_id = source.page_id
			) AS total_source_count
		`).
		Joins(`JOIN wiki_pages AS page
			ON page.id = source.page_id
			AND page.tenant_id = source.tenant_id
			AND page.knowledge_base_id = source.knowledge_base_id
			AND page.deleted_at IS NULL`).
		Where(
			"source.tenant_id = ? AND source.knowledge_base_id = ? AND source.knowledge_id = ?",
			tenantID, kbID, knowledgeID,
		).
		Order("page.slug ASC, page.id ASC").
		Scan(&impacts).Error
	if err != nil {
		return nil, fmt.Errorf("list wiki knowledge page impacts: %w", err)
	}
	if impacts == nil {
		impacts = []types.WikiKnowledgePageImpact{}
	}
	return impacts, nil
}

// DeleteKnowledgeSources atomically removes both the current page-level
// projection and every historical block-to-source edge for one knowledge.
// Revision rows are retained as deleted tombstones so revision numbers are
// never reused.
func (r *wikiProvenanceRepository) DeleteKnowledgeSources(
	ctx context.Context,
	tenantID uint64,
	kbID, knowledgeID string,
	at time.Time,
) (*types.WikiKnowledgeSourceCleanupResult, error) {
	if tenantID == 0 || kbID == "" || knowledgeID == "" {
		return nil, errors.New("wiki provenance cleanup requires tenant, knowledge base and knowledge")
	}
	if at.IsZero() {
		at = time.Now()
	}
	result := &types.WikiKnowledgeSourceCleanupResult{
		AffectedPages: []types.WikiKnowledgePageImpact{},
	}
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var knowledge struct{ ID string }
		err := tx.Table("knowledges").
			Select("id").
			Clauses(clause.Locking{Strength: "UPDATE"}).
			Where(
				"tenant_id = ? AND knowledge_base_id = ? AND id = ? AND deleted_at IS NULL",
				tenantID, kbID, knowledgeID,
			).
			First(&knowledge).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return types.ErrWikiPublishScopeNotFound
		}
		if err != nil {
			return fmt.Errorf("lock provenance source knowledge: %w", err)
		}

		impacts, err := listKnowledgePageImpactsDB(tx, tenantID, kbID, knowledgeID)
		if err != nil {
			return err
		}
		result.AffectedPages = impacts

		blockDelete := tx.Where(
			"tenant_id = ? AND knowledge_base_id = ? AND knowledge_id = ?",
			tenantID, kbID, knowledgeID,
		).Delete(&types.WikiBlockSource{})
		if blockDelete.Error != nil {
			return fmt.Errorf("delete wiki block sources: %w", blockDelete.Error)
		}
		result.DeletedBlockSources = blockDelete.RowsAffected

		pageDelete := tx.Where(
			"tenant_id = ? AND knowledge_base_id = ? AND knowledge_id = ?",
			tenantID, kbID, knowledgeID,
		).Delete(&types.WikiPageSource{})
		if pageDelete.Error != nil {
			return fmt.Errorf("delete wiki page sources: %w", pageDelete.Error)
		}
		result.DeletedPageSources = pageDelete.RowsAffected

		revisionUpdate := tx.Model(&types.KnowledgeRevision{}).
			Where(
				"tenant_id = ? AND knowledge_base_id = ? AND knowledge_id = ? AND deleted_at IS NULL",
				tenantID, kbID, knowledgeID,
			).
			Updates(map[string]any{
				"status":     types.KnowledgeRevisionDeleted,
				"deleted_at": at,
			})
		if revisionUpdate.Error != nil {
			return fmt.Errorf("mark knowledge revisions deleted: %w", revisionUpdate.Error)
		}
		result.DeletedKnowledgeRevisions = revisionUpdate.RowsAffected
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

// EnsureCurrentPage creates the FK parent for a brand-new page inside the
// publication transaction. Existing pages are left untouched. ON CONFLICT
// handles a concurrent creator; the scoped read below then verifies that the
// requested page identity is the row that actually exists.
func (r *wikiProvenanceRepository) EnsureCurrentPage(ctx context.Context, page *types.WikiPage) error {
	if page == nil || page.ID == "" || page.TenantID == 0 || page.KnowledgeBaseID == "" || page.Slug == "" {
		return errors.New("current wiki page requires id, tenant, knowledge base and slug")
	}
	var knowledgeBase struct{ ID string }
	err := r.db.WithContext(ctx).
		Table("knowledge_bases").
		Select("id").
		Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("tenant_id = ? AND id = ? AND deleted_at IS NULL", page.TenantID, page.KnowledgeBaseID).
		First(&knowledgeBase).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return types.ErrWikiPublishScopeNotFound
	}
	if err != nil {
		return fmt.Errorf("lock page knowledge base: %w", err)
	}
	var count int64
	query := r.db.WithContext(ctx).Model(&types.WikiPage{}).
		Where("tenant_id = ? AND knowledge_base_id = ? AND id = ?", page.TenantID, page.KnowledgeBaseID, page.ID).
		Count(&count)
	if query.Error != nil {
		return fmt.Errorf("find current wiki page: %w", query.Error)
	}
	if count == 0 {
		candidate := *page
		candidate.Status = types.WikiPageStatusDraft
		candidate.Content = ""
		candidate.Summary = ""
		candidate.SourceRefs = types.StringArray{}
		candidate.ChunkRefs = types.StringArray{}
		candidate.PageMetadata = types.JSON(`{}`)
		candidate.Version = 0
		candidate.CreatedAt = time.Time{}
		candidate.UpdatedAt = time.Time{}
		candidate.DeletedAt = (types.WikiPage{}).DeletedAt
		if err := r.db.WithContext(ctx).
			Clauses(clause.OnConflict{DoNothing: true}).
			Create(&candidate).Error; err != nil {
			return fmt.Errorf("create current wiki page shell: %w", err)
		}
	}
	var persisted types.WikiPage
	err = r.db.WithContext(ctx).
		Select("id", "tenant_id", "knowledge_base_id", "slug").
		Where("tenant_id = ? AND knowledge_base_id = ? AND id = ?", page.TenantID, page.KnowledgeBaseID, page.ID).
		First(&persisted).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return types.ErrWikiPublishScopeNotFound
	}
	if err != nil {
		return fmt.Errorf("verify current wiki page: %w", err)
	}
	if persisted.Slug != page.Slug {
		return types.ErrWikiPublishScopeNotFound
	}
	return nil
}

// LockPublishScope locks the parent page and source-document rows before any
// MAX(revision_no)+1 calculation. Sorted source IDs give all writers the same
// lock order and avoid cross-document deadlocks.
func (r *wikiProvenanceRepository) LockPublishScope(
	ctx context.Context,
	tenantID uint64,
	kbID, pageID string,
	knowledgeIDs []string,
) error {
	var page types.WikiPage
	err := r.db.WithContext(ctx).
		Select("id").
		Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("tenant_id = ? AND knowledge_base_id = ? AND id = ?", tenantID, kbID, pageID).
		First(&page).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return types.ErrWikiPublishScopeNotFound
	}
	if err != nil {
		return fmt.Errorf("lock wiki page: %w", err)
	}

	ids := uniqueSortedStrings(knowledgeIDs)
	if len(ids) == 0 {
		return nil
	}
	type knowledgeLockRow struct {
		ID string
	}
	var rows []knowledgeLockRow
	err = r.db.WithContext(ctx).
		Table("knowledges").
		Select("id").
		Clauses(clause.Locking{Strength: "UPDATE"}).
		Where(
			"tenant_id = ? AND knowledge_base_id = ? AND id IN ? AND deleted_at IS NULL AND (parse_status IS NULL OR parse_status <> ?)",
			tenantID, kbID, ids, types.ParseStatusDeleting,
		).
		Order("id ASC").
		Find(&rows).Error
	if err != nil {
		return fmt.Errorf("lock source knowledge: %w", err)
	}
	if len(rows) != len(ids) {
		return types.ErrWikiPublishScopeNotFound
	}
	return nil
}

func uniqueSortedStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func (r *wikiProvenanceRepository) FindPageRevisionByPublishKey(
	ctx context.Context,
	tenantID uint64,
	kbID, pageID, publishKey string,
) (*types.WikiProvenancePageRevision, error) {
	var revision types.WikiProvenancePageRevision
	err := r.db.WithContext(ctx).
		Where(
			"tenant_id = ? AND knowledge_base_id = ? AND page_id = ? AND publish_key = ?",
			tenantID, kbID, pageID, publishKey,
		).
		First(&revision).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("find page revision by publish key: %w", err)
	}
	return &revision, nil
}

func (r *wikiProvenanceRepository) GetKnowledgeRevision(
	ctx context.Context,
	tenantID uint64,
	kbID, revisionID string,
) (*types.KnowledgeRevision, error) {
	var revision types.KnowledgeRevision
	err := r.db.WithContext(ctx).
		Where("tenant_id = ? AND knowledge_base_id = ? AND id = ?", tenantID, kbID, revisionID).
		First(&revision).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get knowledge revision: %w", err)
	}
	return &revision, nil
}

func (r *wikiProvenanceRepository) FindKnowledgeRevisionByContentHash(
	ctx context.Context,
	tenantID uint64,
	kbID, knowledgeID, contentHash string,
	parseAttempt int,
) (*types.KnowledgeRevision, error) {
	if contentHash == "" {
		return nil, nil
	}
	var revision types.KnowledgeRevision
	err := r.db.WithContext(ctx).
		Where(
			"tenant_id = ? AND knowledge_base_id = ? AND knowledge_id = ? AND content_hash = ? AND parse_attempt = ? AND status IN ?",
			tenantID, kbID, knowledgeID, contentHash, parseAttempt,
			[]types.KnowledgeRevisionStatus{types.KnowledgeRevisionPublished, types.KnowledgeRevisionSuperseded},
		).
		Order("revision_no DESC").
		First(&revision).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("find knowledge revision by content hash: %w", err)
	}
	return &revision, nil
}

func (r *wikiProvenanceRepository) NextPageRevisionNo(
	ctx context.Context,
	tenantID uint64,
	kbID, pageID string,
) (int, error) {
	var currentPage types.WikiPage
	if err := r.db.WithContext(ctx).
		Select("id", "version").
		Where("tenant_id = ? AND knowledge_base_id = ? AND id = ?", tenantID, kbID, pageID).
		First(&currentPage).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return 0, types.ErrWikiPublishScopeNotFound
		}
		return 0, fmt.Errorf("load current page version: %w", err)
	}

	var currentProvenance int
	err := r.db.WithContext(ctx).
		Model(&types.WikiProvenancePageRevision{}).
		Select("COALESCE(MAX(revision_no), 0)").
		Where("tenant_id = ? AND knowledge_base_id = ? AND page_id = ?", tenantID, kbID, pageID).
		Scan(&currentProvenance).Error
	if err != nil {
		return 0, fmt.Errorf("next page revision number: %w", err)
	}
	return max(currentPage.Version, currentProvenance) + 1, nil
}

func (r *wikiProvenanceRepository) NextKnowledgeRevisionNo(
	ctx context.Context,
	tenantID uint64,
	kbID, knowledgeID string,
) (int, error) {
	var current int
	err := r.db.WithContext(ctx).
		Model(&types.KnowledgeRevision{}).
		Select("COALESCE(MAX(revision_no), 0)").
		Where(
			"tenant_id = ? AND knowledge_base_id = ? AND knowledge_id = ?",
			tenantID, kbID, knowledgeID,
		).
		Scan(&current).Error
	if err != nil {
		return 0, fmt.Errorf("next knowledge revision number: %w", err)
	}
	return current + 1, nil
}

func (r *wikiProvenanceRepository) CreateKnowledgeRevision(
	ctx context.Context,
	revision *types.KnowledgeRevision,
) error {
	return r.db.WithContext(ctx).Create(revision).Error
}

func (r *wikiProvenanceRepository) CreatePageRevision(
	ctx context.Context,
	revision *types.WikiProvenancePageRevision,
) error {
	return r.db.WithContext(ctx).Create(revision).Error
}

func (r *wikiProvenanceRepository) CreateBlocks(
	ctx context.Context,
	blocks []types.WikiPageBlock,
) error {
	if len(blocks) == 0 {
		return nil
	}
	return r.db.WithContext(ctx).Create(&blocks).Error
}

func (r *wikiProvenanceRepository) CreateBlockSources(
	ctx context.Context,
	sources []types.WikiBlockSource,
) error {
	if len(sources) == 0 {
		return nil
	}
	return r.db.WithContext(ctx).Create(&sources).Error
}

func (r *wikiProvenanceRepository) ReplacePageSources(
	ctx context.Context,
	tenantID uint64,
	kbID, pageID string,
	sources []types.WikiPageSource,
) error {
	query := r.db.WithContext(ctx).
		Where("tenant_id = ? AND knowledge_base_id = ? AND page_id = ?", tenantID, kbID, pageID).
		Delete(&types.WikiPageSource{})
	if query.Error != nil {
		return fmt.Errorf("delete old page source projection: %w", query.Error)
	}
	if len(sources) == 0 {
		return nil
	}
	if err := r.db.WithContext(ctx).Create(&sources).Error; err != nil {
		return fmt.Errorf("create page source projection: %w", err)
	}
	return nil
}

func (r *wikiProvenanceRepository) PublishKnowledgeRevision(
	ctx context.Context,
	tenantID uint64,
	kbID, knowledgeID, revisionID string,
	at time.Time,
) error {
	db := r.db.WithContext(ctx)
	if err := db.Model(&types.KnowledgeRevision{}).
		Where(
			"tenant_id = ? AND knowledge_base_id = ? AND knowledge_id = ? AND id <> ? AND status = ?",
			tenantID, kbID, knowledgeID, revisionID, types.KnowledgeRevisionPublished,
		).
		Updates(map[string]any{
			"status":        types.KnowledgeRevisionSuperseded,
			"superseded_at": at,
		}).Error; err != nil {
		return fmt.Errorf("supersede knowledge revision: %w", err)
	}
	result := db.Model(&types.KnowledgeRevision{}).
		Where(
			"tenant_id = ? AND knowledge_base_id = ? AND knowledge_id = ? AND id = ? AND status = ?",
			tenantID, kbID, knowledgeID, revisionID, types.KnowledgeRevisionStaged,
		).
		Updates(map[string]any{
			"status":       types.KnowledgeRevisionPublished,
			"published_at": at,
		})
	if result.Error != nil {
		return fmt.Errorf("publish knowledge revision: %w", result.Error)
	}
	if result.RowsAffected != 1 {
		return types.ErrWikiPublishScopeNotFound
	}
	return nil
}

func (r *wikiProvenanceRepository) PublishPageRevision(
	ctx context.Context,
	tenantID uint64,
	kbID, pageID, revisionID string,
	at time.Time,
) error {
	db := r.db.WithContext(ctx)
	if err := db.Model(&types.WikiProvenancePageRevision{}).
		Where(
			"tenant_id = ? AND knowledge_base_id = ? AND page_id = ? AND id <> ? AND status = ?",
			tenantID, kbID, pageID, revisionID, types.WikiPageRevisionPublished,
		).
		Updates(map[string]any{
			"status":        types.WikiPageRevisionSuperseded,
			"superseded_at": at,
		}).Error; err != nil {
		return fmt.Errorf("supersede page revision: %w", err)
	}
	result := db.Model(&types.WikiProvenancePageRevision{}).
		Where(
			"tenant_id = ? AND knowledge_base_id = ? AND page_id = ? AND id = ? AND status = ?",
			tenantID, kbID, pageID, revisionID, types.WikiPageRevisionStaged,
		).
		Updates(map[string]any{
			"status":       types.WikiPageRevisionPublished,
			"published_at": at,
		})
	if result.Error != nil {
		return fmt.Errorf("publish page revision: %w", result.Error)
	}
	if result.RowsAffected != 1 {
		return types.ErrWikiPublishScopeNotFound
	}
	return nil
}

func (r *wikiProvenanceRepository) UpdateCurrentPage(
	ctx context.Context,
	tenantID uint64,
	kbID string,
	page *types.WikiPage,
	revision *types.WikiProvenancePageRevision,
	at time.Time,
) error {
	if page == nil || revision == nil || page.ID != revision.PageID {
		return errors.New("current page projection does not match page revision")
	}
	var current types.WikiPage
	if err := r.db.WithContext(ctx).
		Where(
			"tenant_id = ? AND knowledge_base_id = ? AND id = ?",
			tenantID, kbID, revision.PageID,
		).
		First(&current).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return types.ErrWikiPublishScopeNotFound
		}
		return fmt.Errorf("load current wiki page for snapshot: %w", err)
	}
	if revision.RevisionNo <= current.Version {
		return fmt.Errorf(
			"wiki provenance revision %d does not advance current page version %d",
			revision.RevisionNo, current.Version,
		)
	}
	if current.Version > 0 {
		snapshot := &types.WikiPageRevision{
			ID:              uuid.NewString(),
			TenantID:        current.TenantID,
			KnowledgeBaseID: current.KnowledgeBaseID,
			PageID:          current.ID,
			Slug:            current.Slug,
			Version:         current.Version,
			Title:           current.Title,
			PageType:        current.PageType,
			Status:          current.Status,
			Content:         current.Content,
			Summary:         current.Summary,
			Aliases:         append(types.StringArray(nil), current.Aliases...),
			EditSource:      types.NormalizeWikiEditSource(current.LastEditSource),
			EditorID:        current.LastEditorID,
			EditedAt:        current.UpdatedAt,
			CreatedAt:       at,
		}
		if err := r.db.WithContext(ctx).Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "page_id"}, {Name: "version"}},
			DoNothing: true,
		}).Create(snapshot).Error; err != nil {
			return fmt.Errorf("snapshot current wiki page: %w", err)
		}
	}
	result := r.db.WithContext(ctx).
		Model(&types.WikiPage{}).
		Where(
			"tenant_id = ? AND knowledge_base_id = ? AND id = ? AND version = ?",
			tenantID, kbID, revision.PageID, current.Version,
		).
		Updates(map[string]any{
			"slug":             page.Slug,
			"title":            revision.Title,
			"summary":          revision.Summary,
			"content":          revision.RenderedContent,
			"page_type":        page.PageType,
			"status":           types.WikiPageStatusPublished,
			"aliases":          page.Aliases,
			"parent_slug":      page.ParentSlug,
			"folder_id":        page.FolderID,
			"category_path":    page.CategoryPath,
			"wiki_path":        page.WikiPath,
			"depth":            page.Depth,
			"sort_order":       page.SortOrder,
			"source_refs":      page.SourceRefs,
			"chunk_refs":       page.ChunkRefs,
			"in_links":         page.InLinks,
			"out_links":        page.OutLinks,
			"page_metadata":    page.PageMetadata,
			"version":          revision.RevisionNo,
			"last_edit_source": types.WikiEditSourcePipeline,
			"last_editor_id":   "",
			"updated_at":       at,
		})
	if result.Error != nil {
		return fmt.Errorf("update current wiki page: %w", result.Error)
	}
	if result.RowsAffected != 1 {
		return types.ErrWikiPublishScopeNotFound
	}
	return nil
}

var _ interfaces.WikiProvenanceRepository = (*wikiProvenanceRepository)(nil)
var _ interfaces.WikiProvenanceLifecycleRepository = (*wikiProvenanceRepository)(nil)
