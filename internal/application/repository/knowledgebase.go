package repository

import (
	"context"
	"errors"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"gorm.io/gorm"
)

var ErrKnowledgeBaseNotFound = errors.New("knowledge base not found")

// knowledgeBaseRepository implements the KnowledgeBaseRepository interface
type knowledgeBaseRepository struct {
	db *gorm.DB
}

// NewKnowledgeBaseRepository creates a new knowledge base repository
func NewKnowledgeBaseRepository(db *gorm.DB) interfaces.KnowledgeBaseRepository {
	return &knowledgeBaseRepository{db: db}
}

// CreateKnowledgeBase creates a new knowledge base
func (r *knowledgeBaseRepository) CreateKnowledgeBase(ctx context.Context, kb *types.KnowledgeBase) error {
	return r.db.WithContext(ctx).Create(kb).Error
}

// GetKnowledgeBaseByID gets a knowledge base by id
func (r *knowledgeBaseRepository) GetKnowledgeBaseByID(ctx context.Context, id string) (*types.KnowledgeBase, error) {
	var kb types.KnowledgeBase
	if err := r.db.WithContext(ctx).Where("id = ?", id).First(&kb).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrKnowledgeBaseNotFound
		}
		return nil, err
	}
	return &kb, nil
}

// GetKnowledgeBaseByIDs gets knowledge bases by multiple ids
func (r *knowledgeBaseRepository) GetKnowledgeBaseByIDs(ctx context.Context, ids []string) ([]*types.KnowledgeBase, error) {
	if len(ids) == 0 {
		return []*types.KnowledgeBase{}, nil
	}
	var kbs []*types.KnowledgeBase
	if err := r.db.WithContext(ctx).Where("id IN ?", ids).Find(&kbs).Error; err != nil {
		return nil, err
	}
	return kbs, nil
}

// ListKnowledgeBases lists all knowledge bases
func (r *knowledgeBaseRepository) ListKnowledgeBases(ctx context.Context) ([]*types.KnowledgeBase, error) {
	var kbs []*types.KnowledgeBase
	if err := r.db.WithContext(ctx).Find(&kbs).Error; err != nil {
		return nil, err
	}
	return kbs, nil
}

// ListKnowledgeBasesByTenantID lists all knowledge bases by tenant id
func (r *knowledgeBaseRepository) ListKnowledgeBasesByTenantID(
	ctx context.Context, tenantID uint64,
) ([]*types.KnowledgeBase, error) {
	var kbs []*types.KnowledgeBase
	if err := r.db.WithContext(ctx).Where("tenant_id = ? AND is_temporary = ?", tenantID, false).
		Order("created_at DESC").Find(&kbs).Error; err != nil {
		return nil, err
	}
	return kbs, nil
}

// UpdateKnowledgeBase updates a knowledge base
func (r *knowledgeBaseRepository) UpdateKnowledgeBase(ctx context.Context, kb *types.KnowledgeBase) error {
	return r.db.WithContext(ctx).Save(kb).Error
}

// DeleteKnowledgeBase deletes a knowledge base
func (r *knowledgeBaseRepository) DeleteKnowledgeBase(ctx context.Context, id string) error {
	return r.db.WithContext(ctx).Where("id = ?", id).Delete(&types.KnowledgeBase{}).Error
}
