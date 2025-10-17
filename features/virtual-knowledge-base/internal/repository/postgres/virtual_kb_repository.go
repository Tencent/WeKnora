package postgres

import (
	"context"

	"github.com/tencent/weknora/features/virtualkb/internal/types"
	"gorm.io/gorm"
)

// VirtualKBRepository provides PostgreSQL persistence for virtual knowledge bases.
type VirtualKBRepository struct {
	db *gorm.DB
}

// NewVirtualKBRepository creates a new repository instance.
func NewVirtualKBRepository(db *gorm.DB) *VirtualKBRepository {
	return &VirtualKBRepository{db: db}
}

// Create inserts a new virtual knowledge base and its filters.
func (r *VirtualKBRepository) Create(ctx context.Context, vkb *types.VirtualKB) error {
	model, err := toVirtualKBModel(vkb)
	if err != nil {
		return err
	}

	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&model).Error; err != nil {
			return err
		}

		fromVirtualKBModel(model, vkb)
		return nil
	})
}

// Update modifies an existing virtual knowledge base and replaces its filters.
func (r *VirtualKBRepository) Update(ctx context.Context, vkb *types.VirtualKB) error {
	model, err := toVirtualKBModel(vkb)
	if err != nil {
		return err
	}

	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&virtualKBModel{}).
			Where("id = ?", vkb.ID).
			Updates(map[string]any{
				"name":        model.Name,
				"description": model.Description,
				"config":      model.Config,
			}).Error; err != nil {
			return err
		}

		if err := tx.Where("virtual_kb_id = ?", vkb.ID).
			Delete(&virtualKBFilterModel{}).
			Error; err != nil {
			return err
		}

		for _, filter := range model.Filters {
			filter.VirtualKBID = vkb.ID
			if err := tx.Create(&filter).Error; err != nil {
				return err
			}
		}

		return nil
	})
}

// Delete removes a virtual knowledge base and its filters.
func (r *VirtualKBRepository) Delete(ctx context.Context, id int64) error {
	return r.db.WithContext(ctx).
		Where("id = ?", id).
		Delete(&virtualKBModel{}).
		Error
}

// GetByID fetches a virtual knowledge base by ID.
func (r *VirtualKBRepository) GetByID(ctx context.Context, id int64) (*types.VirtualKB, error) {
	var model virtualKBModel
	if err := r.db.WithContext(ctx).
		Preload("Filters").
		First(&model, id).
		Error; err != nil {
		return nil, err
	}

	vkb := new(types.VirtualKB)
	if err := fromVirtualKBModel(model, vkb); err != nil {
		return nil, err
	}
	return vkb, nil
}

// List returns all virtual knowledge bases.
func (r *VirtualKBRepository) List(ctx context.Context) ([]*types.VirtualKB, error) {
	var models []virtualKBModel
	if err := r.db.WithContext(ctx).
		Preload("Filters").
		Order("id ASC").
		Find(&models).
		Error; err != nil {
		return nil, err
	}

	results := make([]*types.VirtualKB, 0, len(models))
	for _, model := range models {
		vkb := new(types.VirtualKB)
		if err := fromVirtualKBModel(model, vkb); err != nil {
			return nil, err
		}
		results = append(results, vkb)
	}

	return results, nil
}
