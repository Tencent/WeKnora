package repository

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/Tencent/WeKnora/internal/common"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"gorm.io/gorm"
)

// chunkRepository implements the ChunkRepository interface
type chunkRepository struct {
	db *gorm.DB
}

// NewChunkRepository creates a new chunk repository
func NewChunkRepository(db *gorm.DB) interfaces.ChunkRepository {
	return &chunkRepository{db: db}
}

// CreateChunks creates multiple chunks in batches
func (r *chunkRepository) CreateChunks(ctx context.Context, chunks []*types.Chunk) error {
	for _, chunk := range chunks {
		chunk.Content = common.CleanInvalidUTF8(chunk.Content)
	}
	// Use Select("*") to ensure all fields including zero values (IsEnabled=false, Flags=0)
	// are inserted, bypassing GORM's default value behavior for zero values
	return r.db.WithContext(ctx).Select("*").CreateInBatches(chunks, 100).Error
}

// GetChunkByID retrieves a chunk by its ID and tenant ID
func (r *chunkRepository) GetChunkByID(ctx context.Context, tenantID uint64, id string) (*types.Chunk, error) {
	var chunk types.Chunk
	if err := r.db.WithContext(ctx).Where("tenant_id = ? AND id = ?", tenantID, id).First(&chunk).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errors.New("chunk not found")
		}
		return nil, err
	}
	return &chunk, nil
}

// ListChunksByID retrieves multiple chunks by their IDs
func (r *chunkRepository) ListChunksByID(
	ctx context.Context, tenantID uint64, ids []string,
) ([]*types.Chunk, error) {
	var chunks []*types.Chunk
	if err := r.db.WithContext(ctx).
		Where("tenant_id = ? AND id IN ?", tenantID, ids).
		Find(&chunks).Error; err != nil {
		return nil, err
	}
	return chunks, nil
}

// ListChunksByKnowledgeID lists all chunks for a knowledge ID
func (r *chunkRepository) ListChunksByKnowledgeID(
	ctx context.Context, tenantID uint64, knowledgeID string,
) ([]*types.Chunk, error) {
	var chunks []*types.Chunk
	if err := r.db.WithContext(ctx).
		Where("tenant_id = ? AND knowledge_id = ? and chunk_type = ?", tenantID, knowledgeID, "text").
		Order("chunk_index ASC").
		Find(&chunks).Error; err != nil {
		return nil, err
	}
	return chunks, nil
}

// ListPagedChunksByKnowledgeID lists chunks for a knowledge ID with pagination
func (r *chunkRepository) ListPagedChunksByKnowledgeID(
	ctx context.Context,
	tenantID uint64,
	knowledgeID string,
	page *types.Pagination,
	chunkType []types.ChunkType,
	tagID string,
	keyword string,
	searchField string,
	sortOrder string,
) ([]*types.Chunk, int64, error) {
	var chunks []*types.Chunk
	var total int64
	keyword = strings.TrimSpace(keyword)

	baseFilter := func(db *gorm.DB) *gorm.DB {
		db = db.Where("tenant_id = ? AND knowledge_id = ? AND chunk_type IN (?) AND status in (?)",
			tenantID, knowledgeID, chunkType, []int{int(types.ChunkStatusIndexed), int(types.ChunkStatusDefault)})
		if tagID == types.UntaggedTagID {
			// Special value to filter entries without a tag
			db = db.Where("tag_id = ''")
		} else if tagID != "" {
			db = db.Where("tag_id = ?", tagID)
		}
		if keyword != "" {
			like := "%" + keyword + "%"
			// 根据数据库类型使用不同的 JSON 查询语法
			isPostgres := db.Dialector.Name() == "postgres"

			switch searchField {
			case "standard_question":
				// Search only in standard_question field of metadata
				if isPostgres {
					db = db.Where("metadata->>'standard_question' ILIKE ?", like)
				} else {
					// MySQL: metadata->>'$.standard_question' (MySQL 5.7.13+)
					// 也可以用 JSON_UNQUOTE(JSON_EXTRACT(metadata, '$.standard_question'))
					db = db.Where("metadata->>'$.standard_question' LIKE ?", like)
				}
			case "similar_questions":
				// Search in similar_questions array of metadata
				if isPostgres {
					db = db.Where("metadata->'similar_questions'::text ILIKE ?", like)
				} else {
					db = db.Where("JSON_EXTRACT(metadata, '$.similar_questions') LIKE ?", like)
				}
			case "answers":
				// Search in answers array of metadata
				if isPostgres {
					db = db.Where("metadata->'answers'::text ILIKE ?", like)
				} else {
					db = db.Where("JSON_EXTRACT(metadata, '$.answers') LIKE ?", like)
				}
			default:
				// Search in all fields (content and metadata)
				if isPostgres {
					db = db.Where("(content ILIKE ? OR metadata::text ILIKE ?)", like, like)
				} else {
					db = db.Where("(content LIKE ? OR CAST(metadata AS CHAR) LIKE ?)", like, like)
				}
			}
		}
		return db
	}

	query := baseFilter(r.db.WithContext(ctx).Model(&types.Chunk{}))

	// First query the total count
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	// Then query the paginated data
	dataQuery := baseFilter(r.db.WithContext(ctx))

	// Default is time descending, "asc" for time ascending
	orderClause := "updated_at DESC"
	if sortOrder == "asc" {
		orderClause = "updated_at ASC"
	}

	if err := dataQuery.
		Order(orderClause).
		Offset(page.Offset()).
		Limit(page.Limit()).
		Find(&chunks).Error; err != nil {
		return nil, 0, err
	}

	return chunks, total, nil
}

