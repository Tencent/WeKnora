package repository

import (
	"context"
	"errors"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"gorm.io/gorm"
)

type userResourceFavoriteRepository struct {
	db *gorm.DB
}

// NewUserResourceFavoriteRepository constructs the GORM-backed implementation.
func NewUserResourceFavoriteRepository(db *gorm.DB) interfaces.UserResourceFavoriteRepository {
	return &userResourceFavoriteRepository{db: db}
}

func (r *userResourceFavoriteRepository) List(
	ctx context.Context, userID string, tenantID uint64, resourceType string,
) ([]*types.UserResourceFavorite, error) {
	var list []*types.UserResourceFavorite
	err := r.db.WithContext(ctx).
		Where("user_id = ? AND tenant_id = ? AND resource_type = ?", userID, tenantID, resourceType).
		Order("created_at DESC").
		Find(&list).Error
	return list, err
}

// Add upserts the favorite. We rely on the composite primary key + GORM's
// FirstOrCreate to make the insertion idempotent — concurrent double-clicks
// from the same user collapse into one row with no error path needed.
// `created` reflects whether the row was newly inserted vs already present.
func (r *userResourceFavoriteRepository) Add(
	ctx context.Context, userID string, tenantID uint64, resourceType, resourceID string,
) (bool, error) {
	rec := &types.UserResourceFavorite{
		UserID:       userID,
		TenantID:     tenantID,
		ResourceType: resourceType,
		ResourceID:   resourceID,
	}
	res := r.db.WithContext(ctx).Where(rec).FirstOrCreate(rec)
	if res.Error != nil {
		return false, res.Error
	}
	return res.RowsAffected > 0, nil
}

func (r *userResourceFavoriteRepository) Remove(
	ctx context.Context, userID string, tenantID uint64, resourceType, resourceID string,
) (bool, error) {
	res := r.db.WithContext(ctx).
		Where("user_id = ? AND tenant_id = ? AND resource_type = ? AND resource_id = ?",
			userID, tenantID, resourceType, resourceID).
		Delete(&types.UserResourceFavorite{})
	if res.Error != nil {
		return false, res.Error
	}
	return res.RowsAffected > 0, nil
}

func (r *userResourceFavoriteRepository) IsFavorite(
	ctx context.Context, userID string, tenantID uint64, resourceType, resourceID string,
) (bool, error) {
	var rec types.UserResourceFavorite
	err := r.db.WithContext(ctx).
		Where("user_id = ? AND tenant_id = ? AND resource_type = ? AND resource_id = ?",
			userID, tenantID, resourceType, resourceID).
		First(&rec).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}
