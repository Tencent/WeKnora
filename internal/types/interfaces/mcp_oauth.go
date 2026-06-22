package interfaces

import (
	"context"

	"github.com/Tencent/WeKnora/internal/types"
)

// MCPOAuthRepository persists OAuth clients (per service) and tokens
// (per user + service) for the MCP OAuth2 authorization-code flow.
type MCPOAuthRepository interface {
	// GetClient returns the registered OAuth client for a service, or
	// (nil, nil) when none has been registered yet.
	GetClient(ctx context.Context, tenantID uint64, serviceID string) (*types.MCPOAuthClient, error)

	// SaveClient creates or updates the registered OAuth client for a service.
	SaveClient(ctx context.Context, client *types.MCPOAuthClient) error

	// DeleteClient removes the registered OAuth client for a service.
	DeleteClient(ctx context.Context, tenantID uint64, serviceID string) error

	// GetToken returns the stored token for (tenant, user, service), or
	// (nil, nil) when the user has not authorized yet.
	GetToken(ctx context.Context, tenantID uint64, userID, serviceID string) (*types.MCPOAuthToken, error)

	// SaveToken creates or updates the per-user token for a service.
	SaveToken(ctx context.Context, token *types.MCPOAuthToken) error

	// DeleteToken removes the per-user token for a service (revoke /
	// re-authorize).
	DeleteToken(ctx context.Context, tenantID uint64, userID, serviceID string) error
}