func (r *chunkRepository) ListChunkByParentID(
	ctx context.Context,
	tenantID uint64,
	parentID string,
) ([]*types.Chunk, error) {
	var chunks []*types.Chunk
	if err := r.db.WithContext(ctx).
		Where("tenant_id = ? AND parent_chunk_id = ?", tenantID, parentID).
		Find(&chunks).Error; err != nil {
		return nil, err
	}
	return chunks, nil
}

// UpdateChunk updates a chunk using GORM Save, which updates ALL fields.
// Note: This will update all fields including metadata and content_hash.
// Make sure the chunk object is complete (e.g., fetched from DB) before calling this method.
func (r *chunkRepository) UpdateChunk(ctx context.Context, chunk *types.Chunk) error {
	return r.db.WithContext(ctx).Save(chunk).Error
}

// UpdateChunks updates chunks in batch using raw SQL for efficiency.
// Uses raw SQL to bypass GORM's default value handling for boolean fields.
//
// IMPORTANT: This method only updates the following fields:
//   - content
//   - is_enabled
//   - tag_id
//   - flags
//   - status
//   - updated_at
//
// Fields NOT updated by this method (will retain their original values):
//   - metadata
//   - content_hash
//   - embedding-related fields
//   - other fields not listed above
//
// If you need to update metadata or content_hash, use UpdateChunk (single) instead.
func (r *chunkRepository) UpdateChunks(ctx context.Context, chunks []*types.Chunk) error {
	if len(chunks) == 0 {
		return nil
	}

	// Build batch update SQL with CASE expressions
	var ids []string
	contentCases := make([]string, 0, len(chunks))
	isEnabledCases := make([]string, 0, len(chunks))
	tagIDCases := make([]string, 0, len(chunks))
	flagsCases := make([]string, 0, len(chunks))
	statusCases := make([]string, 0, len(chunks))

	var contentArgs []interface{}
	var isEnabledArgs []interface{}
	var tagIDArgs []interface{}
	var flagsArgs []interface{}
	var statusArgs []interface{}

	for _, chunk := range chunks {
		ids = append(ids, chunk.ID)
		content := common.CleanInvalidUTF8(chunk.Content)

		contentCases = append(contentCases, "WHEN id = ? THEN ?")
		contentArgs = append(contentArgs, chunk.ID, content)

		// Convert bool to string for PostgreSQL compatibility
		isEnabledStr := "false"
		if chunk.IsEnabled {
			isEnabledStr = "true"
		}
		isEnabledCases = append(isEnabledCases, "WHEN id = ? THEN ?")
		isEnabledArgs = append(isEnabledArgs, chunk.ID, isEnabledStr)

		tagIDCases = append(tagIDCases, "WHEN id = ? THEN ?")
		tagIDArgs = append(tagIDArgs, chunk.ID, chunk.TagID)

		flagsCases = append(flagsCases, "WHEN id = ? THEN ?")
		flagsArgs = append(flagsArgs, chunk.ID, fmt.Sprintf("%d", chunk.Flags))

		statusCases = append(statusCases, "WHEN id = ? THEN ?")
		statusArgs = append(statusArgs, chunk.ID, fmt.Sprintf("%d", chunk.Status))
	}

	// Build IN clause placeholders
	inPlaceholders := make([]string, len(ids))
	for i := range ids {
		inPlaceholders[i] = "?"
	}

	// Combine args in correct order: content, is_enabled, tag_id, flags, status, then IN clause
	var args []interface{}
	args = append(args, contentArgs...)
	args = append(args, isEnabledArgs...)
	args = append(args, tagIDArgs...)
	args = append(args, flagsArgs...)
	args = append(args, statusArgs...)
	for _, id := range ids {
		args = append(args, id)
	}

	sql := fmt.Sprintf(`
		UPDATE chunks SET
			content = CASE %s END,
			is_enabled = (CASE %s END)::boolean,
			tag_id = CASE %s END,
			flags = (CASE %s END)::integer,
			status = (CASE %s END)::integer,
			updated_at = NOW()
		WHERE id IN (%s)
	`,
		strings.Join(contentCases, " "),
		strings.Join(isEnabledCases, " "),
		strings.Join(tagIDCases, " "),
		strings.Join(flagsCases, " "),
		strings.Join(statusCases, " "),
		strings.Join(inPlaceholders, ","),
	)

	return r.db.WithContext(ctx).Exec(sql, args...).Error
}

