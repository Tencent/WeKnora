package repository

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	ErrWikiBlockSetNotFound           = errors.New("wiki block set not found")
	ErrWikiBlockSetNotStaged          = errors.New("wiki block set is not staged")
	ErrWikiBlockSetConflict           = errors.New("wiki block set conflict")
	ErrWikiBlockSourceStale           = errors.New("wiki block source is no longer current")
	ErrWikiBlockSourceAttemptConflict = errors.New("wiki block source parse attempt is no longer current")
)

// wikiBlockSetCurrentSourceAttemptsSQL is both the validation predicate and
// the final publication guard. Keeping it on the staged->published UPDATE
// closes the window where a newer parse attempt could be persisted after the
// transaction's initial source check but before the page snapshot becomes
// visible. Attempt zero is the explicit legacy/untracked compatibility path.
const wikiBlockSetCurrentSourceAttemptsSQL = `NOT EXISTS (
	SELECT 1
	FROM wiki_block_sources AS wiki_source
	JOIN wiki_page_blocks AS wiki_block ON wiki_block.id = wiki_source.block_id
	WHERE wiki_block.block_set_id = ?
	  AND wiki_source.knowledge_attempt > 0
	  AND wiki_source.knowledge_attempt <> (
		SELECT COALESCE(MAX(kps.attempt), 0)
		FROM knowledge_processing_spans AS kps
		WHERE kps.knowledge_id = wiki_source.knowledge_id
	  )
)`

type wikiProvenanceRepository struct {
	db *gorm.DB
}

func NewWikiProvenanceRepository(db *gorm.DB) interfaces.WikiProvenanceRepository {
	return &wikiProvenanceRepository{db: db}
}

func normalizeStagedBlockSet(set *types.WikiPageBlockSet) error {
	if set == nil {
		return errors.New("wiki block set is required")
	}
	if set.ID == "" {
		set.ID = uuid.NewString()
	}
	if set.TenantID == 0 || set.KnowledgeBaseID == "" || set.PageID == "" || set.PageVersion < 1 {
		return errors.New("wiki block set tenant, knowledge base, page, and positive version are required")
	}
	if set.Status == "" {
		set.Status = types.WikiBlockSetStatusStaged
	}
	if set.Status != types.WikiBlockSetStatusStaged {
		return ErrWikiBlockSetNotStaged
	}
	if set.CreatedAt.IsZero() {
		set.CreatedAt = time.Now()
	}
	set.PublishedAt = nil
	for i, block := range set.Blocks {
		if block == nil {
			return fmt.Errorf("wiki block %d is nil", i)
		}
		if block.ID == "" {
			block.ID = uuid.NewString()
		}
		if block.LogicalBlockID == "" {
			block.LogicalBlockID = uuid.NewString()
		}
		if block.BlockType == "" {
			return fmt.Errorf("wiki block %d has no type", i)
		}
		block.BlockSetID = set.ID
		block.SortOrder = i
		if block.SectionPath == nil {
			block.SectionPath = types.StringArray{}
		}
		if block.AuthorType == "" {
			block.AuthorType = types.WikiEditSourcePipeline
		}
		if block.ProvenanceStatus == "" {
			block.ProvenanceStatus = types.WikiBlockProvenanceUnsupported
		}
		if block.CreatedAt.IsZero() {
			block.CreatedAt = set.CreatedAt
		}
		for j, source := range block.Sources {
			if source == nil {
				return fmt.Errorf("wiki block %d source %d is nil", i, j)
			}
			if source.ID == "" {
				source.ID = uuid.NewString()
			}
			if source.KnowledgeID == "" || source.ChunkID == "" {
				return fmt.Errorf("wiki block %d source %d has no knowledge or chunk id", i, j)
			}
			source.BlockID = block.ID
			source.TenantID = set.TenantID
			source.KnowledgeBaseID = set.KnowledgeBaseID
			source.SortOrder = j
			if source.ValidationStatus == "" {
				source.ValidationStatus = types.WikiSourceValidationInvalid
			}
			if source.CreatedAt.IsZero() {
				source.CreatedAt = block.CreatedAt
			}
		}
	}
	return nil
}

