package repository

import (
	"context"
	"errors"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
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
	if provider.IsDefault {
		return r.withDefaultTransition(ctx, provider.TenantID, "", func(tx *gorm.DB) error {
			return tx.Create(provider).Error
		})
	}
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
	update := func(db *gorm.DB) error {
		result := db.Model(&types.WebSearchProviderEntity{}).Where(
			"id = ? AND tenant_id = ?", provider.ID, provider.TenantID,
		).Select("*").Updates(provider)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return gorm.ErrRecordNotFound
		}
		return nil
	}
	if provider.IsDefault {
		return r.withDefaultTransition(ctx, provider.TenantID, provider.ID, update)
	}
	return update(r.db.WithContext(ctx))
}

// Delete soft-deletes a web search provider
func (r *webSearchProviderRepository) Delete(ctx context.Context, tenantID uint64, id string) error {
	return r.db.WithContext(ctx).Where(
		"id = ? AND tenant_id = ?", id, tenantID,
	).Delete(&types.WebSearchProviderEntity{}).Error
}

func clearDefault(tx *gorm.DB, tenantID uint64, excludeID string) error {
	query := tx.Model(&types.WebSearchProviderEntity{}).Where(
		"tenant_id = ? AND is_default = ?", tenantID, true,
	)
	if excludeID != "" {
		query = query.Where("id != ?", excludeID)
	}
	return query.Update("is_default", false).Error
}

// withDefaultTransition serializes changes to a tenant's default provider and
// keeps clearing the previous default in the same transaction as the new write.
func (r *webSearchProviderRepository) withDefaultTransition(
	ctx context.Context,
	tenantID uint64,
	excludeID string,
	write func(*gorm.DB) error,
) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := lockDefaultProviderScope(tx, tenantID); err != nil {
			return err
		}
		if err := clearDefault(tx, tenantID, excludeID); err != nil {
			return err
		}
		return write(tx)
	})
}

// lockDefaultProviderScope uses the tenant row as a stable per-tenant lock,
// including when the tenant does not have any providers yet. SQLite has no row
// locks, so a no-op write acquires its database writer lock before the read/write
// transition starts.
func lockDefaultProviderScope(tx *gorm.DB, tenantID uint64) error {
	if tx.Name() == "sqlite" {
		result := tx.Model(&types.Tenant{}).Where("id = ?", tenantID).UpdateColumn("id", tenantID)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return gorm.ErrRecordNotFound
		}
		return nil
	}

	var tenant types.Tenant
	return tx.Clauses(clause.Locking{Strength: "UPDATE"}).Select("id").Where("id = ?", tenantID).First(&tenant).Error
}

// PatchSettings persists display/default changes without writing connection parameters.
func (r *webSearchProviderRepository) PatchSettings(
	ctx context.Context, provider *types.WebSearchProviderEntity, fields []string,
) error {
	if provider == nil || provider.ID == "" || provider.TenantID == 0 || len(fields) == 0 {
		return errors.New("provider identity and settings are required")
	}
	values := map[string]any{}
	for _, field := range fields {
		switch field {
		case "name":
			values[field] = provider.Name
		case "description":
			values[field] = provider.Description
		case "is_default":
			values[field] = provider.IsDefault
		default:
			return errors.New("unsupported provider setting")
		}
	}
	update := func(tx *gorm.DB) error {
		result := tx.Model(&types.WebSearchProviderEntity{}).
			Where("id = ? AND tenant_id = ?", provider.ID, provider.TenantID).Updates(values)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return gorm.ErrRecordNotFound
		}
		return nil
	}
	if values["is_default"] == true {
		return r.withDefaultTransition(ctx, provider.TenantID, provider.ID, update)
	}
	return r.db.WithContext(ctx).Transaction(update)
}
