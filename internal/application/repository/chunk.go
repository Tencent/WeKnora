package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/common"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ErrChunkNotFound is returned when a chunk lookup finds no row. A typed
// sentinel (matching the ErrXNotFound convention used by the other repos)
// so callers can errors.Is it safely through wrapping — replacing the
// previous bare errors.New("chunk not found") that forced fragile
// string-equality matching in the service layer.
var ErrChunkNotFound = errors.New("chunk not found")

// chunkRepository implements the ChunkRepository interface
type chunkRepository struct {
	db *gorm.DB
}

// NewChunkRepository creates a new chunk repository
func NewChunkRepository(db *gorm.DB) interfaces.ChunkRepository {
	return &chunkRepository{db: db}
}

// CreateChunks creates multiple chunks in batches.
// Uses Omit("SeqID") so GORM won't include the auto-increment column in the
// INSERT, which avoids MySQL generating ON DUPLICATE KEY UPDATE and the
// resulting gap-lock deadlocks under concurrent writes.
// A deadlock retry wrapper is kept as defense-in-depth for any remaining
// edge cases on secondary unique indexes.
func (r *chunkRepository) CreateChunks(ctx context.Context, chunks []*types.Chunk) error {
	for _, chunk := range chunks {
		chunk.Content = common.CleanInvalidUTF8(chunk.Content)
	}

	db := r.db.WithContext(ctx)

	// SQLite doesn't support autoIncrement on non-PK columns,
	// so we must pre-assign SeqIDs manually (safe: single connection).
	// PostgreSQL / MySQL use DB sequences — skip to avoid duplicate key
	// races under concurrent inserts.
	if db.Dialector.Name() == "sqlite" {
		if err := types.AssignChunkSeqIDs(db, chunks); err != nil {
			return fmt.Errorf("failed to assign chunk seq_ids: %w", err)
		}
	}

	// Select("*") ensures zero-value fields (IsEnabled=false, Flags=0) are
	// explicitly inserted, bypassing GORM's default value behavior.
	// SeqID=0 is skipped by GORM automatically (autoIncrement tag).
	return db.Select("*").CreateInBatches(chunks, 100).Error
}

// GetChunkByID retrieves a chunk by its ID and tenant ID
func (r *chunkRepository) GetChunkByID(ctx context.Context, tenantID uint64, id string) (*types.Chunk, error) {
	var chunk types.Chunk
	if err := r.db.WithContext(ctx).Where("tenant_id = ? AND id = ?", tenantID, id).First(&chunk).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrChunkNotFound
		}
		return nil, err
	}
	return &chunk, nil
}

// GetChunkByIDOnly retrieves a chunk by ID without tenant filter (for permission resolution).
func (r *chunkRepository) GetChunkByIDOnly(ctx context.Context, id string) (*types.Chunk, error) {
	var chunk types.Chunk
	if err := r.db.WithContext(ctx).Where("id = ?", id).First(&chunk).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrChunkNotFound
		}
		return nil, err
	}
	return &chunk, nil
}