func createWikiBlockSetTree(tx *gorm.DB, set *types.WikiPageBlockSet) error {
	if err := tx.Omit("Blocks").Create(set).Error; err != nil {
		return err
	}
	for _, block := range set.Blocks {
		if err := tx.Omit("Sources").Create(block).Error; err != nil {
			return err
		}
		if len(block.Sources) > 0 {
			if err := tx.Create(&block.Sources).Error; err != nil {
				return err
			}
		}
	}
	return nil
}

func (r *wikiProvenanceRepository) SaveStagedBlockSet(
	ctx context.Context, set *types.WikiPageBlockSet,
) error {
	if err := normalizeStagedBlockSet(set); err != nil {
		return err
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return createWikiBlockSetTree(tx, set)
	})
}

func (r *wikiProvenanceRepository) MarkStagedBlockSetFailed(
	ctx context.Context, kbID, blockSetID string,
) error {
	if kbID == "" || blockSetID == "" {
		return ErrWikiBlockSetNotFound
	}
	db := r.db.WithContext(ctx)
	result := db.Model(&types.WikiPageBlockSet{}).
		Where("knowledge_base_id = ? AND id = ? AND status = ?",
			kbID, blockSetID, types.WikiBlockSetStatusStaged).
		Update("status", types.WikiBlockSetStatusFailed)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 1 {
		return nil
	}

	// The conditional write above is the state-transition guard. This read is
	// only for a precise error; it must never broaden the update predicate.
	var set types.WikiPageBlockSet
	err := db.Select("id", "status").
		Where("knowledge_base_id = ? AND id = ?", kbID, blockSetID).
		First(&set).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return ErrWikiBlockSetNotFound
	}
	if err != nil {
		return err
	}
	return ErrWikiBlockSetNotStaged
}

func citationKey(source *types.WikiBlockSource) string {
	if source == nil {
		return ""
	}
	raw := fmt.Sprintf("%s\x00%d\x00%s\x00%d\x00%s",
		source.KnowledgeID, source.KnowledgeAttempt, source.ChunkID,
		source.ChunkRevision, source.EvidenceHash)
	sum := sha256.Sum256([]byte(raw))
	return fmt.Sprintf("src-%x", sum[:8])
}

func hydrateCitationKeys(set *types.WikiPageBlockSet) {
	if set == nil {
		return
	}
	for _, block := range set.Blocks {
		for _, source := range block.Sources {
			source.CitationKey = citationKey(source)
		}
	}
}

func getWikiBlockSet(db *gorm.DB, kbID, blockSetID string) (*types.WikiPageBlockSet, error) {
	if kbID == "" || blockSetID == "" {
		return nil, ErrWikiBlockSetNotFound
	}
	var set types.WikiPageBlockSet
	err := db.
		Preload("Blocks", func(q *gorm.DB) *gorm.DB {
			return q.Order("sort_order ASC").Order("id ASC")
		}).
		Preload("Blocks.Sources", func(q *gorm.DB) *gorm.DB {
			return q.Order("sort_order ASC").Order("id ASC")
		}).
		Where("knowledge_base_id = ? AND id = ?", kbID, blockSetID).
		First(&set).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrWikiBlockSetNotFound
	}
	if err != nil {
		return nil, err
	}
	hydrateCitationKeys(&set)
	return &set, nil
}

func (r *wikiProvenanceRepository) GetBlockSet(
	ctx context.Context, kbID, blockSetID string,
) (*types.WikiPageBlockSet, error) {
	return getWikiBlockSet(r.db.WithContext(ctx), kbID, blockSetID)
}

