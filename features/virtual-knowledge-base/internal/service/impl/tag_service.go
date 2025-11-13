package impl

import (
	"context"
	"errors"
	"strings"

	repo "github.com/tencent/weknora/features/virtualkb/internal/repository/interfaces"
	"github.com/tencent/weknora/features/virtualkb/internal/types"
)

// TagService provides business logic for tag categories and tags.
type TagService struct {
	repo repo.TagRepository
}

// NewTagService creates a new TagService instance.
func NewTagService(repository repo.TagRepository) *TagService {
	return &TagService{repo: repository}
}

// CreateCategory creates a new tag category after validation.
func (s *TagService) CreateCategory(ctx context.Context, category *types.TagCategory) error {
	if err := validateCategory(category); err != nil {
		return err
	}
	return s.repo.CreateCategory(ctx, category)
}

// UpdateCategory updates an existing tag category.
func (s *TagService) UpdateCategory(ctx context.Context, category *types.TagCategory) error {
	if category.ID == 0 {
		return errors.New("category id is required")
	}
	if err := validateCategory(category); err != nil {
		return err
	}
	return s.repo.UpdateCategory(ctx, category)
}

// DeleteCategory removes a category.
func (s *TagService) DeleteCategory(ctx context.Context, id int64) error {
	if id == 0 {
		return errors.New("category id is required")
	}
	return s.repo.DeleteCategory(ctx, id)
}

// GetCategoryByID fetches a category by identifier.
func (s *TagService) GetCategoryByID(ctx context.Context, id int64) (*types.TagCategory, error) {
	if id == 0 {
		return nil, errors.New("category id is required")
	}
	return s.repo.GetCategoryByID(ctx, id)
}

// ListCategories returns all categories.
func (s *TagService) ListCategories(ctx context.Context) ([]*types.TagCategory, error) {
	return s.repo.ListCategories(ctx)
}

// CreateTag creates a new tag.
func (s *TagService) CreateTag(ctx context.Context, tag *types.Tag) error {
	if err := validateTag(tag); err != nil {
		return err
	}
	return s.repo.CreateTag(ctx, tag)
}

// UpdateTag updates tag information.
func (s *TagService) UpdateTag(ctx context.Context, tag *types.Tag) error {
	if tag.ID == 0 {
		return errors.New("tag id is required")
	}
	if err := validateTag(tag); err != nil {
		return err
	}
	return s.repo.UpdateTag(ctx, tag)
}

// DeleteTag removes a tag by identifier.
func (s *TagService) DeleteTag(ctx context.Context, id int64) error {
	if id == 0 {
		return errors.New("tag id is required")
	}
	return s.repo.DeleteTag(ctx, id)
}

// GetTagByID fetches a tag by identifier.
func (s *TagService) GetTagByID(ctx context.Context, id int64) (*types.Tag, error) {
	if id == 0 {
		return nil, errors.New("tag id is required")
	}
	return s.repo.GetTagByID(ctx, id)
}

// ListTagsByCategory lists tags for a category.
func (s *TagService) ListTagsByCategory(ctx context.Context, categoryID int64) ([]*types.Tag, error) {
	if categoryID == 0 {
		return nil, errors.New("category id is required")
	}
	return s.repo.ListTagsByCategory(ctx, categoryID)
}

func validateCategory(category *types.TagCategory) error {
	if category == nil {
		return errors.New("category is required")
	}
	if strings.TrimSpace(category.Name) == "" {
		return errors.New("category name is required")
	}
	return nil
}

func validateTag(tag *types.Tag) error {
	if tag == nil {
		return errors.New("tag is required")
	}
	if tag.CategoryID == 0 {
		return errors.New("tag category_id is required")
	}
	if strings.TrimSpace(tag.Name) == "" {
		return errors.New("tag name is required")
	}
	if strings.TrimSpace(tag.Value) == "" {
		return errors.New("tag value is required")
	}
	return nil
}