// DeleteChunk deletes a chunk by its ID
func (r *chunkRepository) DeleteChunk(ctx context.Context, tenantID uint64, id string) error {
	return r.db.WithContext(ctx).Where("tenant_id = ? AND id = ?", tenantID, id).Delete(&types.Chunk{}).Error
}

// DeleteChunks deletes chunks by IDs in batch
func (r *chunkRepository) DeleteChunks(ctx context.Context, tenantID uint64, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	return r.db.WithContext(ctx).Where("tenant_id = ? AND id IN ?", tenantID, ids).Delete(&types.Chunk{}).Error
}

// DeleteChunksByKnowledgeID deletes all chunks for a knowledge ID
func (r *chunkRepository) DeleteChunksByKnowledgeID(ctx context.Context, tenantID uint64, knowledgeID string) error {
	return r.db.WithContext(ctx).Where(
		"tenant_id = ? AND knowledge_id = ?", tenantID, knowledgeID,
	).Delete(&types.Chunk{}).Error
}

// DeleteByKnowledgeList deletes all chunks for a knowledge list
func (r *chunkRepository) DeleteByKnowledgeList(ctx context.Context, tenantID uint64, knowledgeIDs []string) error {
	return r.db.WithContext(ctx).Where(
		"tenant_id = ? AND knowledge_id in ?", tenantID, knowledgeIDs,
	).Delete(&types.Chunk{}).Error
}

// DeleteChunksByTagID deletes all chunks with the specified tag ID
// Returns the IDs of deleted chunks for index cleanup
func (r *chunkRepository) DeleteChunksByTagID(ctx context.Context, tenantID uint64, kbID string, tagID string, excludeIDs []string) ([]string, error) {
	// Build exclude set for O(1) lookup
	excludeSet := make(map[string]struct{}, len(excludeIDs))
	for _, id := range excludeIDs {
		excludeSet[id] = struct{}{}
	}

	// Get all chunk IDs for this tag
	var allIDs []string
	if err := r.db.WithContext(ctx).Model(&types.Chunk{}).
		Where("tenant_id = ? AND knowledge_base_id = ? AND tag_id = ?", tenantID, kbID, tagID).
		Pluck("id", &allIDs).Error; err != nil {
		return nil, err
	}

	// Filter out excluded IDs
	toDelete := make([]string, 0, len(allIDs))
	for _, id := range allIDs {
		if _, excluded := excludeSet[id]; !excluded {
			toDelete = append(toDelete, id)
		}
	}

	if len(toDelete) == 0 {
		return nil, nil
	}

	// Delete in batches
	const batchSize = 1000
	for i := 0; i < len(toDelete); i += batchSize {
		end := i + batchSize
		if end > len(toDelete) {
			end = len(toDelete)
		}
		batch := toDelete[i:end]

		if err := r.db.WithContext(ctx).Where("id IN ?", batch).Delete(&types.Chunk{}).Error; err != nil {
			// Return already planned deletions up to this point for index cleanup
			return toDelete[:i], err
		}
	}

	return toDelete, nil
}

