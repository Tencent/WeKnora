package repository

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	ErrKnowledgeNotFound                  = errors.New("knowledge not found")
	ErrKnowledgeFileUpdateVersionConflict = errors.New("knowledge file update version conflict")
	ErrKnowledgeFileUpdateDeleting        = errors.New("knowledge is being deleted")
	ErrKnowledgeFileUpdateStateConflict   = errors.New("knowledge file update state conflict")
)

// likeEscapeChar is the SQL ESCAPE character paired with escapeLikeKeyword.
const likeEscapeChar = `\`

// escapeLikeKeyword escapes SQL LIKE wildcards (%, _) in a keyword
// so they are treated as literal characters.
func escapeLikeKeyword(keyword string) string {
	keyword = strings.ReplaceAll(keyword, `\`, `\\`)
	keyword = strings.ReplaceAll(keyword, "%", `\%`)
	keyword = strings.ReplaceAll(keyword, "_", `\_`)
	return keyword
}

// omitFieldsOnUpdate defines fields to omit when updating knowledge.
//
// PendingSubtasksCount is deliberately omitted from every full-row Save:
// it is an orchestration counter owned exclusively by the atomic helpers
// SetFinalizing (seed), FinalizeSubtask (decrement+promote) and the
// explicit UpdateKnowledgeColumns resets (cancel/reparse). A generic
// UpdateKnowledge call persists the WHOLE in-memory struct, so any
// concurrent enrichment subtask that loaded the row, did slow work
// (e.g. an LLM call), then saved an unrelated field would otherwise
// write back the STALE counter it read at load time — clobbering the
// decrements other subtasks performed in the meantime. That made the
// counter jump back up and never reach zero (the "stuck
// pending_subtasks_count / never promoted to completed" bug). Omitting
// the column here means Save can never touch it.
var omitFieldsOnUpdate = []string{"DeletedAt", "PendingSubtasksCount"}

// knowledgeRepository implements knowledge base and knowledge repository interface
type knowledgeRepository struct {
	db *gorm.DB
}

// NewKnowledgeRepository creates a new knowledge repository
func NewKnowledgeRepository(db *gorm.DB) interfaces.KnowledgeRepository {
	return &knowledgeRepository{db: db}
}

// CreateKnowledge creates knowledge
func (r *knowledgeRepository) CreateKnowledge(ctx context.Context, knowledge *types.Knowledge) error {
	err := r.db.WithContext(ctx).Create(knowledge).Error
	return err
}

// GetKnowledgeByID gets knowledge
func (r *knowledgeRepository) GetKnowledgeByID(
	ctx context.Context,
	tenantID uint64,
	id string,
) (*types.Knowledge, error) {
	var knowledge types.Knowledge
	if err := r.db.WithContext(ctx).Where("tenant_id = ? AND id = ?", tenantID, id).First(&knowledge).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrKnowledgeNotFound
		}
		return nil, err
	}
	r.projectKnowledgeFileUpdateSlots(ctx, tenantID, []*types.Knowledge{&knowledge})
	return &knowledge, nil
}

// GetKnowledgeByIDOnly returns knowledge by ID without tenant filter (for permission resolution).
func (r *knowledgeRepository) GetKnowledgeByIDOnly(ctx context.Context, id string) (*types.Knowledge, error) {
	var knowledge types.Knowledge
	if err := r.db.WithContext(ctx).Where("id = ?", id).First(&knowledge).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrKnowledgeNotFound
		}
		return nil, err
	}
	return &knowledge, nil
}

// ListKnowledgeByKnowledgeBaseID lists all knowledge in a knowledge base
func (r *knowledgeRepository) ListKnowledgeByKnowledgeBaseID(
	ctx context.Context, tenantID uint64, kbID string,
) ([]*types.Knowledge, error) {
	var knowledges []*types.Knowledge
	if err := r.db.WithContext(ctx).Where("tenant_id = ? AND knowledge_base_id = ?", tenantID, kbID).
		Order("created_at DESC").Find(&knowledges).Error; err != nil {
		return nil, err
	}
	r.projectKnowledgeFileUpdateSlots(ctx, tenantID, knowledges)
	return knowledges, nil
}

// applyKnowledgeListFilter applies the optional filter dimensions of
// KnowledgeListFilter to a GORM query. Tenant / knowledge base scoping must be
// applied by the caller before invoking this helper.
func applyKnowledgeListFilter(query *gorm.DB, filter types.KnowledgeListFilter) *gorm.DB {
	if len(filter.TagIDs) > 0 {
		query = query.Where(
			"knowledges.id IN (SELECT knowledge_id FROM knowledge_tag_relations WHERE tag_id IN (?))",
			filter.TagIDs,
		)
	}
	if filter.Keyword != "" {
		// Case-insensitive (LOWER … LIKE LOWER) so keyword search matches
		// regardless of the stored casing — consistent with the sibling
		// LOWER() filters in this file and with the client-side `search kb`
		// / `search sessions` filters. Plain LIKE is case-sensitive in
		// Postgres, which surprised callers searching with lowercase.
		escaped := strings.ToLower(escapeLikeKeyword(filter.Keyword))
		query = query.Where("(LOWER(file_name) LIKE ? OR LOWER(title) LIKE ?)", "%"+escaped+"%", "%"+escaped+"%")
	}
	// FileType and Source share the same special-case routing onto `type` for
	// the "manual" / "url" values, so callers can pick either control.
	applyTypeOrFileType := func(q *gorm.DB, val string) *gorm.DB {
		switch val {
		case "":
			return q
		case "manual", "url":
			return q.Where("type = ?", val)
		default:
			return q.Where("file_type = ?", val)
		}
	}
	query = applyTypeOrFileType(query, filter.FileType)
	if filter.Source != "" {
		switch filter.Source {
		case "manual", "url":
			query = query.Where("type = ?", filter.Source)
		default:
			query = query.Where("channel = ?", filter.Source)
		}
	}
	if filter.ParseStatus != "" {
		query = query.Where("parse_status = ?", filter.ParseStatus)
	} else {
		// Hide rows that are mid-deletion so an async delete never lingers in the
		// document list as if it were a normal entry (issue #2192). The delete
		// pipeline marks the row `deleting` before tearing down its resources; a
		// row whose delete task exhausts its retries is flipped to `failed` by the
		// dead-letter callback and stays visible so the failure remains actionable.
		query = query.Where("parse_status <> ?", types.ParseStatusDeleting)
	}
	if !filter.UpdatedFrom.IsZero() {
		query = query.Where("updated_at >= ?", filter.UpdatedFrom)
	}
	if !filter.UpdatedTo.IsZero() {
		query = query.Where("updated_at <= ?", filter.UpdatedTo)
	}
	switch filter.FolderScope {
	case types.FolderScopeExact:
		query = query.Where("folder_path = ?", filter.FolderPath)
	case types.FolderScopeSubtree:
		// An empty path means "the whole knowledge base", so no predicate is
		// needed; otherwise match the folder itself plus everything below it.
		if filter.FolderPath != "" {
			query = query.Where(
				"(folder_path = ? OR folder_path LIKE ? ESCAPE ?)",
				filter.FolderPath,
				escapeLikeKeyword(filter.FolderPath)+"/%",
				likeEscapeChar,
			)
		}
	}
	return query
}

// ListPagedKnowledgeByKnowledgeBaseID lists all knowledge in a knowledge base with pagination
func (r *knowledgeRepository) ListPagedKnowledgeByKnowledgeBaseID(
	ctx context.Context,
	tenantID uint64,
	kbID string,
	page *types.Pagination,
	filter types.KnowledgeListFilter,
) ([]*types.Knowledge, int64, error) {
	var knowledges []*types.Knowledge
	var total int64

	scope := func(q *gorm.DB) *gorm.DB {
		return applyKnowledgeListFilter(
			q.Where("tenant_id = ? AND knowledge_base_id = ?", tenantID, kbID),
			filter,
		)
	}

	if err := scope(r.db.WithContext(ctx).Model(&types.Knowledge{})).Count(&total).Error; err != nil {
		return nil, 0, err
	}

	if err := scope(r.db.WithContext(ctx)).
		Order("created_at DESC").
		Offset(page.Offset()).
		Limit(page.Limit()).
		Find(&knowledges).Error; err != nil {
		return nil, 0, err
	}
	r.projectKnowledgeFileUpdateSlots(ctx, tenantID, knowledges)

	return knowledges, total, nil
}

// ListKnowledgeFolderCounts aggregates how many knowledge entries live directly
// in each folder of a knowledge base. Rows mid-deletion are excluded so the
// sidebar tree counts match the document list.
func (r *knowledgeRepository) ListKnowledgeFolderCounts(
	ctx context.Context,
	tenantID uint64,
	kbID string,
) ([]*types.KnowledgeFolderCount, error) {
	var counts []*types.KnowledgeFolderCount
	if err := r.db.WithContext(ctx).
		Model(&types.Knowledge{}).
		Select("folder_path AS folder_path, COUNT(*) AS count").
		Where("tenant_id = ? AND knowledge_base_id = ? AND parse_status <> ?",
			tenantID, kbID, types.ParseStatusDeleting).
		Group("folder_path").
		Find(&counts).Error; err != nil {
		return nil, err
	}
	return counts, nil
}

// UpdateKnowledgeFolderPath files the given knowledge entries under folderPath.
// Only the display/navigation column is touched: chunks, embeddings and the
// stored file are unaffected, which is why re-filing needs no re-processing.
// Returns the number of affected rows.
func (r *knowledgeRepository) UpdateKnowledgeFolderPath(
	ctx context.Context,
	tenantID uint64,
	kbID string,
	ids []string,
	folderPath string,
) (int64, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	result := r.db.WithContext(ctx).
		Model(&types.Knowledge{}).
		Where("tenant_id = ? AND knowledge_base_id = ? AND id IN (?)", tenantID, kbID, ids).
		Updates(map[string]interface{}{"folder_path": folderPath, "updated_at": time.Now()})
	if result.Error != nil {
		return 0, result.Error
	}
	return result.RowsAffected, nil
}

// RenameKnowledgeFolderPath rewrites folder_path for a folder and every folder
// below it, which is how a folder rename or move is applied. Renaming onto an
// existing path merges the two folders. Returns the number of affected rows.
func (r *knowledgeRepository) RenameKnowledgeFolderPath(
	ctx context.Context,
	tenantID uint64,
	kbID string,
	from string,
	to string,
) (int64, error) {
	if from == "" {
		return 0, errors.New("source folder path is required")
	}

	// The rewrite is done row by row rather than with SQL string functions so it
	// behaves identically on PostgreSQL and SQLite.
	var rows []*types.Knowledge
	if err := r.db.WithContext(ctx).
		Select("id", "folder_path").
		Where("tenant_id = ? AND knowledge_base_id = ? AND (folder_path = ? OR folder_path LIKE ? ESCAPE ?)",
			tenantID, kbID, from, escapeLikeKeyword(from)+"/%", likeEscapeChar).
		Find(&rows).Error; err != nil {
		return 0, err
	}
	if len(rows) == 0 {
		return 0, nil
	}

	// Group by destination so each distinct rewrite is a single UPDATE.
	byTarget := map[string][]string{}
	for _, row := range rows {
		suffix := strings.TrimPrefix(row.FolderPath, from)
		byTarget[types.NormalizeKnowledgeFolderPath(to+suffix)] = append(
			byTarget[types.NormalizeKnowledgeFolderPath(to+suffix)], row.ID)
	}

	var affected int64
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for target, targetIDs := range byTarget {
			result := tx.Model(&types.Knowledge{}).
				Where("tenant_id = ? AND knowledge_base_id = ? AND id IN (?)", tenantID, kbID, targetIDs).
				Updates(map[string]interface{}{"folder_path": target, "updated_at": time.Now()})
			if result.Error != nil {
				return result.Error
			}
			affected += result.RowsAffected
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return affected, nil
}

// UpdateKnowledge updates knowledge
func (r *knowledgeRepository) UpdateKnowledge(ctx context.Context, knowledge *types.Knowledge) error {
	omit := omitFieldsOnUpdate
	// Legacy/unit-test schemas created before custom_metadata should continue
	// to support unrelated updates when the caller did not provide the field.
	if knowledge.CustomMetadata == nil {
		omit = append(append([]string{}, omitFieldsOnUpdate...), "custom_metadata")
	}
	err := r.db.WithContext(ctx).Omit(omit...).Save(knowledge).Error
	return err
}

// UpdateKnowledgeBatch updates knowledge items in batch
func (r *knowledgeRepository) UpdateKnowledgeBatch(ctx context.Context, knowledgeList []*types.Knowledge) error {
	if len(knowledgeList) == 0 {
		return nil
	}
	return r.db.Debug().WithContext(ctx).Omit(omitFieldsOnUpdate...).Save(knowledgeList).Error
}

// DeleteKnowledge deletes knowledge
func (r *knowledgeRepository) DeleteKnowledge(ctx context.Context, tenantID uint64, id string) error {
	return r.db.WithContext(ctx).Where("tenant_id = ? AND id = ?", tenantID, id).Delete(&types.Knowledge{}).Error
}

// DeleteKnowledge deletes knowledge
func (r *knowledgeRepository) DeleteKnowledgeList(ctx context.Context, tenantID uint64, ids []string) error {
	return r.db.WithContext(ctx).Where("tenant_id = ? AND id in ?", tenantID, ids).Delete(&types.Knowledge{}).Error
}

// GetKnowledgeBatch gets knowledge in batch
func (r *knowledgeRepository) GetKnowledgeBatch(
	ctx context.Context, tenantID uint64, ids []string,
) ([]*types.Knowledge, error) {
	var knowledge []*types.Knowledge
	if err := r.db.WithContext(ctx).Debug().
		Where("tenant_id = ? AND id IN ?", tenantID, ids).
		Find(&knowledge).Error; err != nil {
		return nil, err
	}
	r.projectKnowledgeFileUpdateSlots(ctx, tenantID, knowledge)
	return knowledge, nil
}

// projectKnowledgeFileUpdateSlots adds read-only update state without making
// existing knowledge queries depend on the new table. This is intentionally
// best-effort for deployments that disable automatic migrations.
func (r *knowledgeRepository) projectKnowledgeFileUpdateSlots(
	ctx context.Context, tenantID uint64, knowledges []*types.Knowledge,
) {
	if len(knowledges) == 0 {
		return
	}
	ids := make([]string, 0, len(knowledges))
	byID := make(map[string]*types.Knowledge, len(knowledges))
	for _, knowledge := range knowledges {
		if knowledge == nil {
			continue
		}
		knowledge.FileUpdateState = types.KnowledgeFileUpdateStateIdle
		ids = append(ids, knowledge.ID)
		byID[knowledge.ID] = knowledge
	}
	var slots []*types.KnowledgeFileUpdateSlot
	if err := r.db.WithContext(ctx).
		Where("tenant_id = ? AND knowledge_id IN ?", tenantID, ids).
		Find(&slots).Error; err != nil {
		return
	}
	for _, slot := range slots {
		knowledge := byID[slot.KnowledgeID]
		if knowledge == nil {
			continue
		}
		knowledge.FileUpdateVersion = slot.LatestVersion
		switch {
		case slot.ActiveState == types.KnowledgeFileUpdateStateFailed:
			knowledge.FileUpdateState = types.KnowledgeFileUpdateStateFailed
			knowledge.FileUpdateError = "文件更新失败"
		case slot.PendingVersion != nil:
			knowledge.FileUpdateState = types.KnowledgeFileUpdateResultPending
		case slot.ActiveVersion != nil:
			knowledge.FileUpdateState = types.KnowledgeFileUpdateResultActive
		default:
			knowledge.FileUpdateState = types.KnowledgeFileUpdateStateIdle
		}
	}
}

// CheckKnowledgeExists checks if knowledge already exists
func (r *knowledgeRepository) CheckKnowledgeExists(
	ctx context.Context,
	tenantID uint64,
	kbID string,
	params *types.KnowledgeCheckParams,
) (bool, *types.Knowledge, error) {
	query := r.db.WithContext(ctx).Model(&types.Knowledge{}).
		Where("tenant_id = ? AND knowledge_base_id = ? AND parse_status <> ?", tenantID, kbID, "failed")
	return checkKnowledgeExistsQuery(query, params)
}

// CheckKnowledgeExistsExcluding checks duplicate identity while excluding the
// row being replaced. It prevents a replacement from conflicting with itself
// without weakening duplicate protection against other knowledge rows.
func (r *knowledgeRepository) CheckKnowledgeExistsExcluding(
	ctx context.Context,
	tenantID uint64,
	kbID string,
	excludeKnowledgeID string,
	params *types.KnowledgeCheckParams,
) (bool, *types.Knowledge, error) {
	query := r.db.WithContext(ctx).Model(&types.Knowledge{}).
		Where("tenant_id = ? AND knowledge_base_id = ? AND parse_status <> ?", tenantID, kbID, types.ParseStatusFailed)
	if excludeKnowledgeID != "" {
		query = query.Where("id <> ?", excludeKnowledgeID)
	}
	return checkKnowledgeExistsQuery(query, params)
}

func checkKnowledgeExistsQuery(query *gorm.DB, params *types.KnowledgeCheckParams) (bool, *types.Knowledge, error) {
	if params == nil {
		return false, nil, nil
	}

	switch params.Type {
	case "file":
		// File content is only a duplicate within the same file type. This keeps
		// same-content documents with distinct formats (for example, .md and
		// .txt) available as separate knowledge items.
		if params.FileHash != "" {
			var knowledge types.Knowledge
			duplicateQuery := query.Where("type = ? AND file_hash = ?", "file", params.FileHash)
			if params.FileType != "" {
				duplicateQuery = duplicateQuery.Where("LOWER(file_type) = ?", strings.ToLower(params.FileType))
			}
			err := duplicateQuery.First(&knowledge).Error
			if err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					return false, nil, nil
				}
				return false, nil, err
			}
			return true, &knowledge, nil
		}

		// If no hash or hash doesn't match, use filename, size, and file type.
		if params.FileName != "" && params.FileSize > 0 {
			var knowledge types.Knowledge
			duplicateQuery := query.Where(
				"type = ? AND file_name = ? AND file_size = ?",
				"file", params.FileName, params.FileSize,
			)
			if params.FileType != "" {
				duplicateQuery = duplicateQuery.Where("LOWER(file_type) = ?", strings.ToLower(params.FileType))
			}
			err := duplicateQuery.First(&knowledge).Error
			if err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					return false, nil, nil
				}
				return false, nil, err
			}
			return true, &knowledge, nil
		}
	case "url":
		// If file hash exists, prioritize exact match using hash
		if params.FileHash != "" {
			var knowledge types.Knowledge
			err := query.Where("type = 'url' AND file_hash = ?", params.FileHash).First(&knowledge).Error
			if err == nil && knowledge.ID != "" {
				return true, &knowledge, nil
			}
			if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
				return false, nil, err
			}
		}

		if params.URL != "" {
			var knowledge types.Knowledge
			err := query.Where("type = 'url' AND source = ?", params.URL).First(&knowledge).Error
			if err == nil && knowledge.ID != "" {
				return true, &knowledge, nil
			}
			if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
				return false, nil, err
			}
		}
		return false, nil, nil
	}

	// No valid parameters, default to not existing
	return false, nil, nil
}

// AminusB returns the IDs of knowledge in A that have no counterpart in B,
// comparing by file_hash as a MULTISET rather than a plain set.
//
// A plain "file_hash NOT IN (SELECT file_hash FROM B)" only asks whether a
// hash exists in B at all, so once a KB accumulates several rows sharing the
// same file_hash (e.g. the same file ingested multiple times), the diff can
// never reconcile the *count* difference: two KBs with identical distinct-hash
// sets but different row counts produce an empty diff in both directions, and
// a clone target can never converge to the source. This also breaks on MySQL
// when B contains a NULL file_hash, because NOT IN then yields no rows at all.
//
// The multiset diff is computed in Go rather than SQL: we only pull
// (id, file_hash) for A plus per-hash counts for B, then keep A's surplus
// copies. This avoids window functions (unsupported on MySQL 5.7 / MariaDB)
// and the O(n^2) correlated-subquery ranking that would otherwise be needed
// there. Clone is a background job over at most a few thousand rows, so the
// two lightweight two-column reads are cheap.
//
// Rows with a NULL/empty file_hash carry no reliable identity (unparsed /
// passage knowledge), so they are always treated as present-only-in-A to
// avoid collapsing distinct rows into one.
func (r *knowledgeRepository) AminusB(
	ctx context.Context,
	Atenant uint64, A string,
	Btenant uint64, B string,
) ([]string, error) {
	type hashRow struct {
		ID       string
		FileHash string
	}
	// Order so the retained (matched) copies are the earliest ones and the
	// surplus we return is deterministic across runs.
	var aRows []hashRow
	if err := r.db.WithContext(ctx).
		Model(&types.Knowledge{}).
		Select("id, file_hash").
		Where("tenant_id = ? AND knowledge_base_id = ?", Atenant, A).
		Order("file_hash, created_at, id").
		Find(&aRows).Error; err != nil {
		return nil, err
	}

	type hashCount struct {
		FileHash string
		Cnt      int
	}
	var bCounts []hashCount
	if err := r.db.WithContext(ctx).
		Model(&types.Knowledge{}).
		Select("file_hash, COUNT(*) AS cnt").
		Where("tenant_id = ? AND knowledge_base_id = ?", Btenant, B).
		Group("file_hash").
		Find(&bCounts).Error; err != nil {
		return nil, err
	}

	// remaining[h] is how many copies of hash h in B are still unmatched.
	remaining := make(map[string]int, len(bCounts))
	for _, c := range bCounts {
		if c.FileHash != "" {
			remaining[c.FileHash] = c.Cnt
		}
	}

	knowledgeIDs := make([]string, 0)
	for _, row := range aRows {
		// NULL scans into "" here, so this also covers NULL hashes.
		if row.FileHash == "" {
			knowledgeIDs = append(knowledgeIDs, row.ID)
			continue
		}
		if remaining[row.FileHash] > 0 {
			remaining[row.FileHash]-- // matched by an existing copy in B
			continue
		}
		knowledgeIDs = append(knowledgeIDs, row.ID) // surplus copy in A
	}
	return knowledgeIDs, nil
}

func (r *knowledgeRepository) UpdateKnowledgeColumn(
	ctx context.Context,
	id string,
	column string,
	value interface{},
) error {
	err := r.db.WithContext(ctx).Model(&types.Knowledge{}).Where("id = ?", id).Update(column, value).Error
	return err
}

// UpdateKnowledgeColumns writes multiple columns in a single UPDATE so callers
// that flip related fields together (parse_status + error_message after
// dead-letter, for example) cannot leave the row half-updated when the second
// write fails.
func (r *knowledgeRepository) UpdateKnowledgeColumns(
	ctx context.Context,
	id string,
	values map[string]interface{},
) error {
	if len(values) == 0 {
		return nil
	}
	return r.db.WithContext(ctx).Model(&types.Knowledge{}).Where("id = ?", id).Updates(values).Error
}

// ClaimKnowledgeFileUpdate serializes in-place replacements using the exact
// source version observed by the request. GORM's normal model scope also
// excludes soft-deleted rows.
func (r *knowledgeRepository) ClaimKnowledgeFileUpdate(
	ctx context.Context,
	tenantID uint64,
	knowledgeID string,
	kbID string,
	expectedStatus string,
	expectedFilePath string,
	expectedFileHash string,
) (bool, error) {
	result := r.db.WithContext(ctx).
		Model(&types.Knowledge{}).
		Where(
			"tenant_id = ? AND id = ? AND knowledge_base_id = ? AND parse_status = ? AND file_path = ? AND file_hash = ?",
			tenantID, knowledgeID, kbID, expectedStatus, expectedFilePath, expectedFileHash,
		).
		Updates(map[string]interface{}{
			"parse_status":  types.ParseStatusReplacing,
			"error_message": "",
			"updated_at":    time.Now(),
		})
	if result.Error != nil {
		return false, result.Error
	}
	return result.RowsAffected == 1, nil
}

// UpdateApplyingKnowledgeFileColumns applies compensation or the final file
// switch only if the task still owns the source version it originally claimed.
func (r *knowledgeRepository) UpdateApplyingKnowledgeFileColumns(
	ctx context.Context,
	tenantID uint64,
	knowledgeID string,
	kbID string,
	expectedFilePath string,
	expectedFileHash string,
	values map[string]interface{},
) (bool, error) {
	if len(values) == 0 {
		return false, nil
	}
	result := r.db.WithContext(ctx).
		Model(&types.Knowledge{}).
		Where(
			"tenant_id = ? AND id = ? AND knowledge_base_id = ? AND parse_status = ? AND file_path = ? AND file_hash = ?",
			tenantID, knowledgeID, kbID, types.ParseStatusReplacing, expectedFilePath, expectedFileHash,
		).
		Updates(values)
	if result.Error != nil {
		return false, result.Error
	}
	return result.RowsAffected == 1, nil
}

// StageKnowledgeFileUpdate accepts a new latest-wins replacement under a row
// lock. When no active version exists, the new version is promoted directly.
func (r *knowledgeRepository) StageKnowledgeFileUpdate(
	ctx context.Context,
	tenantID uint64,
	knowledgeID string,
	kbID string,
	payload types.JSON,
	expectedVersion *uint64,
) (*types.KnowledgeFileUpdateStageResult, error) {
	var staged *types.KnowledgeFileUpdateStageResult
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Deletion locks the knowledge before its slot as well. Keeping the same
		// order prevents an upload that passed the HTTP precheck from recreating
		// a slot after deletion has claimed the resource.
		knowledgeQuery := tx.Model(&types.Knowledge{}).
			Select("id", "parse_status").
			Where("id = ? AND tenant_id = ? AND knowledge_base_id = ?", knowledgeID, tenantID, kbID)
		if tx.Dialector.Name() != "sqlite" {
			knowledgeQuery = knowledgeQuery.Clauses(clause.Locking{Strength: "UPDATE"})
		}
		var knowledge types.Knowledge
		if err := knowledgeQuery.Take(&knowledge).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrKnowledgeNotFound
			}
			return err
		}
		if knowledge.ParseStatus == types.ParseStatusDeleting {
			return ErrKnowledgeFileUpdateDeleting
		}

		now := time.Now()
		seed := &types.KnowledgeFileUpdateSlot{
			KnowledgeID:     knowledgeID,
			TenantID:        tenantID,
			KnowledgeBaseID: kbID,
			ActiveState:     types.KnowledgeFileUpdateStateIdle,
			CreatedAt:       now,
			UpdatedAt:       now,
		}
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(seed).Error; err != nil {
			return err
		}

		query := tx.Where("knowledge_id = ?", knowledgeID)
		if tx.Dialector.Name() != "sqlite" {
			query = query.Clauses(clause.Locking{Strength: "UPDATE"})
		}
		var slot types.KnowledgeFileUpdateSlot
		if err := query.First(&slot).Error; err != nil {
			return err
		}
		if slot.TenantID != tenantID || slot.KnowledgeBaseID != kbID {
			return ErrKnowledgeFileUpdateVersionConflict
		}
		if expectedVersion != nil && slot.LatestVersion != *expectedVersion {
			return ErrKnowledgeFileUpdateVersionConflict
		}

		version := slot.LatestVersion + 1
		updates := map[string]interface{}{
			"latest_version": version,
			"last_error":     "",
			"updated_at":     now,
		}
		state := types.KnowledgeFileUpdateResultPending
		activeVersion := uint64(0)
		if slot.ActiveVersion == nil || slot.ActiveState == types.KnowledgeFileUpdateStateFailed {
			updates["active_version"] = version
			updates["active_state"] = types.KnowledgeFileUpdateStateWaiting
			updates["active_payload"] = payload
			updates["pending_version"] = nil
			updates["pending_payload"] = nil
			state = types.KnowledgeFileUpdateResultActive
			activeVersion = version
		} else {
			updates["pending_version"] = version
			updates["pending_payload"] = payload
			activeVersion = *slot.ActiveVersion
		}

		result := tx.Model(&types.KnowledgeFileUpdateSlot{}).
			Where("knowledge_id = ? AND latest_version = ?", knowledgeID, slot.LatestVersion).
			Updates(updates)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrKnowledgeFileUpdateVersionConflict
		}
		staged = &types.KnowledgeFileUpdateStageResult{
			Version:                version,
			State:                  state,
			ActiveVersion:          activeVersion,
			ReplacedPendingPayload: append(types.JSON(nil), slot.PendingPayload...),
		}
		if slot.ActiveState == types.KnowledgeFileUpdateStateFailed {
			staged.ReplacedActivePayload = append(types.JSON(nil), slot.ActivePayload...)
		}
		return nil
	})
	return staged, err
}

func (r *knowledgeRepository) GetKnowledgeFileUpdateSlot(
	ctx context.Context, tenantID uint64, knowledgeID string,
) (*types.KnowledgeFileUpdateSlot, error) {
	var slot types.KnowledgeFileUpdateSlot
	err := r.db.WithContext(ctx).
		Where("tenant_id = ? AND knowledge_id = ?", tenantID, knowledgeID).
		First(&slot).Error
	if err != nil {
		return nil, err
	}
	return &slot, nil
}

func (r *knowledgeRepository) PrepareKnowledgeFileUpdate(
	ctx context.Context, tenantID uint64, knowledgeID string, version uint64, payload types.JSON,
) (bool, error) {
	result := r.db.WithContext(ctx).Model(&types.KnowledgeFileUpdateSlot{}).
		Where("tenant_id = ? AND knowledge_id = ? AND active_version = ? AND active_state = ?",
			tenantID, knowledgeID, version, types.KnowledgeFileUpdateStateWaiting).
		Updates(map[string]interface{}{"active_payload": payload, "updated_at": time.Now()})
	return result.RowsAffected == 1, result.Error
}

func (r *knowledgeRepository) TransitionKnowledgeFileUpdateState(
	ctx context.Context,
	tenantID uint64,
	knowledgeID string,
	version uint64,
	fromState string,
	toState string,
	lastError string,
) (bool, error) {
	updates := map[string]interface{}{
		"active_state": toState,
		"updated_at":   time.Now(),
	}
	if lastError != "" || toState != types.KnowledgeFileUpdateStateFailed {
		updates["last_error"] = lastError
	}
	result := r.db.WithContext(ctx).Model(&types.KnowledgeFileUpdateSlot{}).
		Where("tenant_id = ? AND knowledge_id = ? AND active_version = ? AND active_state = ?",
			tenantID, knowledgeID, version, fromState).
		Updates(updates)
	return result.RowsAffected == 1, result.Error
}

// CompleteKnowledgeFileUpdate clears the finished active version and promotes
// the latest pending version in the same transaction.
func (r *knowledgeRepository) CompleteKnowledgeFileUpdate(
	ctx context.Context, tenantID uint64, knowledgeID string, version uint64,
) (*types.KnowledgeFileUpdateSlot, error) {
	var completed *types.KnowledgeFileUpdateSlot
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		query := tx.Where("tenant_id = ? AND knowledge_id = ?", tenantID, knowledgeID)
		if tx.Dialector.Name() != "sqlite" {
			query = query.Clauses(clause.Locking{Strength: "UPDATE"})
		}
		var slot types.KnowledgeFileUpdateSlot
		if err := query.First(&slot).Error; err != nil {
			return err
		}
		if slot.ActiveVersion == nil || *slot.ActiveVersion != version {
			completed = &slot
			return nil
		}

		updates := map[string]interface{}{
			"active_version": nil,
			"active_state":   types.KnowledgeFileUpdateStateIdle,
			"active_payload": nil,
			"last_error":     "",
			"updated_at":     time.Now(),
		}
		if slot.PendingVersion != nil {
			updates["active_version"] = *slot.PendingVersion
			updates["active_state"] = types.KnowledgeFileUpdateStateWaiting
			updates["active_payload"] = slot.PendingPayload
			updates["pending_version"] = nil
			updates["pending_payload"] = nil
		}
		if err := tx.Model(&types.KnowledgeFileUpdateSlot{}).
			Where("knowledge_id = ? AND active_version = ?", knowledgeID, version).
			Updates(updates).Error; err != nil {
			return err
		}
		if err := tx.Where("knowledge_id = ?", knowledgeID).First(&slot).Error; err != nil {
			return err
		}
		completed = &slot
		return nil
	})
	return completed, err
}

// CancelKnowledgeFileUpdates deletes the coordination row and returns its
// payloads so the caller can clean staged files after the transaction commits.
func (r *knowledgeRepository) CancelKnowledgeFileUpdates(
	ctx context.Context, tenantID uint64, knowledgeID string,
) (*types.KnowledgeFileUpdateSlot, error) {
	var cancelled *types.KnowledgeFileUpdateSlot
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		query := tx.Where("tenant_id = ? AND knowledge_id = ?", tenantID, knowledgeID)
		if tx.Dialector.Name() != "sqlite" {
			query = query.Clauses(clause.Locking{Strength: "UPDATE"})
		}
		var slot types.KnowledgeFileUpdateSlot
		if err := query.First(&slot).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil
			}
			return err
		}
		if err := tx.Delete(&types.KnowledgeFileUpdateSlot{}, "knowledge_id = ?", knowledgeID).Error; err != nil {
			return err
		}
		cancelled = &slot
		return nil
	})
	return cancelled, err
}

// CancelFailedKnowledgeFileUpdate removes only the exact failed active
// version observed by the caller. A concurrent upload cannot be discarded.
func (r *knowledgeRepository) CancelFailedKnowledgeFileUpdate(
	ctx context.Context, tenantID uint64, knowledgeID string, activeVersion uint64,
) (*types.KnowledgeFileUpdateSlot, error) {
	var cancelled *types.KnowledgeFileUpdateSlot
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		query := tx.Where("tenant_id = ? AND knowledge_id = ?", tenantID, knowledgeID)
		if tx.Dialector.Name() != "sqlite" {
			query = query.Clauses(clause.Locking{Strength: "UPDATE"})
		}
		var slot types.KnowledgeFileUpdateSlot
		if err := query.First(&slot).Error; err != nil {
			return err
		}
		if slot.ActiveVersion == nil || *slot.ActiveVersion != activeVersion ||
			slot.ActiveState != types.KnowledgeFileUpdateStateFailed {
			return ErrKnowledgeFileUpdateStateConflict
		}
		result := tx.Where(
			"tenant_id = ? AND knowledge_id = ? AND active_version = ? AND active_state = ?",
			tenantID, knowledgeID, activeVersion, types.KnowledgeFileUpdateStateFailed,
		).Delete(&types.KnowledgeFileUpdateSlot{})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrKnowledgeFileUpdateStateConflict
		}
		cancelled = &slot
		return nil
	})
	return cancelled, err
}

// BeginKnowledgeDeletion serializes deletion against file-update staging and
// returns removed slots so staged files can be cleaned after the commit.
func (r *knowledgeRepository) BeginKnowledgeDeletion(
	ctx context.Context, tenantID uint64, knowledgeIDs []string,
) ([]*types.KnowledgeFileUpdateSlot, error) {
	if len(knowledgeIDs) == 0 {
		return nil, nil
	}
	uniqueIDs := make([]string, 0, len(knowledgeIDs))
	seen := make(map[string]struct{}, len(knowledgeIDs))
	for _, id := range knowledgeIDs {
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		uniqueIDs = append(uniqueIDs, id)
	}
	if len(uniqueIDs) == 0 {
		return nil, ErrKnowledgeNotFound
	}

	var cancelled []*types.KnowledgeFileUpdateSlot
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		knowledgeQuery := tx.Model(&types.Knowledge{}).
			Select("id").
			Where("tenant_id = ? AND id IN ?", tenantID, uniqueIDs).
			Order("id ASC")
		if tx.Dialector.Name() != "sqlite" {
			knowledgeQuery = knowledgeQuery.Clauses(clause.Locking{Strength: "UPDATE"})
		}
		var rows []types.Knowledge
		if err := knowledgeQuery.Find(&rows).Error; err != nil {
			return err
		}
		if len(rows) != len(uniqueIDs) {
			return ErrKnowledgeNotFound
		}

		if err := tx.Model(&types.Knowledge{}).
			Where("tenant_id = ? AND id IN ?", tenantID, uniqueIDs).
			Updates(map[string]interface{}{
				"parse_status": types.ParseStatusDeleting,
				"updated_at":   time.Now(),
			}).Error; err != nil {
			return err
		}

		slotQuery := tx.Where("tenant_id = ? AND knowledge_id IN ?", tenantID, uniqueIDs).
			Order("knowledge_id ASC")
		if tx.Dialector.Name() != "sqlite" {
			slotQuery = slotQuery.Clauses(clause.Locking{Strength: "UPDATE"})
		}
		if err := slotQuery.Find(&cancelled).Error; err != nil {
			return err
		}
		if len(cancelled) == 0 {
			return nil
		}
		return tx.Where("tenant_id = ? AND knowledge_id IN ?", tenantID, uniqueIDs).
			Delete(&types.KnowledgeFileUpdateSlot{}).Error
	})
	return cancelled, err
}

func (r *knowledgeRepository) ListRecoverableKnowledgeFileUpdates(
	ctx context.Context, limit int,
) ([]*types.KnowledgeFileUpdateSlot, error) {
	if limit <= 0 {
		limit = 1000
	}
	var slots []*types.KnowledgeFileUpdateSlot
	err := r.db.WithContext(ctx).
		Where("active_version IS NOT NULL OR pending_version IS NOT NULL").
		Order("updated_at ASC").Limit(limit).Find(&slots).Error
	return slots, err
}

// UpdateActiveDeletingKnowledgeColumns only touches rows that are still visible
// to normal queries and have not moved out of the transient deleting state.
func (r *knowledgeRepository) UpdateActiveDeletingKnowledgeColumns(
	ctx context.Context,
	id string,
	values map[string]interface{},
) (bool, error) {
	if len(values) == 0 {
		return false, nil
	}
	result := r.db.WithContext(ctx).
		Model(&types.Knowledge{}).
		Where("id = ? AND parse_status = ?", id, types.ParseStatusDeleting).
		Updates(values)
	if result.Error != nil {
		return false, result.Error
	}
	return result.RowsAffected > 0, nil
}

// FinalizeSubtask atomically decrements pending_subtasks_count and, when
// the counter reaches zero while parse_status is still 'finalizing',
// flips the row to 'completed' in the same statement so concurrent
// subtask completions can't race the promotion. Both this promotion and
// SetFinalizing clear error_message: a row that re-enters processing or
// finishes successfully must not keep displaying a failure from a
// previous attempt.
//
// Returns (newCount, promoted, error). promoted is true iff this caller
// was the one whose UPDATE flipped 'finalizing'→'completed'.
//
// The implementation is two statements (atomic decrement, then a guarded
// promote UPDATE) because GORM does not expose a portable RETURNING
// across PostgreSQL and SQLite. The promote UPDATE's WHERE clause
// (parse_status='finalizing' AND pending_subtasks_count=0) makes it
// safe to run from any number of concurrent callers — at most one wins.
func (r *knowledgeRepository) FinalizeSubtask(
	ctx context.Context, id string,
) (int, bool, error) {
	now := time.Now()
	// 1) Atomic decrement, clamped at zero. The `pending_subtasks_count > 0`
	//    guard is purely a safety net for accounting bugs — under normal
	//    operation each subtask handler decrements at most once per task,
	//    so the counter cannot go negative.
	res := r.db.WithContext(ctx).Model(&types.Knowledge{}).
		Where("id = ? AND pending_subtasks_count > 0", id).
		Updates(map[string]interface{}{
			"pending_subtasks_count": gorm.Expr("pending_subtasks_count - 1"),
			"updated_at":             now,
		})
	if res.Error != nil {
		return 0, false, res.Error
	}

	// 2) Guarded promote. EVERY caller unconditionally attempts this after
	//    decrementing — we must NOT gate it on a separate SELECT of the
	//    counter. That read can be served by a lagging read-replica (or a
	//    stale connection snapshot) and return a non-zero value even after
	//    the counter has truly reached zero on the primary; if every caller
	//    trusts that stale read, NONE of them runs the promote and the row
	//    is stranded in `finalizing` forever (the observed "stuck
	//    pending_subtasks_count" bug). The promote is a WRITE, so it executes
	//    on the primary and its `pending_subtasks_count = 0` WHERE clause is
	//    the single authoritative, atomic check on the live row: only the
	//    caller whose decrement actually brought the counter to zero matches,
	//    and cancel/delete cannot be clobbered by a late promote.
	promoteRes := r.db.WithContext(ctx).Model(&types.Knowledge{}).
		Where("id = ? AND parse_status = ? AND pending_subtasks_count = 0",
			id, types.ParseStatusFinalizing).
		Updates(map[string]interface{}{
			"parse_status":  types.ParseStatusCompleted,
			"error_message": "",
			"processed_at":  now,
			"updated_at":    now,
		})
	if promoteRes.Error != nil {
		return 0, false, promoteRes.Error
	}
	promoted := promoteRes.RowsAffected > 0

	// 3) Best-effort re-read of the new count for diagnostics/return value
	//    only. This read may be replica-stale and is intentionally NOT used
	//    to decide whether to promote (see above). A read failure here does
	//    not affect correctness, so we don't propagate it as an error.
	var snap struct {
		PendingSubtasksCount int `gorm:"column:pending_subtasks_count"`
	}
	if err := r.db.WithContext(ctx).Model(&types.Knowledge{}).
		Select("pending_subtasks_count").
		Where("id = ?", id).Take(&snap).Error; err != nil {
		return 0, promoted, nil
	}
	return snap.PendingSubtasksCount, promoted, nil
}

// SetFinalizing atomically transitions a row from 'processing' to
// 'finalizing' and seeds pending_subtasks_count. Used by
// KnowledgePostProcess.Handle as the single durable handoff between
// the synchronous parse stage and the asynchronous enrichment fan-out.
//
// The transition is conditional on parse_status='processing' so a row
// that the user cancelled / deleted between ProcessDocument finishing
// and post-process starting will NOT get hijacked into finalizing.
// Returns whether the transition happened.
func (r *knowledgeRepository) SetFinalizing(
	ctx context.Context, id string, expectedSubtasks int,
) (bool, error) {
	if expectedSubtasks < 0 {
		expectedSubtasks = 0
	}
	now := time.Now()
	res := r.db.WithContext(ctx).Model(&types.Knowledge{}).
		Where("id = ? AND parse_status = ?", id, types.ParseStatusProcessing).
		Updates(map[string]interface{}{
			"parse_status":           types.ParseStatusFinalizing,
			"pending_subtasks_count": expectedSubtasks,
			"error_message":          "",
			"updated_at":             now,
		})
	if res.Error != nil {
		return false, res.Error
	}
	return res.RowsAffected > 0, nil
}

// CountKnowledgeByKnowledgeBaseID counts the number of knowledge items in a knowledge base
func (r *knowledgeRepository) CountKnowledgeByKnowledgeBaseID(
	ctx context.Context,
	tenantID uint64,
	kbID string,
) (int64, error) {
	var count int64
	err := r.db.WithContext(ctx).Model(&types.Knowledge{}).
		Where("tenant_id = ? AND knowledge_base_id = ?", tenantID, kbID).
		Count(&count).Error
	return count, err
}

// CountKnowledgeByStatus counts the number of knowledge items with the specified parse status
func (r *knowledgeRepository) CountKnowledgeByStatus(
	ctx context.Context,
	tenantID uint64,
	kbID string,
	parseStatuses []string,
) (int64, error) {
	if len(parseStatuses) == 0 {
		return 0, nil
	}

	var count int64
	query := r.db.WithContext(ctx).Model(&types.Knowledge{}).
		Where("tenant_id = ? AND knowledge_base_id = ?", tenantID, kbID).
		Where("parse_status IN ?", parseStatuses)

	if err := query.Count(&count).Error; err != nil {
		return 0, err
	}

	return count, nil
}

// SearchKnowledge searches knowledge items by keyword across the tenant
// If keyword is empty, returns recent files
// Only returns documents from document-type knowledge bases (excludes FAQ)
// Returns (results, hasMore, error)
// FindByMetadataKey finds a knowledge item by a key-value pair in the metadata JSON column.
// Uses Postgres jsonb operator: metadata->>'key' = 'value'.
func (r *knowledgeRepository) FindByMetadataKey(
	ctx context.Context,
	tenantID uint64,
	kbID string,
	key string,
	value string,
) (*types.Knowledge, error) {
	var knowledge types.Knowledge
	err := r.db.WithContext(ctx).
		Where("tenant_id = ? AND knowledge_base_id = ? AND deleted_at IS NULL", tenantID, kbID).
		Where("metadata->>? = ?", key, value).
		First(&knowledge).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &knowledge, nil
}

// FindByMetadataKeyPrefix finds knowledge items whose metadata[key] starts with
// the given prefix. Used to sweep an external node's attachment sub-items on re-sync.
func (r *knowledgeRepository) FindByMetadataKeyPrefix(
	ctx context.Context,
	tenantID uint64,
	kbID string,
	key string,
	prefix string,
) ([]*types.Knowledge, error) {
	escaped := escapeLikeKeyword(prefix)
	var items []*types.Knowledge
	// The JSON key is embedded as a SQL literal (metadata->>'external_id'), NOT a
	// bind parameter. PostgreSQL only uses the expression index
	// idx_knowledges_kb_metadata_external_id (built on the literal expression
	// (metadata->>'external_id')) when that exact expression appears in the query;
	// a bound metadata->>$1 is a structurally different expression the planner
	// cannot match, so it would silently fall back to a heap scan. key is an
	// internal, caller-supplied field name (always "external_id"); single-quotes
	// are doubled defensively so the literal is always well-formed.
	//
	// The prefix pattern stays a bind parameter: an unnamed prepared statement is
	// custom-planned with the actual value, so LIKE 'prefix%' still extracts the
	// prefix and drives the index. The explicit ESCAPE '\' keeps backslash-escaped
	// wildcards (e.g. \_) literal on both PostgreSQL and SQLite.
	keyExpr := "metadata->>'" + strings.ReplaceAll(key, "'", "''") + "'"
	err := r.db.WithContext(ctx).
		Where("tenant_id = ? AND knowledge_base_id = ? AND deleted_at IS NULL", tenantID, kbID).
		Where(keyExpr+" LIKE ? ESCAPE ?", escaped+"%", `\`).
		Find(&items).Error
	if err != nil {
		return nil, err
	}
	return items, nil
}

func (r *knowledgeRepository) SearchKnowledge(
	ctx context.Context,
	tenantID uint64,
	keyword string,
	offset, limit int,
	fileTypes []string,
) ([]*types.Knowledge, bool, error) {
	// Use raw query to properly map knowledge_base_name
	type KnowledgeWithKBName struct {
		types.Knowledge
		KnowledgeBaseName string `gorm:"column:knowledge_base_name"`
	}

	var results []KnowledgeWithKBName
	query := r.db.WithContext(ctx).
		Table("knowledges").
		Select("knowledges.*, knowledge_bases.name as knowledge_base_name").
		Joins("JOIN knowledge_bases ON knowledge_bases.id = knowledges.knowledge_base_id").
		Where("knowledges.tenant_id = ?", tenantID).
		Where("knowledge_bases.type = ?", types.KnowledgeBaseTypeDocument).
		Where("knowledges.deleted_at IS NULL")

	// If keyword is provided, filter by file_name or title (case-insensitive).
	if keyword != "" {
		escaped := strings.ToLower(escapeLikeKeyword(keyword))
		query = query.Where("(LOWER(knowledges.file_name) LIKE ? OR LOWER(knowledges.title) LIKE ?)", "%"+escaped+"%", "%"+escaped+"%")
	}

	// If fileTypes is provided, filter by file extension or type
	if len(fileTypes) > 0 {
		seen := make(map[string]bool)
		var uniquePatterns []string
		includeURL := false
		for _, ft := range fileTypes {
			ft = strings.ToLower(strings.TrimPrefix(ft, "."))
			if ft == "url" || ft == "html" {
				includeURL = true
				continue
			}
			pattern := "%." + ft
			if !seen[pattern] {
				seen[pattern] = true
				uniquePatterns = append(uniquePatterns, pattern)
			}
			// Handle common aliases
			var aliases []string
			switch ft {
			case "xlsx":
				aliases = []string{"%.xls"}
			case "xls":
				aliases = []string{"%.xlsx"}
			case "docx":
				aliases = []string{"%.doc"}
			case "doc":
				aliases = []string{"%.docx"}
			case "jpg":
				aliases = []string{"%.jpeg", "%.png"}
			case "jpeg":
				aliases = []string{"%.jpg", "%.png"}
			case "png":
				aliases = []string{"%.jpg", "%.jpeg"}
			}
			for _, alias := range aliases {
				if !seen[alias] {
					seen[alias] = true
					uniquePatterns = append(uniquePatterns, alias)
				}
			}
		}
		var orConditions []string
		var args []interface{}
		for _, p := range uniquePatterns {
			orConditions = append(orConditions, "LOWER(knowledges.file_name) LIKE ?")
			args = append(args, p)
		}
		if includeURL {
			orConditions = append(orConditions, "knowledges.type = ?")
			args = append(args, "url")
		}
		if len(orConditions) > 0 {
			query = query.Where("("+strings.Join(orConditions, " OR ")+")", args...)
		}
	}

	// Fetch limit+1 to check if there are more results
	err := query.Order("knowledges.created_at DESC").
		Offset(offset).
		Limit(limit + 1).
		Scan(&results).Error
	if err != nil {
		return nil, false, err
	}

	// Check if there are more results
	hasMore := len(results) > limit
	if hasMore {
		results = results[:limit]
	}

	// Convert to []*types.Knowledge
	knowledges := make([]*types.Knowledge, len(results))
	for i, r := range results {
		k := r.Knowledge
		k.KnowledgeBaseName = r.KnowledgeBaseName
		knowledges[i] = &k
	}
	return knowledges, hasMore, nil
}

// SearchKnowledgeInScopes searches knowledge items by keyword within the given (tenant_id, kb_id) scopes (e.g. own + shared KBs).
func (r *knowledgeRepository) SearchKnowledgeInScopes(
	ctx context.Context,
	scopes []types.KnowledgeSearchScope,
	keyword string,
	offset, limit int,
	fileTypes []string,
) ([]*types.Knowledge, bool, int64, error) {
	if len(scopes) == 0 {
		return nil, false, 0, nil
	}

	type KnowledgeWithKBName struct {
		types.Knowledge
		KnowledgeBaseName string `gorm:"column:knowledge_base_name"`
	}

	placeholders := make([]string, len(scopes))
	args := make([]interface{}, 0, len(scopes)*2)
	for i, s := range scopes {
		placeholders[i] = "(?,?)"
		args = append(args, s.TenantID, s.KBID)
	}
	scopeCondition := "(knowledges.tenant_id, knowledges.knowledge_base_id) IN (" + strings.Join(placeholders, ",") + ")"

	query := r.db.WithContext(ctx).
		Table("knowledges").
		Select("knowledges.*, knowledge_bases.name as knowledge_base_name").
		Joins("JOIN knowledge_bases ON knowledge_bases.id = knowledges.knowledge_base_id AND knowledge_bases.tenant_id = knowledges.tenant_id").
		Where(scopeCondition, args...).
		Where("knowledge_bases.type = ?", types.KnowledgeBaseTypeDocument).
		Where("knowledges.deleted_at IS NULL")

	if keyword != "" {
		escaped := strings.ToLower(escapeLikeKeyword(keyword))
		query = query.Where("(LOWER(knowledges.file_name) LIKE ? OR LOWER(knowledges.title) LIKE ?)", "%"+escaped+"%", "%"+escaped+"%")
	}

	if len(fileTypes) > 0 {
		seen := make(map[string]bool)
		var uniquePatterns []string
		includeURL := false
		for _, ft := range fileTypes {
			ft = strings.ToLower(strings.TrimPrefix(ft, "."))
			if ft == "url" || ft == "html" {
				includeURL = true
				continue
			}
			pattern := "%." + ft
			if !seen[pattern] {
				seen[pattern] = true
				uniquePatterns = append(uniquePatterns, pattern)
			}
			var aliases []string
			switch ft {
			case "xlsx":
				aliases = []string{"%.xls"}
			case "xls":
				aliases = []string{"%.xlsx"}
			case "docx":
				aliases = []string{"%.doc"}
			case "doc":
				aliases = []string{"%.docx"}
			case "jpg":
				aliases = []string{"%.jpeg", "%.png"}
			case "jpeg":
				aliases = []string{"%.jpg", "%.png"}
			case "png":
				aliases = []string{"%.jpg", "%.jpeg"}
			}
			for _, alias := range aliases {
				if !seen[alias] {
					seen[alias] = true
					uniquePatterns = append(uniquePatterns, alias)
				}
			}
		}
		var orConditions []string
		var ftArgs []interface{}
		for _, p := range uniquePatterns {
			orConditions = append(orConditions, "LOWER(knowledges.file_name) LIKE ?")
			ftArgs = append(ftArgs, p)
		}
		if includeURL {
			orConditions = append(orConditions, "knowledges.type = ?")
			ftArgs = append(ftArgs, "url")
		}
		if len(orConditions) > 0 {
			query = query.Where("("+strings.Join(orConditions, " OR ")+")", ftArgs...)
		}
	}

	var total int64
	if err := query.Session(&gorm.Session{}).Count(&total).Error; err != nil {
		return nil, false, 0, err
	}

	var results []KnowledgeWithKBName
	err := query.Order("knowledges.created_at DESC").
		Offset(offset).
		Limit(limit + 1).
		Scan(&results).Error
	if err != nil {
		return nil, false, 0, err
	}

	hasMore := len(results) > limit
	if hasMore {
		results = results[:limit]
	}

	knowledges := make([]*types.Knowledge, len(results))
	for i, r := range results {
		k := r.Knowledge
		k.KnowledgeBaseName = r.KnowledgeBaseName
		knowledges[i] = &k
	}
	return knowledges, hasMore, total, nil
}

// ListIDsByTagIDs returns all knowledge IDs that have any of the specified tag IDs (OR semantics)
func (r *knowledgeRepository) ListIDsByTagIDs(
	ctx context.Context,
	tenantID uint64,
	kbID string,
	tagIDs []string,
) ([]string, error) {
	if len(tagIDs) == 0 {
		return nil, nil
	}
	var ids []string
	err := r.db.WithContext(ctx).Model(&types.Knowledge{}).
		Joins("JOIN knowledge_tag_relations ktr ON knowledges.id = ktr.knowledge_id").
		Where("knowledges.tenant_id = ? AND knowledges.knowledge_base_id = ? AND ktr.tag_id IN (?)",
			tenantID, kbID, tagIDs).
		Distinct("knowledges.id").
		Pluck("knowledges.id", &ids).Error
	return ids, err
}
