package repository

import (
	"context"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"gorm.io/gorm"
)

type knowledgeScopeAuthorizationRepository struct {
	db *gorm.DB
}

var _ interfaces.KnowledgeScopeAuthorizationRepository = (*knowledgeScopeAuthorizationRepository)(nil)

// NewKnowledgeScopeAuthorizationRepository creates the narrow metadata reader.
func NewKnowledgeScopeAuthorizationRepository(
	db *gorm.DB,
) interfaces.KnowledgeScopeAuthorizationRepository {
	return &knowledgeScopeAuthorizationRepository{db: db}
}

func (r *knowledgeScopeAuthorizationRepository) ListKnowledgeScopeReferencesByIDs(
	ctx context.Context,
	knowledgeIDs []string,
) ([]*types.Knowledge, error) {
	if len(knowledgeIDs) == 0 {
		return []*types.Knowledge{}, nil
	}
	var knowledges []*types.Knowledge
	err := r.db.WithContext(ctx).
		Select("id", "tenant_id", "knowledge_base_id").
		Where("id IN ?", knowledgeIDs).
		Find(&knowledges).Error
	return knowledges, err
}

func (r *knowledgeScopeAuthorizationRepository) ListKnowledgeTagScopeReferencesByIDs(
	ctx context.Context,
	tagIDs []string,
) ([]*types.KnowledgeTag, error) {
	if len(tagIDs) == 0 {
		return []*types.KnowledgeTag{}, nil
	}
	var tags []*types.KnowledgeTag
	err := r.db.WithContext(ctx).
		Select("id", "tenant_id", "knowledge_base_id").
		Where("id IN ?", tagIDs).
		Find(&tags).Error
	return tags, err
}

func (r *knowledgeScopeAuthorizationRepository) ListKnowledgeFolderScopeReferencesByIDs(
	ctx context.Context,
	folderIDs []string,
) ([]*types.KnowledgeFolder, error) {
	if len(folderIDs) == 0 {
		return []*types.KnowledgeFolder{}, nil
	}
	var folders []*types.KnowledgeFolder
	err := r.db.WithContext(ctx).
		Select("id", "tenant_id", "knowledge_base_id").
		Where("id IN ?", folderIDs).
		Find(&folders).Error
	return folders, err
}