func (r *wikiProvenanceRepository) GetCurrentBlockSet(
	ctx context.Context, kbID, pageID string,
) (*types.WikiPageBlockSet, error) {
	if kbID == "" || pageID == "" {
		return nil, ErrWikiBlockSetNotFound
	}
	var page types.WikiPage
	err := r.db.WithContext(ctx).
		Select("id", "current_block_set_id").
		Where("knowledge_base_id = ? AND id = ?", kbID, pageID).
		First(&page).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrWikiBlockSetNotFound
	}
	if err != nil {
		return nil, err
	}
	if page.CurrentBlockSetID == "" {
		return nil, ErrWikiBlockSetNotFound
	}
	set, err := getWikiBlockSet(r.db.WithContext(ctx), kbID, page.CurrentBlockSetID)
	if err != nil {
		return nil, err
	}
	if set.PageID != page.ID {
		return nil, ErrWikiBlockSetConflict
	}
	return set, nil
}

func (r *wikiProvenanceRepository) ListBlockReferencesByKnowledge(
	ctx context.Context, kbID, knowledgeID string,
) ([]*types.WikiKnowledgeBlockReference, error) {
	if kbID == "" || knowledgeID == "" {
		return []*types.WikiKnowledgeBlockReference{}, nil
	}
	var refs []*types.WikiKnowledgeBlockReference
	err := r.db.WithContext(ctx).
		Table("wiki_block_sources AS s").
		Select(`p.id AS page_id, p.slug AS page_slug,
			bs.id AS block_set_id, b.id AS block_id,
			b.logical_block_id, b.block_type, b.content, b.author_type,
			b.provenance_status, s.id AS source_id, s.source_title,
			s.chunk_id, s.knowledge_attempt, s.chunk_revision,
			s.validation_status`).
		Joins("JOIN wiki_page_blocks AS b ON b.id = s.block_id").
		Joins(`JOIN wiki_page_block_sets AS bs
			ON bs.id = b.block_set_id
			AND bs.knowledge_base_id = s.knowledge_base_id
			AND bs.tenant_id = s.tenant_id`).
		Joins(`JOIN wiki_pages AS p
			ON p.id = bs.page_id
			AND p.current_block_set_id = bs.id
			AND p.knowledge_base_id = bs.knowledge_base_id
			AND p.tenant_id = bs.tenant_id`).
		Where("s.knowledge_base_id = ? AND s.knowledge_id = ?", kbID, knowledgeID).
		Where("bs.status = ? AND p.deleted_at IS NULL", types.WikiBlockSetStatusPublished).
		Order("p.id ASC, b.sort_order ASC, s.sort_order ASC, s.id ASC").
		Scan(&refs).Error
	if err != nil {
		return nil, err
	}
	if refs == nil {
		refs = []*types.WikiKnowledgeBlockReference{}
	}
	return refs, nil
}

func loadStagedWikiBlockSet(tx *gorm.DB, kbID, blockSetID string) (*types.WikiPageBlockSet, error) {
	var set types.WikiPageBlockSet
	err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("knowledge_base_id = ? AND id = ?", kbID, blockSetID).
		First(&set).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrWikiBlockSetNotFound
	}
	if err != nil {
		return nil, err
	}
	if set.Status != types.WikiBlockSetStatusStaged {
		return nil, ErrWikiBlockSetNotStaged
	}
	return &set, nil
}

func validateStagedWikiSourceAttempts(tx *gorm.DB, blockSetID string) error {
	var current struct {
		ID string `gorm:"column:id"`
	}
	err := tx.Model(&types.WikiPageBlockSet{}).
		Select("id").
		Where("id = ?", blockSetID).
		Where(wikiBlockSetCurrentSourceAttemptsSQL, blockSetID).
		Take(&current).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return ErrWikiBlockSourceAttemptConflict
	}
	return err
}

