package interfaces

import (
	"context"

	"github.com/Tencent/WeKnora/internal/types"
)

// MCPEndpointRepository persists workspace MCP endpoints.
type MCPEndpointRepository interface {
	Create(ctx context.Context, ep *types.MCPEndpoint) error
	GetByID(ctx context.Context, id string) (*types.MCPEndpoint, error)
	GetByTokenHash(ctx context.Context, tokenHash string) (*types.MCPEndpoint, error)
	List(ctx context.Context, owner types.MCPEndpointOwner) ([]*types.MCPEndpoint, error)
	Update(ctx context.Context, ep *types.MCPEndpoint) error
	Delete(ctx context.Context, tenantID uint64, id string) error
	TouchLastUsed(ctx context.Context, id string) error
	// GetSettings returns the workspace settings row, or (nil, nil) when the
	// workspace never saved one.
	GetSettings(ctx context.Context, tenantID uint64) (*types.MCPEndpointSettings, error)
	SaveSettings(ctx context.Context, settings *types.MCPEndpointSettings) error
}

// MCPEndpointUpdate carries the optional fields of an endpoint update. Nil
// means "leave unchanged" so a partial PUT does not reset a field.
type MCPEndpointUpdate struct {
	Name               *string
	Description        *string
	Enabled            *bool
	KnowledgeBaseIDs   *[]string
	Tools              *[]string
	DefaultAgentID     *string
	RateLimitPerMinute *int
}

// MCPEndpointService manages the lifecycle of workspace MCP endpoints and
// resolves bearer tokens for the MCP server surface.
type MCPEndpointService interface {
	// Create stores a new endpoint for owner and returns it together with the
	// one-time plaintext token.
	Create(ctx context.Context, owner types.MCPEndpointOwner, ep *types.MCPEndpoint) (*types.MCPEndpoint, string, error)
	Get(ctx context.Context, owner types.MCPEndpointOwner, id string) (*types.MCPEndpoint, error)
	List(ctx context.Context, owner types.MCPEndpointOwner) ([]*types.MCPEndpoint, error)
	Update(
		ctx context.Context, owner types.MCPEndpointOwner, id string, upd MCPEndpointUpdate,
	) (*types.MCPEndpoint, error)
	Delete(ctx context.Context, owner types.MCPEndpointOwner, id string) error
	// RotateToken replaces the bearer token and returns the new plaintext.
	RotateToken(ctx context.Context, owner types.MCPEndpointOwner, id string) (*types.MCPEndpoint, string, error)
	// Authenticate resolves a bearer token presented on the MCP surface to an
	// enabled endpoint whose ID matches. It returns ErrMCPEndpointNotFound or
	// ErrMCPEndpointDisabled style sentinels on failure.
	Authenticate(ctx context.Context, endpointID, token string) (*types.MCPEndpoint, error)
	// GetSettings returns the workspace settings, filling defaults when none
	// were saved.
	GetSettings(ctx context.Context, tenantID uint64) (*types.MCPEndpointSettings, error)
	UpdateSettings(
		ctx context.Context, tenantID uint64, personalRateLimitPerMinute int,
	) (*types.MCPEndpointSettings, error)
}
