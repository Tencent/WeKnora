package repository

import (
	"context"
	"errors"

	"github.com/Tencent/WeKnora/internal/types"
	"gorm.io/gorm"
)

// ErrCatalogVersionConflict reports a publish based on a stale version.
var ErrCatalogVersionConflict = errors.New("model catalog changed; reload and preview again")

// ModelCatalogRepository stores the singleton console overlay row.
type ModelCatalogRepository struct{ db *gorm.DB }

// NewModelCatalogRepository creates a repository on db.
func NewModelCatalogRepository(db *gorm.DB) *ModelCatalogRepository {
	return &ModelCatalogRepository{db: db}
}

// Get loads the overlay row, including its bounded history.
func (r *ModelCatalogRepository) Get(ctx context.Context) (*types.ModelCatalogConfig, error) {
	var row types.ModelCatalogConfig
	err := r.db.WithContext(ctx).First(&row, 1).Error
	return &row, err
}

// Save replaces the row only if it is still at the expected version.
func (r *ModelCatalogRepository) Save(ctx context.Context, expected uint64, row *types.ModelCatalogConfig) error {
	result := r.db.WithContext(ctx).Model(&types.ModelCatalogConfig{}).
		Where("id = ? AND version = ?", 1, expected).
		Updates(map[string]any{
			"version": row.Version, "overlay": row.Overlay,
			"history": row.History, "updated_by": row.UpdatedBy, "updated_at": row.UpdatedAt,
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrCatalogVersionConflict
	}
	return nil
}

// Version avoids transferring the bounded configuration history on every poll.
func (r *ModelCatalogRepository) Version(ctx context.Context) (uint64, error) {
	var row types.ModelCatalogConfig
	err := r.db.WithContext(ctx).Select("version").First(&row, 1).Error
	return row.Version, err
}