func lockWikiSourceAttemptGenerations(tx *gorm.DB, sources []*types.WikiBlockSource) error {
	knowledgeIDs := make(map[string]struct{})
	for _, source := range sources {
		if source != nil && source.KnowledgeAttempt > 0 && source.KnowledgeID != "" {
			knowledgeIDs[source.KnowledgeID] = struct{}{}
		}
	}
	ordered := make([]string, 0, len(knowledgeIDs))
	for knowledgeID := range knowledgeIDs {
		ordered = append(ordered, knowledgeID)
	}
	sort.Strings(ordered)
	for _, knowledgeID := range ordered {
		if err := lockKnowledgeAttemptGeneration(tx, knowledgeID); err != nil {
			return err
		}
	}
	return nil
}

func publishStagedWikiBlockSet(tx *gorm.DB, blockSetID string, publishedAt time.Time) error {
	result := tx.Model(&types.WikiPageBlockSet{}).
		Where("id = ? AND status = ?", blockSetID, types.WikiBlockSetStatusStaged).
		Where(wikiBlockSetCurrentSourceAttemptsSQL, blockSetID).
		Updates(map[string]interface{}{
			"status":       types.WikiBlockSetStatusPublished,
			"published_at": publishedAt,
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 1 {
		return nil
	}
	// Distinguish a generation conflict from another staged-set CAS failure.
	// This second check observes any attempt inserted during the transaction,
	// and the caller will roll back all page/revision/pointer writes on error.
	if err := validateStagedWikiSourceAttempts(tx, blockSetID); err != nil {
		return err
	}
	return ErrWikiBlockSetConflict
}

// validateStagedWikiBlockSources locks and validates every live chunk used by
// a staged block set. It must run in the same transaction as the page pointer
// switch: otherwise a chunk could be edited, disabled, or deleted after the
// service-level alignment check but before the Wiki snapshot is published.
//
// PostgreSQL row locks make publication and chunk mutation serializable at the
// source-row boundary. SQLite serializes the subsequent write transaction at
// database level, so the same validation still prevents publishing a snapshot
// that was already stale when this transaction observed it.
func validateStagedWikiBlockSources(tx *gorm.DB, set *types.WikiPageBlockSet) error {
	if set == nil {
		return ErrWikiBlockSetNotFound
	}

	var sources []*types.WikiBlockSource
	if err := tx.
		Table("wiki_block_sources AS s").
		Select("s.*").
		Joins("JOIN wiki_page_blocks AS b ON b.id = s.block_id").
		Where("b.block_set_id = ?", set.ID).
		Order("s.chunk_id ASC, s.id ASC").
		Scan(&sources).Error; err != nil {
		return err
	}
	if len(sources) == 0 {
		return nil
	}

	chunkIDs := make([]string, 0, len(sources))
	seen := make(map[string]struct{}, len(sources))
	hasTrackedAttempt := false
	for _, source := range sources {
		if source == nil || source.TenantID != set.TenantID ||
			source.KnowledgeBaseID != set.KnowledgeBaseID || source.ChunkID == "" ||
			source.KnowledgeID == "" || source.ValidationStatus != types.WikiSourceValidationLocated {
			return ErrWikiBlockSourceStale
		}
		if _, exists := seen[source.ChunkID]; exists {
			if source.KnowledgeAttempt > 0 {
				hasTrackedAttempt = true
			}
			continue
		}
		if source.KnowledgeAttempt > 0 {
			hasTrackedAttempt = true
		}
		seen[source.ChunkID] = struct{}{}
		chunkIDs = append(chunkIDs, source.ChunkID)
	}
	if hasTrackedAttempt {
		if err := lockWikiSourceAttemptGenerations(tx, sources); err != nil {
			return err
		}
		if err := validateStagedWikiSourceAttempts(tx, set.ID); err != nil {
			return err
		}
	}

	query := tx.Where(
		"tenant_id = ? AND knowledge_base_id = ? AND id IN ?",
		set.TenantID, set.KnowledgeBaseID, chunkIDs,
	).Order("id ASC")
	if tx.Dialector.Name() == "postgres" {
		query = query.Clauses(clause.Locking{Strength: "UPDATE"})
	}
	var chunks []*types.Chunk
	if err := query.Find(&chunks).Error; err != nil {
		return err
	}
	chunkByID := make(map[string]*types.Chunk, len(chunks))
	for _, chunk := range chunks {
		if chunk != nil {
			chunkByID[chunk.ID] = chunk
		}
	}

	for _, source := range sources {
		chunk := chunkByID[source.ChunkID]
		if chunk == nil || !chunk.IsEnabled ||
			chunk.TenantID != source.TenantID ||
			chunk.KnowledgeBaseID != source.KnowledgeBaseID ||
			chunk.KnowledgeID != source.KnowledgeID ||
			!types.IsWikiProvenanceChunkType(chunk.ChunkType) ||
			chunk.ContentRevision != source.ChunkRevision {
			return ErrWikiBlockSourceStale
		}
		digest := sha256.Sum256([]byte(chunk.Content))
		if source.ChunkContentHash == "" || source.ChunkContentHash != fmt.Sprintf("%x", digest[:]) {
			return ErrWikiBlockSourceStale
		}
	}
	return nil
}

func (r *wikiProvenanceRepository) CreatePageWithBlockSet(
	ctx context.Context, page *types.WikiPage, blockSetID string,
) error {
	if page == nil || page.ID == "" || page.KnowledgeBaseID == "" || blockSetID == "" {
		return errors.New("wiki page and staged block set are required")
	}
	originalVersion := page.Version
	originalBlockSetID := page.CurrentBlockSetID
	originalContent := page.Content
	originalSummary := page.Summary
	originalCreatedAt := page.CreatedAt
	originalUpdatedAt := page.UpdatedAt

	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		set, err := loadStagedWikiBlockSet(tx, page.KnowledgeBaseID, blockSetID)
		if err != nil {
			return err
		}
		if set.PageID != page.ID || set.TenantID != page.TenantID {
			return ErrWikiBlockSetConflict
		}
		if page.Version < 1 {
			page.Version = 1
		}
		if set.PageVersion != page.Version {
			return ErrWikiBlockSetConflict
		}
		if err := validateStagedWikiBlockSources(tx, set); err != nil {
			return err
		}

		now := time.Now()
		page.CurrentBlockSetID = set.ID
		page.Content = set.RenderedContent
		page.Summary = set.RenderedSummary
		if page.CreatedAt.IsZero() {
			page.CreatedAt = now
		}
		if page.UpdatedAt.IsZero() {
			page.UpdatedAt = now
		}
		if err := tx.Create(page).Error; err != nil {
			return err
		}
		return publishStagedWikiBlockSet(tx, set.ID, now)
	})
	if err != nil {
		page.Version = originalVersion
		page.CurrentBlockSetID = originalBlockSetID
		page.Content = originalContent
		page.Summary = originalSummary
		page.CreatedAt = originalCreatedAt
		page.UpdatedAt = originalUpdatedAt
	}
	return err
}

