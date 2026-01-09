package repository

import (
	"context"
	"errors"
	"strings"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"gorm.io/gorm"
)

var ErrKnowledgeNotFound = errors.New("knowledge not found")

// omitFieldsOnUpdate defines fields to omit when updating knowledge
var omitFieldsOnUpdate = []string{"DeletedAt"}

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

// ListPagedKnowledgeByKnowledgeBaseID lists all knowledge in a knowledge base with pagination
func (r *knowledgeRepository) ListPagedKnowledgeByKnowledgeBaseID(
	ctx context.Context,
	tenantID uint64,
	kbID string,
	page *types.Pagination,
	tagID string,
	keyword string,
	fileType string,
) ([]*types.Knowledge, int64, error) {
	var knowledges []*types.Knowledge
	var total int64

	query := r.db.WithContext(ctx).Model(&types.Knowledge{}).
		Where("tenant_id = ? AND knowledge_base_id = ?", tenantID, kbID)
	if tagID != "" {
		if tagID == types.UntaggedTagID {
			// Special value to filter entries without a tag
			query = query.Where("tag_id = '' OR tag_id IS NULL")
		} else {
			query = query.Where("tag_id = ?", tagID)
		}
	}
	if keyword != "" {
		query = query.Where("file_name LIKE ?", "%"+keyword+"%")
	}
	if fileType != "" {
		if fileType == "manual" {
			query = query.Where("type = ?", "manual")
		} else if fileType == "url" {
			query = query.Where("type = ?", "url")
		} else {
			query = query.Where("file_type = ?", fileType)
		}
	}

	// Query total count first
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	// Then query paginated data
	dataQuery := r.db.WithContext(ctx).
		Where("tenant_id = ? AND knowledge_base_id = ?", tenantID, kbID)
	if tagID != "" {
		if tagID == types.UntaggedTagID {
			// Special value to filter entries without a tag
			dataQuery = dataQuery.Where("tag_id = '' OR tag_id IS NULL")
		} else {
			dataQuery = dataQuery.Where("tag_id = ?", tagID)
		}
	}
	if keyword != "" {
		dataQuery = dataQuery.Where("file_name LIKE ?", "%"+keyword+"%")
	}
	if fileType != "" {
		if fileType == "manual" {
			dataQuery = dataQuery.Where("type = ?", "manual")
		} else if fileType == "url" {
			dataQuery = dataQuery.Where("type = ?", "url")
		} else {
			dataQuery = dataQuery.Where("file_type = ?", fileType)
		}
	}

	if err := dataQuery.
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
		query = query.Where("knowledges.file_name LIKE ? ", "%"+keyword+"%")
	}

	// If fileTypes is provided, filter by file extension
	if len(fileTypes) > 0 {
		// Build file extension patterns (e.g., "%.csv", "%.xlsx")
		seen := make(map[string]bool)
		var uniquePatterns []string
		for _, ft := range fileTypes {
			ft = strings.ToLower(strings.TrimPrefix(ft, "."))
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
		// Build OR conditions for file extensions
		if len(uniquePatterns) > 0 {
			orConditions := make([]string, len(uniquePatterns))
			args := make([]interface{}, len(uniquePatterns))
			for i, p := range uniquePatterns {
				orConditions[i] = "LOWER(knowledges.file_name) LIKE ?"
				args[i] = p
			}
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
