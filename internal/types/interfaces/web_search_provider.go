package interfaces

import (
	"context"

	"github.com/Tencent/WeKnora/internal/types"
)

// WebSearchProviderRepository defines the repository interface for web search provider CRUD
type WebSearchProviderRepository interface {
	// Create creates a new web search provider
	Create(ctx context.Context, provider *types.WebSearchProviderEntity) error
	// GetByID retrieves a web search provider by ID within a tenant scope
	GetByID(ctx context.Context, tenantID uint64, id string) (*types.WebSearchProviderEntity, error)
	// GetDefault retrieves the default provider (is_default=true) for a tenant, or nil if none.
	GetDefault(ctx context.Context, tenantID uint64) (*types.WebSearchProviderEntity, error)
	// List lists all web search providers for a tenant
	List(ctx context.Context, tenantID uint64) ([]*types.WebSearchProviderEntity, error)
	// Update updates a web search provider
	Update(ctx context.Context, provider *types.WebSearchProviderEntity) error
	// Delete deletes a web search provider (soft delete)
	Delete(ctx context.Context, tenantID uint64, id string) error
	// ClearDefault clears the default flag for all providers of a tenant, optionally excluding one
	ClearDefault(ctx context.Context, tenantID uint64, excludeID string) error
}

// WebSearchProviderService defines the service interface for web search provider management.
// Tenant isolation is enforced by the handler layer (getOwned pattern).
// Service methods operate on entities whose TenantID is already verified.
type WebSearchProviderService interface {
	// CreateProvider creates a new web search provider.
	// provider.TenantID must be set by the caller (handler).
	CreateProvider(ctx context.Context, provider *types.WebSearchProviderEntity) error
	// UpdateProvider updates an existing provider.
	// provider.TenantID must be set by the caller (handler) for the repository WHERE clause.
	UpdateProvider(ctx context.Context, provider *types.WebSearchProviderEntity) error
	// DeleteProvider deletes a provider by tenant + id.
	DeleteProvider(ctx context.Context, tenantID uint64, id string) error

	// UpdateProviderCredentials writes one or more credential fields.
	// apiKey nil means "do not touch"; empty string is a no-op (clearing
	// goes through ClearProviderCredential). Returns the updated entity.
	UpdateProviderCredentials(
		ctx context.Context, tenantID uint64, id string, apiKey *string,
	) (*types.WebSearchProviderEntity, error)
	// ClearProviderCredential removes a single credential field. Currently
	// only "api_key" is recognized. Idempotent on already-empty fields.
	ClearProviderCredential(ctx context.Context, tenantID uint64, id, field string) error
}
