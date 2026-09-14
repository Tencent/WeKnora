package interfaces

import (
	"context"

	"github.com/Tencent/WeKnora/internal/types"
)

// WorkbenchAuthorizationRepository reads authorization state without caching.
type WorkbenchAuthorizationRepository interface {
	// GetAuthorization returns nil if the user, tenant or membership is missing
	// or soft-deleted. Inactive states are returned for the service to reject.
	GetAuthorization(ctx context.Context, tenantID uint64, userID string) (*types.WorkbenchAuthorization, error)
	// HasIMSession checks for an IM link to a non-deleted session in this tenant.
	HasIMSession(ctx context.Context, tenantID uint64, sessionID string) (bool, error)
}