// CountChunksByKnowledgeBaseID counts the number of chunks in a knowledge base
func (r *chunkRepository) CountChunksByKnowledgeBaseID(
	ctx context.Context,
	tenantID uint64,
	kbID string,
) (int64, error) {
	var count int64
	err := r.db.WithContext(ctx).Model(&types.Chunk{}).
		Where("tenant_id = ? AND knowledge_base_id = ?", tenantID, kbID).
		Count(&count).Error
	return count, err
}

// DeleteUnindexedChunks by knowledge id and chunk index range
func (r *chunkRepository) DeleteUnindexedChunks(
	ctx context.Context,
	tenantID uint64,
	knowledgeID string,
) ([]*types.Chunk, error) {
	var chunks []*types.Chunk
	if err := r.db.WithContext(ctx).
		Where("tenant_id = ? AND knowledge_id = ? AND status = ?", tenantID, knowledgeID, types.ChunkStatusStored).
		Find(&chunks).Error; err != nil {
		return nil, err
	}
	if len(chunks) > 0 {
		if err := r.db.WithContext(ctx).
			Where("tenant_id = ? AND knowledge_id = ? AND status = ?", tenantID, knowledgeID, types.ChunkStatusStored).
			Delete(&types.Chunk{}).Error; err != nil {
			return nil, err
		}
	}
	return chunks, nil
}

// ListAllFAQChunksByKnowledgeID lists all FAQ chunks for a knowledge ID (only essential fields for efficiency)
// Uses batch query to handle large datasets
func (r *chunkRepository) ListAllFAQChunksByKnowledgeID(
	ctx context.Context,
	tenantID uint64,
	knowledgeID string,
) ([]*types.Chunk, error) {
	const batchSize = 1000 // 每批查询1000条
	var allChunks []*types.Chunk
	offset := 0

	for {
		var batchChunks []*types.Chunk
		if err := r.db.WithContext(ctx).
			Select("id, content_hash").
			Where("tenant_id = ? AND knowledge_id = ? AND chunk_type = ?", tenantID, knowledgeID, types.ChunkTypeFAQ).
			Offset(offset).
			Limit(batchSize).
			Find(&batchChunks).Error; err != nil {
			return nil, err
		}

		// 如果没有查询到数据，说明已经查询完毕
		if len(batchChunks) == 0 {
			break
		}

		allChunks = append(allChunks, batchChunks...)

		// 如果返回的数据少于批次大小，说明已经是最后一批
		if len(batchChunks) < batchSize {
			break
		}

		offset += batchSize
	}

	return allChunks, nil
}

// ListAllFAQChunksWithMetadataByKnowledgeBaseID lists all FAQ chunks for a knowledge base ID
// Returns ID and Metadata fields for duplicate question checking
// Uses batch query to handle large datasets
func (r *chunkRepository) ListAllFAQChunksWithMetadataByKnowledgeBaseID(
	ctx context.Context,
	tenantID uint64,
	kbID string,
) ([]*types.Chunk, error) {
	const batchSize = 1000 // 每批查询1000条
	var allChunks []*types.Chunk
	offset := 0

	for {
		var batchChunks []*types.Chunk
		if err := r.db.WithContext(ctx).
			Select("id, metadata").
			Where("tenant_id = ? AND knowledge_base_id = ? AND chunk_type = ? AND status = ?",
				tenantID, kbID, types.ChunkTypeFAQ, types.ChunkStatusIndexed).
			Offset(offset).
			Limit(batchSize).
			Find(&batchChunks).Error; err != nil {
			return nil, err
		}

		// 如果没有查询到数据，说明已经查询完毕
		if len(batchChunks) == 0 {
			break
		}

		allChunks = append(allChunks, batchChunks...)

		// 如果返回的数据少于批次大小，说明已经是最后一批
		if len(batchChunks) < batchSize {
			break
		}

		offset += batchSize
	}

	return allChunks, nil
}

