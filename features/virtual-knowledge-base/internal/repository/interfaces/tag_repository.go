package interfaces

import (
	"context"

	"github.com/tencent/weknora/features/virtualkb/internal/types"
)

// TagRepository defines storage operations for tag categories and tags.
type TagRepository interface {
	CreateCategory(ctx context.Context, category *types.TagCategory) error
	UpdateCategory(ctx context.Context, category *types.TagCategory) error
	DeleteCategory(ctx context.Context, id int64) error
	GetCategoryByID(ctx context.Context, id int64) (*types.TagCategory, error)
	ListCategories(ctx context.Context) ([]*types.TagCategory, error)

	CreateTag(ctx context.Context, tag *types.Tag) error
	UpdateTag(ctx context.Context, tag *types.Tag) error
	DeleteTag(ctx context.Context, id int64) error
	GetTagByID(ctx context.Context, id int64) (*types.Tag, error)
	ListTagsByCategory(ctx context.Context, categoryID int64) ([]*types.Tag, error)
}