// GetChunkBySeqID retrieves a chunk by its seq_id and tenant ID
func (r *chunkRepository) GetChunkBySeqID(ctx context.Context, tenantID uint64, seqID int64) (*types.Chunk, error) {
	var chunk types.Chunk
	if err := r.db.WithContext(ctx).Where("tenant_id = ? AND seq_id = ?", tenantID, seqID).First(&chunk).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrChunkNotFound
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

// ListChunksByIDOnly retrieves multiple chunks by their IDs without tenant filter (for shared KB resolution).
func (r *chunkRepository) ListChunksByIDOnly(ctx context.Context, ids []string) ([]*types.Chunk, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	var chunks []*types.Chunk
	if err := r.db.WithContext(ctx).Where("id IN ?", ids).Find(&chunks).Error; err != nil {
		return nil, err
	}
	return chunks, nil
}

// ListChunksBySeqID retrieves multiple chunks by their seq_ids
func (r *chunkRepository) ListChunksBySeqID(
	ctx context.Context, tenantID uint64, seqIDs []int64,
) ([]*types.Chunk, error) {
	if len(seqIDs) == 0 {
		return []*types.Chunk{}, nil
	}
	var chunks []*types.Chunk
	if err := r.db.WithContext(ctx).
		Where("tenant_id = ? AND seq_id IN ?", tenantID, seqIDs).
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

// ListActiveIngestionChunksByKnowledgeID returns the complete active database
// rows managed by document-ingestion reconciliation. GORM's default scope is
// intentionally retained so soft-deleted history is never returned.
func (r *chunkRepository) ListActiveIngestionChunksByKnowledgeID(
	ctx context.Context,
	tenantID uint64,
	knowledgeID string,
) ([]*types.Chunk, error) {
	chunks := make([]*types.Chunk, 0)
	err := r.db.WithContext(ctx).
		Where(
			"tenant_id = ? AND knowledge_id = ? AND chunk_type IN ?",
			tenantID,
			knowledgeID,
			ingestionReconcileChunkTypes(),
		).
		Order("chunk_index ASC, id ASC").
		Find(&chunks).Error
	if err != nil {
		return nil, err
	}
	return chunks, nil
}

// ApplyIngestionChunkReconcile atomically applies a previously planned diff
// for active text/parent_text rows. The active identity snapshot is validated
// again under a write lock before any mutation is made.
func (r *chunkRepository) ApplyIngestionChunkReconcile(
	ctx context.Context,
	tenantID uint64,
	knowledgeID string,
	mutation interfaces.IngestionChunkReconcileMutation,
) error {
	if err := validateIngestionChunkMutation(tenantID, knowledgeID, mutation); err != nil {
		return err
	}

	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if mutation.ExpectedAttempt > 0 {
			var latestAttempt int
			if err := tx.Model(&types.KnowledgeProcessingSpan{}).
				Where("knowledge_id = ?", knowledgeID).
				Select("COALESCE(MAX(attempt), 0)").
				Scan(&latestAttempt).Error; err != nil {
				return fmt.Errorf("check latest ingestion attempt: %w", err)
			}
			if latestAttempt != mutation.ExpectedAttempt {
				return fmt.Errorf(
					"ingestion attempt %d superseded by attempt %d for knowledge %q",
					mutation.ExpectedAttempt,
					latestAttempt,
					knowledgeID,
				)
			}
		}

		active := make([]*types.Chunk, 0)
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where(
				"tenant_id = ? AND knowledge_id = ? AND chunk_type IN ?",
				tenantID,
				knowledgeID,
				ingestionReconcileChunkTypes(),
			).
			Order("id ASC").
			Find(&active).Error; err != nil {
			return fmt.Errorf("lock active ingestion chunks: %w", err)
		}

		if err := validateActiveIngestionChunkSnapshot(active, mutation.ExpectedActive); err != nil {
			return err
		}

		activeByID := make(map[string]*types.Chunk, len(active))
		for _, chunk := range active {
			activeByID[chunk.ID] = chunk
		}
		if err := validateFinalIngestionIdentities(active, mutation.RemovedIDs, mutation.Added); err != nil {
			return err
		}

		for _, update := range mutation.Matched {
			existing := activeByID[update.ExistingID]
			if existing == nil {
				return fmt.Errorf("matched ingestion chunk %q is not active", update.ExistingID)
			}
			if !sameIngestionChunkIdentity(existing, update.Desired) {
				return fmt.Errorf(
					"matched ingestion chunk %q identity no longer matches desired identity %q",
					update.ExistingID,
					update.Desired.StableIdentity,
				)
			}

			updatedAt := update.Desired.UpdatedAt
			if updatedAt.IsZero() {
				updatedAt = time.Now()
			}
			result := tx.Model(&types.Chunk{}).
				Where(
					"tenant_id = ? AND knowledge_id = ? AND id = ? AND chunk_type IN ?",
					tenantID,
					knowledgeID,
					update.ExistingID,
					ingestionReconcileChunkTypes(),
				).
				Updates(map[string]interface{}{
					"knowledge_base_id": update.Desired.KnowledgeBaseID,
					"content":           common.CleanInvalidUTF8(update.Desired.Content),
					"chunk_index":       update.Desired.ChunkIndex,
					"start_at":          update.Desired.StartAt,
					"end_at":            update.Desired.EndAt,
					"parent_chunk_id":   update.Desired.ParentChunkID,
					"pre_chunk_id":      update.Desired.PreChunkID,
					"next_chunk_id":     update.Desired.NextChunkID,
					"chunk_type":        update.Desired.ChunkType,
					"stable_identity":   update.Desired.StableIdentity,
					"identity_version":  update.Desired.IdentityVersion,
					"updated_at":        updatedAt,
				})
			if result.Error != nil {
				return fmt.Errorf("update matched ingestion chunk %q: %w", update.ExistingID, result.Error)
			}
			// Some MySQL configurations report zero affected rows when every
			// assigned value is unchanged. The row was already locked and
			// validated above, so only an impossible multi-row update is an error.
			if result.RowsAffected > 1 {
				return fmt.Errorf("update matched ingestion chunk %q affected %d rows", update.ExistingID, result.RowsAffected)
			}
		}

		if len(mutation.Added) > 0 {
			added := cloneChunksForInsert(mutation.Added)
			if tx.Dialector.Name() == "sqlite" {
				if err := types.AssignChunkSeqIDs(tx, added); err != nil {
					return fmt.Errorf("assign added ingestion chunk seq_ids: %w", err)
				}
			}
			if err := tx.Select("*").CreateInBatches(added, 100).Error; err != nil {
				return fmt.Errorf("insert added ingestion chunks: %w", err)
			}
		}

		if len(mutation.RemovedIDs) > 0 {
			result := tx.Where(
				"tenant_id = ? AND knowledge_id = ? AND id IN ? AND chunk_type IN ?",
				tenantID,
				knowledgeID,
				mutation.RemovedIDs,
				ingestionReconcileChunkTypes(),
			).Delete(&types.Chunk{})
			if result.Error != nil {
				return fmt.Errorf("soft-delete removed ingestion chunks: %w", result.Error)
			}
			if result.RowsAffected != int64(len(mutation.RemovedIDs)) {
				return fmt.Errorf(
					"soft-delete removed ingestion chunks affected %d rows, expected %d",
					result.RowsAffected,
					len(mutation.RemovedIDs),
				)
			}
		}

		return nil
	})
}