// ListAllFAQChunksForExport lists all FAQ chunks for export with full metadata, tag_id, is_enabled, and flags.
// Uses batch query to handle large datasets.
func (r *chunkRepository) ListAllFAQChunksForExport(
	ctx context.Context,
	tenantID uint64,
	knowledgeID string,
) ([]*types.Chunk, error) {
	const batchSize = 1000 // 每批查询1000条
	var allChunks []*types.Chunk
	offset := 0

	for {
		var batchChunks []*types.Chunk
		if err := r.db.WithContext(ctx).
			Select("id, metadata, tag_id, is_enabled, flags").
			Where("tenant_id = ? AND knowledge_id = ? AND chunk_type = ? AND status = ?",
				tenantID, knowledgeID, types.ChunkTypeFAQ, types.ChunkStatusIndexed).
			Order("created_at ASC").
			Offset(offset).
			Limit(batchSize).
			Find(&batchChunks).Error; err != nil {
			return nil, err
		}

		// 如果没有查询到数据，说明已经查询完毕
		if len(batchChunks) == 0 {
			break
		}

		allChunks = append(allChunks, batchChunks...)

		// 如果返回的数据少于批次大小，说明已经是最后一批
		if len(batchChunks) < batchSize {
			break
		}

		offset += batchSize
	}

	return allChunks, nil
}

// UpdateChunkFlagsBatch updates flags for multiple chunks in batch using SQL CASE expressions.
// This is more efficient than updating chunks one by one.
// setFlags: map of chunk ID to flags to set (OR operation)
// clearFlags: map of chunk ID to flags to clear (AND NOT operation)
func (r *chunkRepository) UpdateChunkFlagsBatch(
	ctx context.Context,
	tenantID uint64,
	kbID string,
	setFlags map[string]types.ChunkFlags,
	clearFlags map[string]types.ChunkFlags,
) error {
	if len(setFlags) == 0 && len(clearFlags) == 0 {
		return nil
	}

	// Collect all IDs
	allIDs := make([]string, 0, len(setFlags)+len(clearFlags))
	for id := range setFlags {
		allIDs = append(allIDs, id)
	}
	for id := range clearFlags {
		if _, exists := setFlags[id]; !exists {
			allIDs = append(allIDs, id)
		}
	}

	if len(allIDs) == 0 {
		return nil
	}

	// Build CASE expression for flags update
	// flags = (flags | setFlag) & ~clearFlag
	var setCases, clearCases []string
	var args []interface{}

	// Build SET cases: flags | value
	for id, flag := range setFlags {
		setCases = append(setCases, "WHEN id = ? THEN ?")
		args = append(args, id, int(flag))
	}

	// Build CLEAR cases: flags & ~value
	for id, flag := range clearFlags {
		clearCases = append(clearCases, "WHEN id = ? THEN ?")
		args = append(args, id, int(flag))
	}

	setExpr := "0"
	clearExpr := "0"

	if len(setCases) > 0 {
		setExpr = fmt.Sprintf("CASE %s ELSE 0 END", strings.Join(setCases, " "))
	}

	if len(clearCases) > 0 {
		clearExpr = fmt.Sprintf("CASE %s ELSE 0 END", strings.Join(clearCases, " "))
	}

	// Build IN clause placeholders manually for raw SQL
	inPlaceholders := make([]string, len(allIDs))
	for i := range allIDs {
		inPlaceholders[i] = "?"
	}

	sql := fmt.Sprintf(`
	UPDATE chunks 
    SET flags = (flags | (%s)) & ~(%s),
        updated_at = NOW()
    WHERE tenant_id = ? 
      AND knowledge_base_id = ?
      AND id IN (%s)
`, setExpr, clearExpr, strings.Join(inPlaceholders, ","))

	args = append(args, tenantID, kbID)
	for _, id := range allIDs {
		args = append(args, id)
	}

	return r.db.WithContext(ctx).Exec(sql, args...).Error
}

