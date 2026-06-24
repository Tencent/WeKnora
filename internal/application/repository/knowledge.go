package repository

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"gorm.io/gorm"
)

var ErrKnowledgeNotFound = errors.New("knowledge not found")

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
		escaped := escapeLikeKeyword(filter.Keyword)
		query = query.Where("(file_name LIKE ? OR title LIKE ?)", "%"+escaped+"%", "%"+escaped+"%")
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
	}
	if !filter.UpdatedFrom.IsZero() {
		query = query.Where("updated_at >= ?", filter.UpdatedFrom)
	}
	if !filter.UpdatedTo.IsZero() {
		query = query.Where("updated_at <= ?", filter.UpdatedTo)
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

	return knowledges, total, nil
}

// UpdateKnowledge updates knowledge
func (r *knowledgeRepository) UpdateKnowledge(ctx context.Context, knowledge *types.Knowledge) error {
	err := r.db.WithContext(ctx).Omit(omitFieldsOnUpdate...).Save(knowledge).Error
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
	return knowledge, nil
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

	switch params.Type {
	case "file":
		// If file hash exists, prioritize exact match using hash
		if params.FileHash != "" {
			var knowledge types.Knowledge
			err := query.Where("file_hash = ?", params.FileHash).First(&knowledge).Error
			if err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					return false, nil, nil
				}
				return false, nil, err
			}
			return true, &knowledge, nil
		}

		// If no hash or hash doesn't match, use filename and size
		if params.FileName != "" && params.FileSize > 0 {
			var knowledge types.Knowledge
			err := query.Where(
				"file_name = ? AND file_size = ?",
				params.FileName, params.FileSize,
			).First(&knowledge).Error
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

func (r *knowledgeRepository) AminusB(
	ctx context.Context,
	Atenant uint64, A string,
	Btenant uint64, B string,
) ([]string, error) {
	knowledgeIDs := []string{}
	subQuery := r.db.Model(&types.Knowledge{}).
		Where("tenant_id = ? AND knowledge_base_id = ?", Btenant, B).Select("file_hash")
	err := r.db.Model(&types.Knowledge{}).
		Where("tenant_id = ? AND knowledge_base_id = ?", Atenant, A).
		Where("file_hash NOT IN (?)", subQuery).
		Pluck("id", &knowledgeIDs).
		Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return knowledgeIDs, nil
	}
	return knowledgeIDs, err
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
// subtask completions can't race the promotion.
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
			"parse_status": types.ParseStatusCompleted,
			"processed_at": now,
			"updated_at":   now,
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

	// If keyword is provided, filter by file_name or title
	if keyword != "" {
		escaped := escapeLikeKeyword(keyword)
		query = query.Where("(knowledges.file_name LIKE ? OR knowledges.title LIKE ?)", "%"+escaped+"%", "%"+escaped+"%")
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
) ([]*types.Knowledge, bool, error) {
	if len(scopes) == 0 {
		return nil, false, nil
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
		escaped := escapeLikeKeyword(keyword)
		query = query.Where("(knowledges.file_name LIKE ? OR knowledges.title LIKE ?)", "%"+escaped+"%", "%"+escaped+"%")
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

	var results []KnowledgeWithKBName
	err := query.Order("knowledges.created_at DESC").
		Offset(offset).
		Limit(limit + 1).
		Scan(&results).Error
	if err != nil {
		return nil, false, err
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
	return knowledges, hasMore, nil
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