func revisionFromStoredWikiPage(page *types.WikiPage) *types.WikiPageRevision {
	return &types.WikiPageRevision{
		ID:              uuid.NewString(),
		TenantID:        page.TenantID,
		KnowledgeBaseID: page.KnowledgeBaseID,
		PageID:          page.ID,
		Slug:            page.Slug,
		Version:         page.Version,
		Title:           page.Title,
		PageType:        page.PageType,
		Status:          page.Status,
		Content:         page.Content,
		Summary:         page.Summary,
		Aliases:         append(types.StringArray(nil), page.Aliases...),
		BlockSetID:      page.CurrentBlockSetID,
		EditSource:      types.NormalizeWikiEditSource(page.LastEditSource),
		EditorID:        page.LastEditorID,
		EditedAt:        page.UpdatedAt,
		CreatedAt:       time.Now(),
	}
}

func (r *wikiProvenanceRepository) PublishStagedBlockSet(
	ctx context.Context, page *types.WikiPage, blockSetID string,
) error {
	if page == nil || page.ID == "" || page.KnowledgeBaseID == "" || blockSetID == "" {
		return errors.New("wiki page and staged block set are required")
	}
	expectedVersion := page.Version
	originalBlockSetID := page.CurrentBlockSetID
	originalContent := page.Content
	originalSummary := page.Summary
	originalUpdatedAt := page.UpdatedAt

	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		set, err := loadStagedWikiBlockSet(tx, page.KnowledgeBaseID, blockSetID)
		if err != nil {
			return err
		}

		var current types.WikiPage
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("knowledge_base_id = ? AND id = ?", page.KnowledgeBaseID, page.ID).
			First(&current).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrWikiPageNotFound
			}
			return err
		}
		if current.Version != expectedVersion {
			return ErrWikiPageConflict
		}
		if current.TenantID != page.TenantID || current.Slug != page.Slug || set.PageID != current.ID ||
			set.TenantID != current.TenantID || set.PageVersion != current.Version+1 {
			return ErrWikiBlockSetConflict
		}
		if err := validateStagedWikiBlockSources(tx, set); err != nil {
			return err
		}

		revision := revisionFromStoredWikiPage(&current)
		if err := tx.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "page_id"}, {Name: "version"}},
			DoNothing: true,
		}).Create(revision).Error; err != nil {
			return err
		}

		page.Content = set.RenderedContent
		page.Summary = set.RenderedSummary
		page.CurrentBlockSetID = set.ID
		if page.UpdatedAt.IsZero() || !page.UpdatedAt.After(current.UpdatedAt) {
			page.UpdatedAt = time.Now()
		}
		if err := updateWikiPageRow(tx, page); err != nil {
			return err
		}
		// Keep this explicit write even though updateWikiPageRow knows about the
		// column: it makes the atomic pointer switch self-contained if the legacy
		// update projection changes independently later.
		if err := tx.Model(&types.WikiPage{}).
			Where("id = ? AND version = ?", page.ID, page.Version).
			Update("current_block_set_id", set.ID).Error; err != nil {
			return err
		}

		if current.CurrentBlockSetID != "" && current.CurrentBlockSetID != set.ID {
			if err := tx.Model(&types.WikiPageBlockSet{}).
				Where("id = ? AND page_id = ?", current.CurrentBlockSetID, current.ID).
				Update("status", types.WikiBlockSetStatusSuperseded).Error; err != nil {
				return err
			}
		}
		return publishStagedWikiBlockSet(tx, set.ID, time.Now())
	})
	if err != nil {
		page.Version = expectedVersion
		page.CurrentBlockSetID = originalBlockSetID
		page.Content = originalContent
		page.Summary = originalSummary
		page.UpdatedAt = originalUpdatedAt
	}
	return err
}

