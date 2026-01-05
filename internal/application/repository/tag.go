package repository

import (
	"context"
	"strings"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"gorm.io/gorm"
)

// knowledgeTagRepository is a repository for knowledge tags
type knowledgeTagRepository struct {
	db *gorm.DB
}

// NewKnowledgeTagRepository creates a new tag repository.
func NewKnowledgeTagRepository(db *gorm.DB) interfaces.KnowledgeTagRepository {
	return &knowledgeTagRepository{db: db}
}

// Create creates a new knowledge tag
func (r *knowledgeTagRepository) Create(ctx context.Context, tag *types.KnowledgeTag) error {
	return r.db.WithContext(ctx).Create(tag).Error
}

// Update updates a knowledge tag
func (r *knowledgeTagRepository) Update(ctx context.Context, tag *types.KnowledgeTag) error {
	return r.db.WithContext(ctx).Save(tag).Error
}

// GetByID gets a knowledge tag by ID
func (r *knowledgeTagRepository) GetByID(ctx context.Context, tenantID uint64, id string) (*types.KnowledgeTag, error) {
	var tag types.KnowledgeTag
	if err := r.db.WithContext(ctx).
		Where("tenant_id = ? AND id = ?", tenantID, id).
		First(&tag).Error; err != nil {
		return nil, err
	}
	return &tag, nil
}

// GetByIDs retrieves multiple tags by their IDs in a single query
func (r *knowledgeTagRepository) GetByIDs(ctx context.Context, tenantID uint64, ids []string) ([]*types.KnowledgeTag, error) {
	if len(ids) == 0 {
		return []*types.KnowledgeTag{}, nil
	}
	var tags []*types.KnowledgeTag
	if err := r.db.WithContext(ctx).
		Where("tenant_id = ? AND id IN (?)", tenantID, ids).
		Find(&tags).Error; err != nil {
		return nil, err
	}
	return tags, nil
}

// GetByName gets a knowledge tag by name
func (r *knowledgeTagRepository) GetByName(ctx context.Context, tenantID uint64, kbID string, name string) (*types.KnowledgeTag, error) {
	var tag types.KnowledgeTag
	if err := r.db.WithContext(ctx).
		Where("tenant_id = ? AND knowledge_base_id = ? AND name = ?", tenantID, kbID, name).
		First(&tag).Error; err != nil {
		return nil, err
	}
	return &tag, nil
}

// ListByKB lists knowledge tags by knowledge base ID with pagination and optional keyword filtering.
func (r *knowledgeTagRepository) ListByKB(
	ctx context.Context,
	tenantID uint64,
	kbID string,
	page *types.Pagination,
	keyword string,
) ([]*types.KnowledgeTag, int64, error) {
	if page == nil {
		page = &types.Pagination{}
	}
	keyword = strings.TrimSpace(keyword)

	var total int64
	baseQuery := r.db.WithContext(ctx).Model(&types.KnowledgeTag{}).
		Where("tenant_id = ? AND knowledge_base_id = ?", tenantID, kbID)
	if keyword != "" {
		baseQuery = baseQuery.Where("name LIKE ?", "%"+keyword+"%")
	}

	if err := baseQuery.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	dataQuery := r.db.WithContext(ctx).
		Where("tenant_id = ? AND knowledge_base_id = ?", tenantID, kbID)
	if keyword != "" {
		dataQuery = dataQuery.Where("name LIKE ?", "%"+keyword+"%")
	}

	var tags []*types.KnowledgeTag
	if err := dataQuery.
		Order("sort_order ASC, created_at ASC").
		Offset(page.Offset()).
		Limit(page.Limit()).
		Find(&tags).Error; err != nil {
		return nil, 0, err
	}

	return tags, total, nil
}

// Delete deletes a knowledge tag
func (r *knowledgeTagRepository) Delete(ctx context.Context, tenantID uint64, id string) error {
	return r.db.WithContext(ctx).
		Where("tenant_id = ? AND id = ?", tenantID, id).
		Delete(&types.KnowledgeTag{}).Error
}

// CountReferences returns the number of knowledges and chunks that reference this tag
func (r *knowledgeTagRepository) CountReferences(
	ctx context.Context,
	tenantID uint64,
	kbID string,
	tagID string,
) (knowledgeCount int64, chunkCount int64, err error) {
	if err = r.db.WithContext(ctx).
		Model(&types.Knowledge{}).
		Where("tenant_id = ? AND knowledge_base_id = ? AND tag_id = ?", tenantID, kbID, tagID).
		Count(&knowledgeCount).Error; err != nil {
		return
	}
	if err = r.db.WithContext(ctx).
		Model(&types.Chunk{}).
		Where("tenant_id = ? AND knowledge_base_id = ? AND tag_id = ?", tenantID, kbID, tagID).
		Count(&chunkCount).Error; err != nil {
		return
	}
	return
}

// CountUntaggedReferences returns the number of knowledges and chunks without a tag
func (r *knowledgeTagRepository) CountUntaggedReferences(
	ctx context.Context,
	tenantID uint64,
	kbID string,
) (knowledgeCount int64, chunkCount int64, err error) {
	if err = r.db.WithContext(ctx).
		Model(&types.Knowledge{}).
		Where("tenant_id = ? AND knowledge_base_id = ? AND (tag_id = '' OR tag_id IS NULL)", tenantID, kbID).
		Count(&knowledgeCount).Error; err != nil {
		return
	}
	if err = r.db.WithContext(ctx).
		Model(&types.Chunk{}).
		Where("tenant_id = ? AND knowledge_base_id = ? AND (tag_id = '' OR tag_id IS NULL)", tenantID, kbID).
		Count(&chunkCount).Error; err != nil {
		return
	}
	return
}

// DeleteUnusedTags deletes tags that are not referenced by any knowledge or chunk.
// Returns the number of deleted tags.
func (r *knowledgeTagRepository) DeleteUnusedTags(ctx context.Context, tenantID uint64, kbID string) (int64, error) {
	// Delete tags that have no references in both knowledges and chunks tables
	result := r.db.WithContext(ctx).
		Where("tenant_id = ? AND knowledge_base_id = ?", tenantID, kbID).
		Where("id NOT IN (SELECT DISTINCT tag_id FROM knowledges WHERE tenant_id = ? AND knowledge_base_id = ? AND tag_id IS NOT NULL AND tag_id != '')", tenantID, kbID).
		Where("id NOT IN (SELECT DISTINCT tag_id FROM chunks WHERE tenant_id = ? AND knowledge_base_id = ? AND tag_id IS NOT NULL AND tag_id != '')", tenantID, kbID).
		Delete(&types.KnowledgeTag{})
	return result.RowsAffected, result.Error
}