func ingestionReconcileChunkTypes() []types.ChunkType {
	return []types.ChunkType{types.ChunkTypeText, types.ChunkTypeParentText}
}

func isIngestionRepositoryChunkType(chunkType types.ChunkType) bool {
	return chunkType == types.ChunkTypeText || chunkType == types.ChunkTypeParentText
}

func validateIngestionChunkMutation(
	tenantID uint64,
	knowledgeID string,
	mutation interfaces.IngestionChunkReconcileMutation,
) error {
	expectedIDs := make(map[string]struct{}, len(mutation.ExpectedActive))
	for i, snapshot := range mutation.ExpectedActive {
		if snapshot.ID == "" {
			return fmt.Errorf("expected active ingestion chunk at index %d has empty ID", i)
		}
		if !isIngestionRepositoryChunkType(snapshot.ChunkType) {
			return fmt.Errorf("expected active ingestion chunk %q has unmanaged type %q", snapshot.ID, snapshot.ChunkType)
		}
		if _, duplicate := expectedIDs[snapshot.ID]; duplicate {
			return fmt.Errorf("duplicate expected active ingestion chunk ID %q", snapshot.ID)
		}
		expectedIDs[snapshot.ID] = struct{}{}
	}

	matchedIDs := make(map[string]struct{}, len(mutation.Matched))
	for i, update := range mutation.Matched {
		if update.ExistingID == "" {
			return fmt.Errorf("matched ingestion update at index %d has empty existing ID", i)
		}
		if update.Desired == nil {
			return fmt.Errorf("matched ingestion update %q has nil desired chunk", update.ExistingID)
		}
		if err := validateIngestionMutationChunk(tenantID, knowledgeID, update.Desired, "matched desired"); err != nil {
			return err
		}
		if _, duplicate := matchedIDs[update.ExistingID]; duplicate {
			return fmt.Errorf("duplicate matched ingestion chunk ID %q", update.ExistingID)
		}
		matchedIDs[update.ExistingID] = struct{}{}
	}

	addedIDs := make(map[string]struct{}, len(mutation.Added))
	for _, chunk := range mutation.Added {
		if err := validateIngestionMutationChunk(tenantID, knowledgeID, chunk, "added"); err != nil {
			return err
		}
		if chunk.ID == "" {
			return errors.New("added ingestion chunk has empty ID")
		}
		if _, duplicate := addedIDs[chunk.ID]; duplicate {
			return fmt.Errorf("duplicate added ingestion chunk ID %q", chunk.ID)
		}
		addedIDs[chunk.ID] = struct{}{}
	}

	removedIDs := make(map[string]struct{}, len(mutation.RemovedIDs))
	for _, id := range mutation.RemovedIDs {
		if id == "" {
			return errors.New("removed ingestion chunk has empty ID")
		}
		if _, duplicate := removedIDs[id]; duplicate {
			return fmt.Errorf("duplicate removed ingestion chunk ID %q", id)
		}
		if _, matched := matchedIDs[id]; matched {
			return fmt.Errorf("ingestion chunk %q is both matched and removed", id)
		}
		removedIDs[id] = struct{}{}
	}
	return nil
}

