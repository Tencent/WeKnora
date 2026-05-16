package interfaces

import (
	"context"

	"github.com/Tencent/WeKnora/internal/types"
)

// UserResourceFavoriteRepository is the storage contract for per-user
// favorited resources. Implementations are expected to be tenant-aware
// (queries always include tenant_id) so a user switching tenants sees
// only that tenant's favorites — matches the PK shape in migration 000047.
type UserResourceFavoriteRepository interface {
	// List returns favorites for (userID, tenantID, resourceType), newest first.
	List(ctx context.Context, userID string, tenantID uint64, resourceType string) ([]*types.UserResourceFavorite, error)
	// Add upserts a favorite (no-op if already present). Returns whether
	// a new row was actually inserted so the service layer can decide
	// whether to emit an audit event.
	Add(ctx context.Context, userID string, tenantID uint64, resourceType, resourceID string) (created bool, err error)
	// Remove deletes a favorite; returns whether anything was deleted.
	Remove(ctx context.Context, userID string, tenantID uint64, resourceType, resourceID string) (removed bool, err error)
	// IsFavorite exists for the rare RPC where the caller wants a single
	// boolean rather than the full list (e.g. a future GET-by-id endpoint).
	IsFavorite(ctx context.Context, userID string, tenantID uint64, resourceType, resourceID string) (bool, error)
}

// UserResourceFavoriteService wraps the repository with input validation
// (resource type allowlist, non-empty id) so the handler stays thin.
type UserResourceFavoriteService interface {
	List(ctx context.Context, userID string, tenantID uint64, resourceType string) ([]*types.UserResourceFavorite, error)
	Add(ctx context.Context, userID string, tenantID uint64, resourceType, resourceID string) error
	Remove(ctx context.Context, userID string, tenantID uint64, resourceType, resourceID string) error
}