func (r *wikiProvenanceRepository) CloneBlockSetToStaged(
	ctx context.Context, sourceBlockSetID string, target *types.WikiPageBlockSet,
) error {
	if sourceBlockSetID == "" || target == nil {
		return errors.New("source and target wiki block sets are required")
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var source types.WikiPageBlockSet
		err := tx.
			Preload("Blocks", func(q *gorm.DB) *gorm.DB { return q.Order("sort_order ASC") }).
			Preload("Blocks.Sources", func(q *gorm.DB) *gorm.DB { return q.Order("sort_order ASC") }).
			Where("id = ?", sourceBlockSetID).
			First(&source).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrWikiBlockSetNotFound
		}
		if err != nil {
			return err
		}
		if target.TenantID == 0 {
			target.TenantID = source.TenantID
		}
		if target.KnowledgeBaseID == "" {
			target.KnowledgeBaseID = source.KnowledgeBaseID
		}
		if target.PageID == "" {
			target.PageID = source.PageID
		}
		if target.TenantID != source.TenantID || target.KnowledgeBaseID != source.KnowledgeBaseID ||
			target.PageID != source.PageID {
			return ErrWikiBlockSetConflict
		}
		target.Status = types.WikiBlockSetStatusStaged
		target.RenderedContent = source.RenderedContent
		target.RenderedSummary = source.RenderedSummary
		target.Blocks = make([]*types.WikiPageBlock, 0, len(source.Blocks))
		for _, oldBlock := range source.Blocks {
			block := &types.WikiPageBlock{
				LogicalBlockID:   oldBlock.LogicalBlockID,
				BlockType:        oldBlock.BlockType,
				SectionPath:      append(types.StringArray(nil), oldBlock.SectionPath...),
				Content:          oldBlock.Content,
				ContentHash:      oldBlock.ContentHash,
				AuthorType:       oldBlock.AuthorType,
				ProvenanceStatus: oldBlock.ProvenanceStatus,
				Sources:          make([]*types.WikiBlockSource, 0, len(oldBlock.Sources)),
			}
			for _, oldSource := range oldBlock.Sources {
				block.Sources = append(block.Sources, &types.WikiBlockSource{
					KnowledgeID:      oldSource.KnowledgeID,
					DocumentTitle:    oldSource.DocumentTitle,
					KnowledgeAttempt: oldSource.KnowledgeAttempt,
					ChunkID:          oldSource.ChunkID,
					ChunkRevision:    oldSource.ChunkRevision,
					Evidence:         oldSource.Evidence,
					EvidenceHash:     oldSource.EvidenceHash,
					ChunkContentHash: oldSource.ChunkContentHash,
					ValidationStatus: oldSource.ValidationStatus,
				})
			}
			target.Blocks = append(target.Blocks, block)
		}
		if err := normalizeStagedBlockSet(target); err != nil {
			return err
		}
		return createWikiBlockSetTree(tx, target)
	})
}

