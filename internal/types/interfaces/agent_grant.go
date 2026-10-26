package interfaces

import (
	"context"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
)

// AgentGrantRepository persists remembered agent approvals.
type AgentGrantRepository interface {
	// FindGrant returns the grant for the key in the scope, revoked or not,
	// or nil when there is none.
	FindGrant(ctx context.Context, tenantID uint64, userID string, scope types.GrantScope, key string) (
		*types.AgentGrant, error)
	// SaveGrant creates the grant or renews a revoked or outdated one.
	SaveGrant(ctx context.Context, grant *types.AgentGrant) error
	// TouchGrant records that the grant allowed a call at at.
	TouchGrant(ctx context.Context, id string, at time.Time) error
	// ListGrants returns the user's grants in a scope, newest first.
	ListGrants(ctx context.Context, tenantID uint64, userID, scope, scopeID string) ([]*types.AgentGrant, error)
	// RevokeGrant revokes one of the user's grants; it reports whether one was.
	RevokeGrant(ctx context.Context, tenantID uint64, userID, id string, at time.Time) (bool, error)
	// DeleteScopes drops every grant of the given scopes.
	DeleteScopes(ctx context.Context, tenantID uint64, scope string, scopeIDs []string) error
}