func validateIngestionMutationChunk(
	tenantID uint64,
	knowledgeID string,
	chunk *types.Chunk,
	kind string,
) error {
	if chunk == nil {
		return fmt.Errorf("%s ingestion chunk is nil", kind)
	}
	if chunk.TenantID != tenantID || chunk.KnowledgeID != knowledgeID {
		return fmt.Errorf(
			"%s ingestion chunk %q is outside tenant %d knowledge %q",
			kind,
			chunk.ID,
			tenantID,
			knowledgeID,
		)
	}
	if !isIngestionRepositoryChunkType(chunk.ChunkType) {
		return fmt.Errorf("%s ingestion chunk %q has unmanaged type %q", kind, chunk.ID, chunk.ChunkType)
	}
	if chunk.StableIdentity == "" || chunk.IdentityVersion == "" {
		return fmt.Errorf("%s ingestion chunk %q has incomplete stable identity", kind, chunk.ID)
	}
	return nil
}

func validateActiveIngestionChunkSnapshot(
	active []*types.Chunk,
	expected []interfaces.IngestionChunkSnapshot,
) error {
	if len(active) != len(expected) {
		return fmt.Errorf("active ingestion chunk snapshot changed: found %d rows, expected %d", len(active), len(expected))
	}

	expectedByID := make(map[string]interfaces.IngestionChunkSnapshot, len(expected))
	for _, snapshot := range expected {
		expectedByID[snapshot.ID] = snapshot
	}
	identityOwners := make(map[string]string, len(active))
	for _, chunk := range active {
		if chunk.StableIdentity != "" {
			identityKey := fmt.Sprintf("%d\x00%s\x00%s", chunk.TenantID, chunk.KnowledgeID, chunk.StableIdentity)
			if owner, duplicate := identityOwners[identityKey]; duplicate {
				return fmt.Errorf(
					"duplicate active ingestion stable identity %q on chunk IDs %q and %q",
					chunk.StableIdentity,
					owner,
					chunk.ID,
				)
			}
			identityOwners[identityKey] = chunk.ID
		}

		snapshot, ok := expectedByID[chunk.ID]
		if !ok || snapshot.StableIdentity != chunk.StableIdentity ||
			snapshot.IdentityVersion != chunk.IdentityVersion || snapshot.ChunkType != chunk.ChunkType {
			return fmt.Errorf("active ingestion chunk snapshot changed at row %q", chunk.ID)
		}
	}
	return nil
}

func sameIngestionChunkIdentity(existing, desired *types.Chunk) bool {
	return existing.TenantID == desired.TenantID &&
		existing.KnowledgeID == desired.KnowledgeID &&
		existing.ChunkType == desired.ChunkType &&
		existing.StableIdentity != "" &&
		existing.StableIdentity == desired.StableIdentity &&
		existing.IdentityVersion != "" &&
		existing.IdentityVersion == desired.IdentityVersion
}