func (r *wikiProvenanceRepository) RemoveKnowledgeSourcesFromStaged(
	ctx context.Context, kbID, blockSetID, knowledgeID string,
) ([]string, error) {
	if kbID == "" || blockSetID == "" || knowledgeID == "" {
		return []string{}, nil
	}
	var orphanBlockIDs []string
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if _, err := loadStagedWikiBlockSet(tx, kbID, blockSetID); err != nil {
			return err
		}

		var affectedBlockIDs []string
		if err := tx.Table("wiki_block_sources AS s").
			Distinct("s.block_id").
			Joins("JOIN wiki_page_blocks AS b ON b.id = s.block_id").
			Where("b.block_set_id = ? AND s.knowledge_id = ?", blockSetID, knowledgeID).
			Order("s.block_id ASC").
			Pluck("s.block_id", &affectedBlockIDs).Error; err != nil {
			return err
		}
		if len(affectedBlockIDs) == 0 {
			orphanBlockIDs = []string{}
			return nil
		}
		if err := tx.Where("block_id IN ? AND knowledge_id = ?", affectedBlockIDs, knowledgeID).
			Delete(&types.WikiBlockSource{}).Error; err != nil {
			return err
		}

		var stillSourced []string
		if err := tx.Model(&types.WikiBlockSource{}).
			Distinct("block_id").
			Where("block_id IN ?", affectedBlockIDs).
			Pluck("block_id", &stillSourced).Error; err != nil {
			return err
		}
		hasSource := make(map[string]struct{}, len(stillSourced))
		for _, id := range stillSourced {
			hasSource[id] = struct{}{}
		}
		orphanBlockIDs = make([]string, 0, len(affectedBlockIDs))
		for _, id := range affectedBlockIDs {
			if _, ok := hasSource[id]; !ok {
				orphanBlockIDs = append(orphanBlockIDs, id)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return orphanBlockIDs, nil
}

func uniqueNonEmptyStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

func (r *wikiProvenanceRepository) DeleteBlocksFromStaged(
	ctx context.Context, kbID, blockSetID string, blockIDs []string,
) error {
	blockIDs = uniqueNonEmptyStrings(blockIDs)
	if len(blockIDs) == 0 {
		return nil
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if _, err := loadStagedWikiBlockSet(tx, kbID, blockSetID); err != nil {
			return err
		}
		var ownedIDs []string
		if err := tx.Model(&types.WikiPageBlock{}).
			Where("block_set_id = ? AND id IN ?", blockSetID, blockIDs).
			Pluck("id", &ownedIDs).Error; err != nil {
			return err
		}
		if len(ownedIDs) != len(blockIDs) {
			return ErrWikiBlockSetConflict
		}
		if err := tx.Where("block_id IN ?", ownedIDs).Delete(&types.WikiBlockSource{}).Error; err != nil {
			return err
		}
		return tx.Where("block_set_id = ? AND id IN ?", blockSetID, ownedIDs).
			Delete(&types.WikiPageBlock{}).Error
	})
}

func (r *wikiProvenanceRepository) UpdateStagedBlockSetRender(
	ctx context.Context, kbID, blockSetID, content, summary string,
) error {
	if kbID == "" || blockSetID == "" {
		return ErrWikiBlockSetNotFound
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if _, err := loadStagedWikiBlockSet(tx, kbID, blockSetID); err != nil {
			return err
		}
		return tx.Model(&types.WikiPageBlockSet{}).
			Where("knowledge_base_id = ? AND id = ?", kbID, blockSetID).
			Updates(map[string]interface{}{
				"rendered_content": content,
				"rendered_summary": summary,
			}).Error
	})
}

