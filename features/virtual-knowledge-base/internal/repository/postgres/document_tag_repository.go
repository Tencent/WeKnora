package postgres

import (
	"context"

	"github.com/tencent/weknora/features/virtualkb/internal/types"
	"gorm.io/gorm"
)

// DocumentTagRepository provides PostgreSQL-backed document tag storage.
type DocumentTagRepository struct {
	db *gorm.DB
}

// NewDocumentTagRepository creates a new instance.
func NewDocumentTagRepository(db *gorm.DB) *DocumentTagRepository {
	return &DocumentTagRepository{db: db}
}

// AssignTag attaches a tag to a document.
func (r *DocumentTagRepository) AssignTag(ctx context.Context, documentTag *types.DocumentTag) error {
	model := toDocumentTagModel(documentTag)
	if err := r.db.WithContext(ctx).Create(&model).Error; err != nil {
		return err
	}
	fromDocumentTagModel(model, documentTag)
	return nil
}

// UpdateTagAssignment updates tag metadata for a document.
func (r *DocumentTagRepository) UpdateTagAssignment(ctx context.Context, documentTag *types.DocumentTag) error {
	model := toDocumentTagModel(documentTag)
	return r.db.WithContext(ctx).
		Model(&documentTagModel{}).
		Where("document_id = ? AND tag_id = ?", documentTag.DocumentID, documentTag.TagID).
		Updates(model).
		Error
}

// RemoveTag detaches a tag from a document.
func (r *DocumentTagRepository) RemoveTag(ctx context.Context, documentID string, tagID int64) error {
	return r.db.WithContext(ctx).
		Where("document_id = ? AND tag_id = ?", documentID, tagID).
		Delete(&documentTagModel{}).
		Error
}

// ListTagsByDocument returns tags assigned to a document.
func (r *DocumentTagRepository) ListTagsByDocument(ctx context.Context, documentID string) ([]*types.Tag, error) {
	var assignments []documentTagModel
	if err := r.db.WithContext(ctx).
		Preload("Tag").
		Where("document_id = ?", documentID).
		Find(&assignments).
		Error; err != nil {
		return nil, err
	}

	tags := make([]*types.Tag, 0, len(assignments))
	for _, assignment := range assignments {
		if assignment.Tag == nil {
			continue
		}
		tag := new(types.Tag)
		fromTagModel(*assignment.Tag, tag)
		tags = append(tags, tag)
	}

	return tags, nil
}

// ListDocumentsByTag fetches document-tag associations for a tag.
func (r *DocumentTagRepository) ListDocumentsByTag(ctx context.Context, tagID int64) ([]*types.DocumentTag, error) {
	var assignments []documentTagModel
	if err := r.db.WithContext(ctx).
		Where("tag_id = ?", tagID).
		Find(&assignments).
		Error; err != nil {
		return nil, err
	}

	results := make([]*types.DocumentTag, 0, len(assignments))
	for _, assignment := range assignments {
		docTag := new(types.DocumentTag)
		fromDocumentTagModel(assignment, docTag)
		results = append(results, docTag)
	}

	return results, nil
}
