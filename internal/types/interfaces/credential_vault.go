package interfaces

import (
	"context"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
)

// CredentialVaultRepository persists saved logins. Every method is scoped by
// tenant and user; no method can reach another user's rows.
type CredentialVaultRepository interface {
	Create(ctx context.Context, item *types.CredentialVaultItem) error
	Get(ctx context.Context, tenantID uint64, userID, id string) (*types.CredentialVaultItem, error)
	FindBySiteUsername(ctx context.Context, tenantID uint64, userID, site, username string) (*types.CredentialVaultItem, error)
	List(ctx context.Context, tenantID uint64, userID, site string) ([]*types.CredentialVaultItem, error)
	Count(ctx context.Context, tenantID uint64, userID string) (int64, error)
	Update(ctx context.Context, item *types.CredentialVaultItem) error
	Delete(ctx context.Context, tenantID uint64, userID, id string) (bool, error)
	Touch(ctx context.Context, tenantID uint64, userID, id string, at time.Time) error
}