func (r *wikiProvenanceRepository) DeleteBlockSetsByPage(ctx context.Context, pageID string) error {
	if pageID == "" {
		return nil
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return deleteWikiBlockSetsByPageTx(tx, pageID)
	})
}

func deleteWikiBlockSetsByPageTx(tx *gorm.DB, pageID string) error {
	var setIDs []string
	if err := tx.Model(&types.WikiPageBlockSet{}).
		Where("page_id = ?", pageID).
		Pluck("id", &setIDs).Error; err != nil {
		return err
	}
	if len(setIDs) == 0 {
		return nil
	}
	var blockIDs []string
	if err := tx.Model(&types.WikiPageBlock{}).
		Where("block_set_id IN ?", setIDs).
		Pluck("id", &blockIDs).Error; err != nil {
		return err
	}
	if len(blockIDs) > 0 {
		if err := tx.Where("block_id IN ?", blockIDs).
			Delete(&types.WikiBlockSource{}).Error; err != nil {
			return err
		}
	}
	if err := tx.Where("block_set_id IN ?", setIDs).
		Delete(&types.WikiPageBlock{}).Error; err != nil {
		return err
	}
	return tx.Where("page_id = ?", pageID).Delete(&types.WikiPageBlockSet{}).Error
}

func (r *wikiProvenanceRepository) DeletePageIfVersion(
	ctx context.Context, kbID, pageID string, expectedVersion int,
) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var page types.WikiPage
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("id = ? AND knowledge_base_id = ? AND version = ?", pageID, kbID, expectedVersion).
			First(&page).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrWikiBlockSetConflict
			}
			return err
		}

		result := tx.Where(
			"id = ? AND knowledge_base_id = ? AND version = ?", pageID, kbID, expectedVersion,
		).Delete(&types.WikiPage{})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrWikiBlockSetConflict
		}
		if err := tx.Where("page_id = ?", pageID).Delete(&types.WikiPageRevision{}).Error; err != nil {
			return err
		}
		if err := deleteWikiBlockSetsByPageTx(tx, pageID); err != nil {
			return err
		}
		return tx.Where(
			"tenant_id = ? AND knowledge_base_id = ? AND id = ?", page.TenantID, kbID, "wp-"+pageID,
		).Delete(&types.Chunk{}).Error
	})
}
