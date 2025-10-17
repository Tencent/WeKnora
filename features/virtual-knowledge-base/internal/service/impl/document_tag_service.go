package impl

import (
	"context"
	"errors"

	docRepo "github.com/tencent/weknora/features/virtualkb/internal/repository/interfaces"
	"github.com/tencent/weknora/features/virtualkb/internal/types"
)

// DocumentTagService provides document tagging business logic.
type DocumentTagService struct {
	repo docRepo.DocumentTagRepository
}

// NewDocumentTagService creates a new instance.
func NewDocumentTagService(repository docRepo.DocumentTagRepository) *DocumentTagService {
	return &DocumentTagService{repo: repository}
}

// AssignTag associates a tag with a document.
func (s *DocumentTagService) AssignTag(ctx context.Context, assignment *types.DocumentTag) error {
	if err := validateDocumentTag(assignment); err != nil {
		return err
	}
	return s.repo.AssignTag(ctx, assignment)
}

// UpdateTag updates metadata for a document-tag association.
func (s *DocumentTagService) UpdateTag(ctx context.Context, assignment *types.DocumentTag) error {
	if err := validateDocumentTag(assignment); err != nil {
		return err
	}
	return s.repo.UpdateTagAssignment(ctx, assignment)
}

// RemoveTag detaches a tag from a document.
func (s *DocumentTagService) RemoveTag(ctx context.Context, documentID string, tagID int64) error {
	if documentID == "" {
		return errors.New("document id is required")
	}
	if tagID == 0 {
		return errors.New("tag id is required")
	}
	return s.repo.RemoveTag(ctx, documentID, tagID)
}

// ListTags lists tags associated with a document.
func (s *DocumentTagService) ListTags(ctx context.Context, documentID string) ([]*types.Tag, error) {
	if documentID == "" {
		return nil, errors.New("document id is required")
	}
	return s.repo.ListTagsByDocument(ctx, documentID)
}

// ListDocuments lists documents associated with a tag.
func (s *DocumentTagService) ListDocuments(ctx context.Context, tagID int64) ([]*types.DocumentTag, error) {
	if tagID == 0 {
		return nil, errors.New("tag id is required")
	}
	return s.repo.ListDocumentsByTag(ctx, tagID)
}

func validateDocumentTag(assignment *types.DocumentTag) error {
	if assignment == nil {
		return errors.New("document tag assignment is required")
	}
	if assignment.DocumentID == "" {
		return errors.New("document id is required")
	}
	if assignment.TagID == 0 {
		return errors.New("tag id is required")
	}
	return nil
}