// UpdateChunkFieldsByTagID updates fields for all chunks with the specified tag ID.
// Returns the list of affected chunk IDs for syncing with retriever engines.
// newTagID: if not nil, updates tag_id to this value (empty string means uncategorized)
func (r *chunkRepository) UpdateChunkFieldsByTagID(
	ctx context.Context,
	tenantID uint64,
	kbID string,
	tagID string,
	isEnabled *bool,
	setFlags types.ChunkFlags,
	clearFlags types.ChunkFlags,
	newTagID *string,
	excludeIDs []string,
) ([]string, error) {
	// First, get the IDs of chunks that will be affected (for is_enabled sync)
	var affectedIDs []string
	if isEnabled != nil {
		var chunks []*types.Chunk
		query := r.db.WithContext(ctx).
			Select("id").
			Where("tenant_id = ? AND knowledge_base_id = ? AND chunk_type = ?",
				tenantID, kbID, types.ChunkTypeFAQ)
		if tagID == "" || tagID == types.UntaggedTagID {
			query = query.Where("(tag_id = '' OR tag_id IS NULL)")
		} else {
			query = query.Where("tag_id = ?", tagID)
		}

		if len(excludeIDs) > 0 {
			query = query.Where("id NOT IN ?", excludeIDs)
		}

		// Only get chunks that need to change
		query = query.Where("is_enabled != ?", *isEnabled)
		if err := query.Find(&chunks).Error; err != nil {
			return nil, err
		}
		for _, c := range chunks {
			affectedIDs = append(affectedIDs, c.ID)
		}
	}

	// Build update query
	updates := map[string]interface{}{
		"updated_at": "NOW()",
	}

	if isEnabled != nil {
		updates["is_enabled"] = *isEnabled
	}

	// Handle newTagID update (__untagged__ is stored as empty string)
	if newTagID != nil {
		if *newTagID == types.UntaggedTagID || *newTagID == "" {
			updates["tag_id"] = ""
		} else {
			updates["tag_id"] = *newTagID
		}
	}

	query := r.db.WithContext(ctx).Model(&types.Chunk{}).
		Where("tenant_id = ? AND knowledge_base_id = ? AND chunk_type = ?",
			tenantID, kbID, types.ChunkTypeFAQ)

	if tagID == "" || tagID == types.UntaggedTagID {
		query = query.Where("(tag_id = '' OR tag_id IS NULL)")
	} else {
		query = query.Where("tag_id = ?", tagID)
	}

	if len(excludeIDs) > 0 {
		query = query.Where("id NOT IN ?", excludeIDs)
	}

	// Handle flags update
	if setFlags != 0 || clearFlags != 0 {
		flagsExpr := "flags"
		if setFlags != 0 {
			flagsExpr = fmt.Sprintf("(%s | %d)", flagsExpr, int(setFlags))
		}
		if clearFlags != 0 {
			flagsExpr = fmt.Sprintf("(%s & ~%d)", flagsExpr, int(clearFlags))
		}
		updates["flags"] = r.db.Raw(flagsExpr)
	}

	if err := query.Updates(updates).Error; err != nil {
		return nil, err
	}

	return affectedIDs, nil
}

// FAQChunkDiff compares FAQ chunks between two knowledge bases and returns the differences.
// Returns: chunksToAdd (IDs of chunks in src whose content_hash is not in dst),
//
//	chunksToDelete (IDs of chunks in dst whose content_hash is not in src)
func (r *chunkRepository) FAQChunkDiff(
	ctx context.Context,
	srcTenantID uint64, srcKBID string,
	dstTenantID uint64, dstKBID string,
) (chunksToAdd []string, chunksToDelete []string, err error) {
	// Get content_hash set from destination KB
	dstHashSubQuery := r.db.Model(&types.Chunk{}).
		Where("tenant_id = ? AND knowledge_base_id = ? AND chunk_type = ?", dstTenantID, dstKBID, types.ChunkTypeFAQ).
		Select("content_hash")

	// Find chunks in source that don't exist in destination (by content_hash)
	err = r.db.WithContext(ctx).Model(&types.Chunk{}).
		Where("tenant_id = ? AND knowledge_base_id = ? AND chunk_type = ?", srcTenantID, srcKBID, types.ChunkTypeFAQ).
		Where("content_hash NOT IN (?)", dstHashSubQuery).
		Pluck("id", &chunksToAdd).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil, fmt.Errorf("failed to get chunks to add: %w", err)
	}

	// Get content_hash set from source KB
	srcHashSubQuery := r.db.Model(&types.Chunk{}).
		Where("tenant_id = ? AND knowledge_base_id = ? AND chunk_type = ?", srcTenantID, srcKBID, types.ChunkTypeFAQ).
		Select("content_hash")

	// Find chunks in destination that don't exist in source (by content_hash)
	err = r.db.WithContext(ctx).Model(&types.Chunk{}).
		Where("tenant_id = ? AND knowledge_base_id = ? AND chunk_type = ?", dstTenantID, dstKBID, types.ChunkTypeFAQ).
		Where("content_hash NOT IN (?)", srcHashSubQuery).
		Pluck("id", &chunksToDelete).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil, fmt.Errorf("failed to get chunks to delete: %w", err)
	}

	return chunksToAdd, chunksToDelete, nil
}
