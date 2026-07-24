package interfaces

import (
	"context"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
)

// ResourceCleaner owns process-lifetime cleanup callbacks (database clients,
// worker pools, etc.). It is unrelated to persisted StoredResource records.
type ResourceCleaner interface {
	Register(cleanup types.CleanupFunc)
	RegisterWithName(name string, cleanup types.CleanupFunc)
	Cleanup(ctx context.Context) []error
}

// ResourceRepository persists stable resource identities, ownership bindings,
// and short-lived access grants.
type ResourceRepository interface {
	Create(ctx context.Context, resource *types.StoredResource) error
	GetByID(ctx context.Context, id string) (*types.StoredResource, error)
	GetByHandle(ctx context.Context, handle string) (*types.StoredResource, error)
	GetByTenantLocation(ctx context.Context, tenantID uint64, locationHash string) (*types.StoredResource, error)
	MarkDeleted(ctx context.Context, id string) error
	CreateBinding(ctx context.Context, binding *types.ResourceBinding) error
	CreateGrant(ctx context.Context, grant *types.ResourceAccessGrant) error
	GetValidGrant(ctx context.Context, tokenHash string, now time.Time) (*types.ResourceAccessGrant, error)
	DeleteExpiredGrants(ctx context.Context, before time.Time) error
}

// ResourceRegistration describes one physical object at registration time.
type ResourceRegistration struct {
	Kind         string
	MimeType     string
	OriginalName string
	Size         int64
	ContentHash  string
	Temporary    bool
}

// ResourceCatalog maps public resource references to internal storage
// locations and manages their access capabilities.
type ResourceCatalog interface {
	Register(ctx context.Context, tenantID uint64, physicalPath string, meta ResourceRegistration) (string, error)
	Resolve(ctx context.Context, reference string) (*types.StoredResource, error)
	ResolvePath(ctx context.Context, value string) (string, *types.StoredResource, error)
	Bind(ctx context.Context, reference, ownerType, ownerID, relation string) error
	MarkDeleted(ctx context.Context, reference string) error
	CreateAccessGrant(ctx context.Context, reference string, ttl time.Duration) (string, error)
	ResolveAccessGrant(ctx context.Context, token string) (*types.StoredResource, error)
}
