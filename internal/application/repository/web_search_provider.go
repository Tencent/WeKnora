package repository

import (
	"context"
	"errors"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"gorm.io/gorm"
)

// webSearchProviderRepository implements the WebSearchProviderRepository interface
type webSearchProviderRepository struct {
	db *gorm.DB
}

// NewWebSearchProviderRepository creates a new web search provider repository
func NewWebSearchProviderRepository(db *gorm.DB) interfaces.WebSearchProviderRepository {
	return &webSearchProviderRepository{db: db}
}

// Create creates a new web search provider
func (r *webSearchProviderRepository) Create(ctx context.Context, provider *types.WebSearchProviderEntity) error {
	return r.db.WithContext(ctx).Create(provider).Error
}

// GetByID retrieves a web search provider by ID within a tenant scope
func (r *webSearchProviderRepository) GetByID(ctx context.Context, tenantID uint64, id string) (*types.WebSearchProviderEntity, error) {
	var provider types.WebSearchProviderEntity
	if err := r.db.WithContext(ctx).Where(
		"id = ? AND tenant_id = ?", id, tenantID,
	).First(&provider).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &provider, nil
}

// GetDefault retrieves the default provider (is_default=true) for a tenant, or nil if none.
func (r *webSearchProviderRepository) GetDefault(ctx context.Context, tenantID uint64) (*types.WebSearchProviderEntity, error) {
	var provider types.WebSearchProviderEntity
	if err := r.db.WithContext(ctx).Where(
		"tenant_id = ? AND is_default = ?", tenantID, true,
	).First(&provider).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &provider, nil
}

// List lists all web search providers for a tenant
func (r *webSearchProviderRepository) List(ctx context.Context, tenantID uint64) ([]*types.WebSearchProviderEntity, error) {
	var providers []*types.WebSearchProviderEntity
	if err := r.db.WithContext(ctx).Where(
		"tenant_id = ?", tenantID,
	).Order("created_at ASC").Find(&providers).Error; err != nil {
		return nil, err
	}
	return providers, nil
}

// Update updates a web search provider
func (r *webSearchProviderRepository) Update(ctx context.Context, provider *types.WebSearchProviderEntity) error {
	return r.db.WithContext(ctx).Model(&types.WebSearchProviderEntity{}).Where(
		"id = ? AND tenant_id = ?", provider.ID, provider.TenantID,
	).Select("*").Updates(provider).Error
}

// Delete soft-deletes a web search provider
func (r *webSearchProviderRepository) Delete(ctx context.Context, tenantID uint64, id string) error {
	return r.db.WithContext(ctx).Where(
		"id = ? AND tenant_id = ?", id, tenantID,
	).Delete(&types.WebSearchProviderEntity{}).Error
}

// ClearDefault clears the default flag for all providers of a tenant, optionally excluding one
func (r *webSearchProviderRepository) ClearDefault(ctx context.Context, tenantID uint64, excludeID string) error {
	query := r.db.WithContext(ctx).Model(&types.WebSearchProviderEntity{}).Where(
		"tenant_id = ? AND is_default = ?", tenantID, true,
	)
	if excludeID != "" {
		query = query.Where("id != ?", excludeID)
	}
	return query.Update("is_default", false).Error
}