func validateFinalIngestionIdentities(
	active []*types.Chunk,
	removedIDs []string,
	added []*types.Chunk,
) error {
	removed := make(map[string]struct{}, len(removedIDs))
	for _, id := range removedIDs {
		removed[id] = struct{}{}
	}
	owners := make(map[string]string, len(active)+len(added))
	for _, chunk := range active {
		if chunk.StableIdentity == "" {
			continue
		}
		if _, willRemove := removed[chunk.ID]; willRemove {
			continue
		}
		owners[chunk.StableIdentity] = chunk.ID
	}
	for _, chunk := range added {
		if owner, duplicate := owners[chunk.StableIdentity]; duplicate {
			return fmt.Errorf(
				"added ingestion chunk %q duplicates active stable identity %q owned by chunk %q",
				chunk.ID,
				chunk.StableIdentity,
				owner,
			)
		}
		owners[chunk.StableIdentity] = chunk.ID
	}
	return nil
}

func cloneChunksForInsert(chunks []*types.Chunk) []*types.Chunk {
	cloned := make([]*types.Chunk, len(chunks))
	for i, chunk := range chunks {
		copy := *chunk
		copy.Content = common.CleanInvalidUTF8(copy.Content)
		cloned[i] = &copy
	}
	return cloned
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
	knowledgeType string,
) ([]*types.Chunk, int64, error) {
	var chunks []*types.Chunk
	var total int64
	keyword = strings.TrimSpace(keyword)

	baseFilter := func(db *gorm.DB) *gorm.DB {
		db = db.Where("tenant_id = ? AND knowledge_id = ? AND chunk_type IN (?) AND status in (?)",
			tenantID, knowledgeID, chunkType, []int{int(types.ChunkStatusIndexed), int(types.ChunkStatusDefault)})
		if tagID != "" {
			db = db.Where("tag_id = ?", tagID)
		}
		if keyword != "" {
			like := "%" + keyword + "%"

			// Document type: search content only
			if knowledgeType != types.KnowledgeTypeFAQ {
				db = db.Where("content LIKE ?", like)
				return db
			}

			// FAQ type: search based on searchField
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
					db = db.Where("(metadata->'similar_questions')::text ILIKE ?", like)
				} else {
					db = db.Where("JSON_EXTRACT(metadata, '$.similar_questions') LIKE ?", like)
				}
			case "answers":
				// Search in answers array of metadata
				if isPostgres {
					db = db.Where("(metadata->'answers')::text ILIKE ?", like)
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

	// Determine sort order based on knowledge type
	var orderClause string
	if knowledgeType == types.KnowledgeTypeFAQ {
		// FAQ: sort by updated_at
		orderClause = "updated_at DESC"
		if sortOrder == "asc" {
			orderClause = "updated_at ASC"
		}
	} else {
		// Document: sort by chunk_index
		orderClause = "chunk_index ASC"
		if sortOrder == "desc" {
			orderClause = "chunk_index DESC"
		}
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

func (r *chunkRepository) ListChunksByParentIDs(
	ctx context.Context,
	tenantID uint64,
	parentIDs []string,
) ([]*types.Chunk, error) {
	if len(parentIDs) == 0 {
		return nil, nil
	}
	var chunks []*types.Chunk
	if err := r.db.WithContext(ctx).
		Where("tenant_id = ? AND parent_chunk_id IN ?", tenantID, parentIDs).
		Find(&chunks).Error; err != nil {
		return nil, err
	}
	return chunks, nil
}

// UpdateChunk updates a chunk using GORM Save, which updates ALL fields
// except SeqID (auto-increment, must not be overwritten).
// Make sure the chunk object is complete (e.g., fetched from DB) before calling this method.
func (r *chunkRepository) UpdateChunk(ctx context.Context, chunk *types.Chunk) error {
	return r.db.WithContext(ctx).Omit("SeqID").Save(chunk).Error
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

	isPostgres := r.db.Dialector.Name() == "postgres"

	var sql string
	if isPostgres {
		sql = fmt.Sprintf(`
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
	} else {
		sql = fmt.Sprintf(`
			UPDATE chunks SET
				content = CASE %s END,
				is_enabled = CASE %s END,
				tag_id = CASE %s END,
				flags = CASE %s END,
				status = CASE %s END,
				updated_at = datetime('now')
			WHERE id IN (%s)
		`,
			strings.Join(contentCases, " "),
			strings.Join(isEnabledCases, " "),
			strings.Join(tagIDCases, " "),
			strings.Join(flagsCases, " "),
			strings.Join(statusCases, " "),
			strings.Join(inPlaceholders, ","),
		)
	}

	return r.db.WithContext(ctx).Exec(sql, args...).Error
}

// DeleteChunk deletes a chunk by its ID
func (r *chunkRepository) DeleteChunk(ctx context.Context, tenantID uint64, id string) error {
	return r.db.WithContext(ctx).Where("tenant_id = ? AND id = ?", tenantID, id).Delete(&types.Chunk{}).Error
}

// DeleteChunks deletes chunks by IDs in batch.
// To avoid MySQL Error 1390 (too many placeholders), IDs are split into batches.
func (r *chunkRepository) DeleteChunks(ctx context.Context, tenantID uint64, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	const batchSize = 5000
	for i := 0; i < len(ids); i += batchSize {
		end := i + batchSize
		if end > len(ids) {
			end = len(ids)
		}
		if err := r.db.WithContext(ctx).Where("tenant_id = ? AND id IN ?", tenantID, ids[i:end]).Delete(&types.Chunk{}).Error; err != nil {
			return err
		}
	}
	return nil
}

// DeleteChunksByKnowledgeID deletes all chunks for a knowledge ID
func (r *chunkRepository) DeleteChunksByKnowledgeID(ctx context.Context, tenantID uint64, knowledgeID string) error {
	return r.db.WithContext(ctx).Where(
		"tenant_id = ? AND knowledge_id = ?", tenantID, knowledgeID,
	).Delete(&types.Chunk{}).Error
}

// ListImageInfoByKnowledgeIDs returns non-empty image_info values for the given knowledge IDs.
// No chunk_type filter — collects from text, image_ocr, and image_caption chunks.
func (r *chunkRepository) ListImageInfoByKnowledgeIDs(
	ctx context.Context, tenantID uint64, knowledgeIDs []string,
) ([]interfaces.ChunkImageInfo, error) {
	var results []interfaces.ChunkImageInfo
	err := r.db.WithContext(ctx).
		Model(&types.Chunk{}).
		Select("knowledge_id, image_info").
		Where("tenant_id = ? AND knowledge_id IN ? AND image_info != ''", tenantID, knowledgeIDs).
		Scan(&results).Error
	return results, err
}

// DeleteByKnowledgeList deletes all chunks for a knowledge list
func (r *chunkRepository) DeleteByKnowledgeList(ctx context.Context, tenantID uint64, knowledgeIDs []string) error {
	return r.db.WithContext(ctx).Where(
		"tenant_id = ? AND knowledge_id in ?", tenantID, knowledgeIDs,
	).Delete(&types.Chunk{}).Error
}

// MoveChunksByKnowledgeID updates knowledge_base_id for all chunks of a knowledge item
func (r *chunkRepository) MoveChunksByKnowledgeID(ctx context.Context, tenantID uint64, knowledgeID string, targetKBID string) error {
	return r.db.WithContext(ctx).Model(&types.Chunk{}).
		Where("tenant_id = ? AND knowledge_id = ?", tenantID, knowledgeID).
		Update("knowledge_base_id", targetKBID).Error
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

// FindFAQChunkWithDuplicateQuestion finds a single FAQ chunk whose standard_question or
// similar_questions overlap with the given question list.
// Uses dialect-specific JSON queries (MySQL / PostgreSQL / SQLite).
func (r *chunkRepository) FindFAQChunkWithDuplicateQuestion(
	ctx context.Context,
	tenantID uint64,
	kbID string,
	excludeChunkID string,
	questions []string,
) (*types.Chunk, error) {
	if len(questions) == 0 {
		return nil, nil
	}

	db := r.db.WithContext(ctx).
		Select("id, metadata").
		Where("tenant_id = ? AND knowledge_base_id = ? AND chunk_type = ? AND status = ? AND id != ?",
			tenantID, kbID, types.ChunkTypeFAQ, types.ChunkStatusIndexed, excludeChunkID)

	switch r.db.Name() {
	case "mysql":
		// MySQL 5.7+: JSON_EXTRACT for standard_question, JSON_CONTAINS for similar_questions
		parts := []string{
			"JSON_UNQUOTE(JSON_EXTRACT(metadata, '$.standard_question')) IN ?",
		}
		args := []interface{}{questions}
		for _, q := range questions {
			parts = append(parts,
				"JSON_CONTAINS(metadata, ?, '$.similar_questions')")
			jsonVal, _ := json.Marshal(q)
			args = append(args, string(jsonVal))
		}
		db = db.Where(strings.Join(parts, " OR "), args...)
	case "postgres":
		db = db.Where(
			"(metadata->>'standard_question' IN ? OR EXISTS ("+
				"SELECT 1 FROM jsonb_array_elements_text("+
				"COALESCE(metadata->'similar_questions', '[]'::jsonb)) elem "+
				"WHERE elem.value IN ?))",
			questions, questions)
	default: // sqlite
		db = db.Where(
			"(json_extract(metadata, '$.standard_question') IN ? OR EXISTS ("+
				"SELECT 1 FROM json_each("+
				"CASE WHEN json_extract(metadata, '$.similar_questions') IS NOT NULL "+
				"THEN json_extract(metadata, '$.similar_questions') ELSE '[]' END) "+
				"WHERE value IN ?))",
			questions, questions)
	}

	var chunk types.Chunk
	if err := db.Limit(1).Find(&chunk).Error; err != nil {
		return nil, err
	}
	if chunk.ID == "" {
		return nil, nil
	}
	return &chunk, nil
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

	nowFunc := "NOW()"
	if r.db.Dialector.Name() == "sqlite" {
		nowFunc = "datetime('now')"
	}
	sql := fmt.Sprintf(`
	UPDATE chunks
    SET flags = (flags | (%s)) & ~(%s),
        updated_at = %s
    WHERE tenant_id = ?
      AND knowledge_base_id = ?
      AND id IN (%s)
`, setExpr, clearExpr, nowFunc, strings.Join(inPlaceholders, ","))

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
		if tagID != "" {
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
		"updated_at": time.Now(),
	}

	if isEnabled != nil {
		updates["is_enabled"] = *isEnabled
	}

	// Handle newTagID update
	if newTagID != nil {
		updates["tag_id"] = *newTagID
	}

	query := r.db.WithContext(ctx).Model(&types.Chunk{}).
		Where("tenant_id = ? AND knowledge_base_id = ? AND chunk_type = ?",
			tenantID, kbID, types.ChunkTypeFAQ)

	if tagID != "" {
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

// ListRecommendedFAQChunks lists FAQ chunks with the recommended flag set.
// Filter by explicitly selected kbIDs, knowledgeIDs, and/or FAQ tagIDs (OR relationship).
// Returns up to `limit` chunks sorted by updated_at descending.
func (r *chunkRepository) ListRecommendedFAQChunks(
	ctx context.Context,
	tenantID uint64,
	kbIDs []string,
	knowledgeIDs []string,
	tagIDs []string,
	limit int,
) ([]*types.Chunk, error) {
	if limit <= 0 {
		limit = 10
	}
	if len(kbIDs) == 0 && len(knowledgeIDs) == 0 && len(tagIDs) == 0 {
		return nil, nil
	}
	var chunks []*types.Chunk
	query := r.db.WithContext(ctx).
		Select("id, knowledge_id, knowledge_base_id, chunk_type, metadata, flags, updated_at").
		Where("tenant_id = ? AND chunk_type = ? AND status IN ? AND is_enabled = ? AND flags & ? != 0",
			tenantID, types.ChunkTypeFAQ, []int{int(types.ChunkStatusIndexed), int(types.ChunkStatusDefault)}, true, int(types.ChunkFlagRecommended))
	var scopeClauses []string
	var scopeArgs []interface{}
	if len(kbIDs) > 0 {
		scopeClauses = append(scopeClauses, "knowledge_base_id IN ?")
		scopeArgs = append(scopeArgs, kbIDs)
	}
	if len(knowledgeIDs) > 0 {
		scopeClauses = append(scopeClauses, "knowledge_id IN ?")
		scopeArgs = append(scopeArgs, knowledgeIDs)
	}
	if len(tagIDs) > 0 {
		scopeClauses = append(scopeClauses, "tag_id IN ?")
		scopeArgs = append(scopeArgs, tagIDs)
	}
	query = query.Where("("+strings.Join(scopeClauses, " OR ")+")", scopeArgs...)

	orderClause := "RANDOM()"
	if r.db.Dialector.Name() == "mysql" {
		orderClause = "RAND()"
	}

	if err := query.
		Order(orderClause).
		Limit(limit).
		Find(&chunks).Error; err != nil {
		return nil, err
	}
	return chunks, nil
}

// ListRecentDocumentChunksWithQuestions lists recent document chunks that have generated questions.
// Filter by kbIDs and/or knowledgeIDs (OR relationship). At least one must be non-empty.
// Returns up to `limit` chunks sorted by updated_at descending.
func (r *chunkRepository) ListRecentDocumentChunksWithQuestions(
	ctx context.Context,
	tenantID uint64,
	kbIDs []string,
	knowledgeIDs []string,
	limit int,
) ([]*types.Chunk, error) {
	if limit <= 0 {
		limit = 10
	}
	if len(kbIDs) == 0 && len(knowledgeIDs) == 0 {
		return nil, nil
	}
	var chunks []*types.Chunk

	baseQuery := r.db.WithContext(ctx).
		Select("id, knowledge_id, knowledge_base_id, chunk_type, metadata, updated_at").
		Where("tenant_id = ? AND chunk_type = ? AND status IN ? AND is_enabled = ?",
			tenantID, types.ChunkTypeText, []int{int(types.ChunkStatusIndexed), int(types.ChunkStatusDefault)}, true)

	if len(kbIDs) > 0 && len(knowledgeIDs) > 0 {
		baseQuery = baseQuery.Where("knowledge_base_id IN ? OR knowledge_id IN ?", kbIDs, knowledgeIDs)
	} else if len(knowledgeIDs) > 0 {
		// 指定了具体知识文档，直接按 knowledge_id 过滤（忽略 kbIDs）
		baseQuery = baseQuery.Where("knowledge_id IN ?", knowledgeIDs)
	} else if len(kbIDs) > 0 {
		baseQuery = baseQuery.Where("knowledge_base_id IN ?", kbIDs)
	}

	orderClause := "RANDOM()"
	if r.db.Dialector.Name() == "mysql" {
		orderClause = "RAND()"
	}

	// Query chunks that have non-empty generated_questions in metadata
	switch r.db.Name() {
	case "postgres":
		if err := baseQuery.
			Where("metadata IS NOT NULL AND metadata::text != '{}' AND jsonb_array_length(COALESCE(metadata->'generated_questions', '[]'::jsonb)) > 0").
			Order(orderClause).
			Limit(limit).
			Find(&chunks).Error; err != nil {
			return nil, err
		}
	case "mysql":
		if err := baseQuery.
			Where("metadata IS NOT NULL AND JSON_LENGTH(JSON_EXTRACT(metadata, '$.generated_questions')) > 0").
			Order(orderClause).
			Limit(limit).
			Find(&chunks).Error; err != nil {
			return nil, err
		}
	default: // sqlite
		if err := baseQuery.
			Where("metadata IS NOT NULL AND json_array_length(json_extract(metadata, '$.generated_questions')) > 0").
			Order(orderClause).
			Limit(limit).
			Find(&chunks).Error; err != nil {
			return nil, err
		}
	}

	return chunks, nil
}
