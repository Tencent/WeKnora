package repository

import (
	"context"
	"errors"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// modelRepository implements the model repository interface
type modelRepository struct {
	db *gorm.DB
}

// NewModelRepository creates a new model repository
func NewModelRepository(db *gorm.DB) interfaces.ModelRepository {
	return &modelRepository{db: db}
}

// Create creates a new model
func (r *modelRepository) Create(ctx context.Context, m *types.Model) error {
	return r.db.WithContext(ctx).Create(m).Error
}

// GetByID retrieves a model by ID
func (r *modelRepository) GetByID(ctx context.Context, tenantID uint64, id string) (*types.Model, error) {
	var m types.Model
	if err := r.db.WithContext(ctx).Where("id = ?", id).Where(
		"(tenant_id = ? OR is_builtin = true)", tenantID,
	).First(&m).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &m, nil
}

// List lists models with optional filtering
func (r *modelRepository) List(
	ctx context.Context, tenantID uint64, modelType types.ModelType, source types.ModelSource,
) ([]*types.Model, error) {
	var models []*types.Model
	query := r.db.WithContext(ctx).Where(
		"(tenant_id = ? OR is_builtin = true)", tenantID,
	)

	if modelType != "" {
		query = query.Where("type = ?", modelType)
	}

	if err := query.Order("created_at ASC NULLS LAST").Order("id ASC").Find(&models).Error; err != nil {
		return nil, err
	}

	var preferences []types.ModelDefault
	if err := r.db.WithContext(ctx).Where("tenant_id = ?", tenantID).Find(&preferences).Error; err != nil {
		return nil, err
	}
	preferred := make(map[types.ModelType]string, len(preferences))
	for _, preference := range preferences {
		preferred[preference.Type] = preference.ModelID
	}
	// Historical choices are backfilled by the migration. Runtime selection
	// uses only the workspace preference, falling back to the first active model
	// when that preference is absent or unavailable.
	selected := make(map[types.ModelType]*types.Model)
	for _, model := range models {
		if model.Status != types.ModelStatusActive {
			continue
		}
		if selected[model.Type] == nil || preferred[model.Type] == model.ID {
			selected[model.Type] = model
		}
	}
	for _, model := range models {
		model.IsDefault = selected[model.Type] == model
	}
	// Resolve against all sources before filtering, so a source-specific list
	// cannot label another model as the workspace default.
	if source != "" {
		filtered := make([]*types.Model, 0, len(models))
		for _, model := range models {
			if model.Source == source {
				filtered = append(filtered, model)
			}
		}
		models = filtered
	}

	return models, nil
}

func (r *modelRepository) SetDefault(ctx context.Context, tenantID uint64, modelType types.ModelType, id string) error {
	preference := types.ModelDefault{TenantID: tenantID, Type: modelType, ModelID: id}
	return r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "tenant_id"}, {Name: "model_type"}},
		DoUpdates: clause.AssignmentColumns([]string{"model_id"}),
	}).Create(&preference).Error
}

// Update updates a model
func (r *modelRepository) Update(ctx context.Context, m *types.Model) error {
	// Use Select to explicitly update all fields, including zero values like false
	return r.db.WithContext(ctx).Debug().Model(&types.Model{}).Where(
		"id = ? AND tenant_id = ?", m.ID, m.TenantID,
	).Select("*").Updates(m).Error
}

// Delete deletes a model
func (r *modelRepository) Delete(ctx context.Context, tenantID uint64, id string) error {
	return r.db.WithContext(ctx).Where(
		"id = ? AND tenant_id = ?", id, tenantID,
	).Delete(&types.Model{}).Error
}

// ClearDefaultByType clears the default flag for all models of a specific type
// This is a batch operation that updates all matching records in one query
func (r *modelRepository) ClearDefaultByType(
	ctx context.Context,
	tenantID uint,
	modelType types.ModelType,
	excludeID string,
) error {
	query := r.db.WithContext(ctx).Model(&types.Model{}).Where(
		"tenant_id = ? AND type = ? AND is_default = ?", tenantID, modelType, true,
	)

	// If excludeID is provided, exclude that model from the update
	if excludeID != "" {
		query = query.Where("id != ?", excludeID)
	}

	// Batch update: set is_default to false for all matching records
	return query.Update("is_default", false).Error
}
