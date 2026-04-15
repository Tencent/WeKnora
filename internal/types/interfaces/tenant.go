package interfaces

import (
	"context"

	"github.com/Tencent/WeKnora/internal/types"
)

// TenantService defines the tenant service interface
type TenantService interface {
	// CreateTenant creates a tenant
	CreateTenant(ctx context.Context, tenant *types.Tenant) (*types.Tenant, error)
	// GetTenantByID gets a tenant by ID
	GetTenantByID(ctx context.Context, id uint64) (*types.Tenant, error)
	// ListTenants lists all tenants
	ListTenants(ctx context.Context) ([]*types.Tenant, error)
	// UpdateTenant updates a tenant
	UpdateTenant(ctx context.Context, tenant *types.Tenant) (*types.Tenant, error)
	// DeleteTenant deletes a tenant
	DeleteTenant(ctx context.Context, id uint64) error
	// UpdateAPIKey updates the API key
	UpdateAPIKey(ctx context.Context, id uint64) (string, error)
	// ExtractTenantIDFromAPIKey extracts the tenant ID from the API key
	ExtractTenantIDFromAPIKey(apiKey string) (uint64, error)
	// ListAllTenants lists all tenants (for users with cross-tenant access permission)
	ListAllTenants(ctx context.Context) ([]*types.Tenant, error)
	// SearchTenants searches tenants with pagination and filters
	SearchTenants(ctx context.Context, keyword string, tenantID uint64, page, pageSize int) ([]*types.Tenant, int64, error)
	// GetTenantByIDForUser gets a tenant by ID with permission check
	GetTenantByIDForUser(ctx context.Context, tenantID uint64, userID string) (*types.Tenant, error)
	// GetWeKnoraCloudCredentials returns the decrypted WeKnoraCloud credentials for the current tenant.
	GetWeKnoraCloudCredentials(ctx context.Context) *types.WeKnoraCloudCredentials
}

// TenantRepository defines the tenant repository interface
type TenantRepository interface {
	// CreateTenant creates a tenant
	CreateTenant(ctx context.Context, tenant *types.Tenant) error
	// GetTenantByID gets a tenant by ID
	GetTenantByID(ctx context.Context, id uint64) (*types.Tenant, error)
	// ListTenants lists all tenants
	ListTenants(ctx context.Context) ([]*types.Tenant, error)
	// SearchTenants searches tenants with pagination and filters
	SearchTenants(ctx context.Context, keyword string, tenantID uint64, page, pageSize int) ([]*types.Tenant, int64, error)
	// UpdateTenant updates a tenant
	UpdateTenant(ctx context.Context, tenant *types.Tenant) error
	// DeleteTenant deletes a tenant
	DeleteTenant(ctx context.Context, id uint64) error
	// AdjustStorageUsed adjusts the storage used for a tenant
	AdjustStorageUsed(ctx context.Context, tenantID uint64, delta int64) error
}
