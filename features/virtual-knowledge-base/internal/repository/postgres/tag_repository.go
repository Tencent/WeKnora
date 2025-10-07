package postgres

import (
	"context"

	"github.com/tencent/weknora/features/virtualkb/internal/types"
	"gorm.io/gorm"
)

// TagRepository provides PostgreSQL-backed storage for tag categories and tags.
type TagRepository struct {
	db *gorm.DB
}

// NewTagRepository instantiates a tag repository.
func NewTagRepository(db *gorm.DB) *TagRepository {
	return &TagRepository{db: db}
}

// CreateCategory persists a new tag category.
func (r *TagRepository) CreateCategory(ctx context.Context, category *types.TagCategory) error {
	model := toTagCategoryModel(category)
	if err := r.db.WithContext(ctx).Create(&model).Error; err != nil {
		return err
	}
	fromTagCategoryModel(model, category)
	return nil
}

// UpdateCategory updates an existing tag category.
func (r *TagRepository) UpdateCategory(ctx context.Context, category *types.TagCategory) error {
	model := toTagCategoryModel(category)
	return r.db.WithContext(ctx).Model(&tagCategoryModel{}).
		Where("id = ?", category.ID).
		Updates(model).
		Error
}

// DeleteCategory removes a tag category by ID.
func (r *TagRepository) DeleteCategory(ctx context.Context, id int64) error {
	return r.db.WithContext(ctx).
		Where("id = ?", id).
		Delete(&tagCategoryModel{}).
		Error
}

// GetCategoryByID fetches a tag category by identifier.
func (r *TagRepository) GetCategoryByID(ctx context.Context, id int64) (*types.TagCategory, error) {
	var model tagCategoryModel
	if err := r.db.WithContext(ctx).
		First(&model, id).
		Error; err != nil {
		return nil, err
	}
	category := new(types.TagCategory)
	fromTagCategoryModel(model, category)
	return category, nil
}

// ListCategories retrieves all tag categories.
func (r *TagRepository) ListCategories(ctx context.Context) ([]*types.TagCategory, error) {
	var models []tagCategoryModel
	if err := r.db.WithContext(ctx).
		Order("id ASC").
		Find(&models).
		Error; err != nil {
		return nil, err
	}
	categories := make([]*types.TagCategory, 0, len(models))
	for _, m := range models {
		category := new(types.TagCategory)
		fromTagCategoryModel(m, category)
		categories = append(categories, category)
	}
	return categories, nil
}

// CreateTag persists a new tag.
func (r *TagRepository) CreateTag(ctx context.Context, tag *types.Tag) error {
	model := toTagModel(tag)
	if err := r.db.WithContext(ctx).Create(&model).Error; err != nil {
		return err
	}
	fromTagModel(model, tag)
	return nil
}

// UpdateTag updates an existing tag.
func (r *TagRepository) UpdateTag(ctx context.Context, tag *types.Tag) error {
	model := toTagModel(tag)
	return r.db.WithContext(ctx).
		Model(&tagModel{}).
		Where("id = ?", tag.ID).
		Updates(model).
		Error
}

// DeleteTag removes a tag by ID.
func (r *TagRepository) DeleteTag(ctx context.Context, id int64) error {
	return r.db.WithContext(ctx).
		Where("id = ?", id).
		Delete(&tagModel{}).
		Error
}

// GetTagByID fetches a tag using its identifier.
func (r *TagRepository) GetTagByID(ctx context.Context, id int64) (*types.Tag, error) {
	var model tagModel
	if err := r.db.WithContext(ctx).
		First(&model, id).
		Error; err != nil {
		return nil, err
	}
	tag := new(types.Tag)
	fromTagModel(model, tag)
	return tag, nil
}

// ListTagsByCategory returns all tags for a category.
func (r *TagRepository) ListTagsByCategory(ctx context.Context, categoryID int64) ([]*types.Tag, error) {
	var models []tagModel
	if err := r.db.WithContext(ctx).
		Where("category_id = ?", categoryID).
		Order("id ASC").
		Find(&models).
		Error; err != nil {
		return nil, err
	}
	tags := make([]*types.Tag, 0, len(models))
	for _, m := range models {
		tag := new(types.Tag)
		fromTagModel(m, tag)
		tags = append(tags, tag)
	}
	return tags, nil
}
