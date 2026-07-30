package interfaces

import (
	"context"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
)

// TenantService defines the tenant service interface
type TenantService interface {
	// CreateTenant creates a tenant
	CreateTenant(ctx context.Context, tenant *types.Tenant) (*types.Tenant, error)
	// GetTenantByID gets a tenant by ID
	GetTenantByID(ctx context.Context, id uint64) (*types.Tenant, error)
	// GetTenantsByIDs batches GetTenantByID for multiple IDs in a single
	// query. Returns a map keyed by tenant ID for O(1) lookup at the
	// call site; missing tenants are simply absent from the map.
	GetTenantsByIDs(ctx context.Context, ids []uint64) (map[uint64]*types.Tenant, error)
	// ListTenants lists all tenants
	ListTenants(ctx context.Context) ([]*types.Tenant, error)
	// UpdateTenant updates a tenant
	UpdateTenant(ctx context.Context, tenant *types.Tenant) (*types.Tenant, error)
	// DeleteTenant deletes a tenant
	DeleteTenant(ctx context.Context, id uint64) error
	// ListAllTenants lists all tenants (for users with cross-tenant access permission)
	ListAllTenants(ctx context.Context) ([]*types.Tenant, error)
	// BulkSetStorageQuota overwrites every tenant's storage_quota with
	// quotaBytes. Returns how many rows were affected. Used by the
	// SystemAdmin "apply default to all tenants" action; bypasses the
	// per-tenant whitelist on PUT /tenants/:id (which intentionally
	// forbids storage_quota edits for Owners). quotaBytes must be > 0;
	// callers are responsible for resolving GB→bytes.
	BulkSetStorageQuota(ctx context.Context, quotaBytes int64) (int64, error)
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
	// GetTenantsByIDs batches GetTenantByID; see TenantService.GetTenantsByIDs.
	GetTenantsByIDs(ctx context.Context, ids []uint64) (map[uint64]*types.Tenant, error)
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
	// BulkSetStorageQuota — see TenantService.BulkSetStorageQuota.
	BulkSetStorageQuota(ctx context.Context, quotaBytes int64) (int64, error)
}

type TenantAPIKeyCreateRequest struct {
	TenantID         uint64
	ScopeType        types.APIKeyScopeType
	Name             string
	FullAccess       bool
	KnowledgeBaseIDs []string
	Capabilities     []string
	ExpiresAt        *time.Time
}

type TenantAPIKeyCreateResult struct {
	APIKey *types.TenantAPIKey
	Token  string
}

type TenantAPIKeyRepository interface {
	CreateAPIKey(ctx context.Context, key *types.TenantAPIKey) error
	GetAPIKeyByHash(ctx context.Context, hash string) (*types.TenantAPIKey, error)
	ListAPIKeys(ctx context.Context, tenantID uint64) ([]*types.TenantAPIKey, error)
	ListPlatformAPIKeys(ctx context.Context) ([]*types.TenantAPIKey, error)
	RevokeAPIKey(ctx context.Context, tenantID uint64, id uint64) error
	RevokePlatformAPIKey(ctx context.Context, id uint64) error
	UpdateAPIKeyHash(ctx context.Context, id uint64, hash string) error
	UpdateAPIKeyLastUsed(ctx context.Context, id uint64, at time.Time) error
	// ListKeysWithPlaceholderHash returns keys whose key_hash is still the
	// migration placeholder (never authenticated since the 000065 upgrade),
	// so the real SHA-256 hash can be computed and backfilled.
	ListKeysWithPlaceholderHash(ctx context.Context) ([]*types.TenantAPIKey, error)
	// HasKeysWithPlaceholderHash is a cheap existence probe used at startup
	// to skip loading/decrypting api_key rows once backfill is complete.
	HasKeysWithPlaceholderHash(ctx context.Context) (bool, error)
}

type TenantAPIKeyService interface {
	CreateAPIKey(ctx context.Context, req TenantAPIKeyCreateRequest) (*TenantAPIKeyCreateResult, error)
	AuthenticateAPIKey(ctx context.Context, token string) (*types.TenantAPIKey, error)
	ListAPIKeys(ctx context.Context, tenantID uint64) ([]*types.TenantAPIKey, error)
	ListPlatformAPIKeys(ctx context.Context) ([]*types.TenantAPIKey, error)
	RevokeAPIKey(ctx context.Context, tenantID uint64, id uint64) error
	RevokePlatformAPIKey(ctx context.Context, id uint64) error
	// BackfillMissingKeyHashes computes and persists the SHA-256 key_hash
	// for legacy keys still carrying the migration placeholder.
	// Returns the number of keys backfilled.
	BackfillMissingKeyHashes(ctx context.Context) (int, error)
}
