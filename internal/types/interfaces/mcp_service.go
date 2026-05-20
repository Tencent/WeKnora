package interfaces

import (
	"context"

	"github.com/Tencent/WeKnora/internal/types"
)

// MCPServiceRepository defines the interface for MCP service data access
type MCPServiceRepository interface {
	// Create creates a new MCP service
	Create(ctx context.Context, service *types.MCPService) error

	// GetByID retrieves an MCP service by ID and tenant ID
	GetByID(ctx context.Context, tenantID uint64, id string) (*types.MCPService, error)

	// List retrieves all MCP services for a tenant
	List(ctx context.Context, tenantID uint64) ([]*types.MCPService, error)

	// ListEnabled retrieves all enabled MCP services for a tenant
	ListEnabled(ctx context.Context, tenantID uint64) ([]*types.MCPService, error)

	// ListByIDs retrieves MCP services by multiple IDs for a tenant
	ListByIDs(ctx context.Context, tenantID uint64, ids []string) ([]*types.MCPService, error)

	// Update updates an MCP service
	Update(ctx context.Context, service *types.MCPService) error

	// Delete deletes an MCP service (soft delete)
	Delete(ctx context.Context, tenantID uint64, id string) error
}

// MCPServiceService defines the interface for MCP service business logic
type MCPServiceService interface {
	// CreateMCPService creates a new MCP service
	CreateMCPService(ctx context.Context, service *types.MCPService) error

	// GetMCPServiceByID retrieves an MCP service by ID
	GetMCPServiceByID(ctx context.Context, tenantID uint64, id string) (*types.MCPService, error)

	// ListMCPServices lists all MCP services for a tenant
	ListMCPServices(ctx context.Context, tenantID uint64) ([]*types.MCPService, error)

	// ListMCPServicesByIDs retrieves multiple MCP services by IDs
	ListMCPServicesByIDs(ctx context.Context, tenantID uint64, ids []string) ([]*types.MCPService, error)

	// UpdateMCPService updates an MCP service
	UpdateMCPService(ctx context.Context, service *types.MCPService) error

	// DeleteMCPService deletes an MCP service
	DeleteMCPService(ctx context.Context, tenantID uint64, id string) error

	// TestMCPService tests the connection to an MCP service and returns available tools/resources
	TestMCPService(ctx context.Context, tenantID uint64, id string) (*types.MCPTestResult, error)

	// GetMCPServiceTools retrieves the list of tools from an MCP service
	GetMCPServiceTools(ctx context.Context, tenantID uint64, id string) ([]*types.MCPTool, error)

	// GetMCPServiceResources retrieves the list of resources from an MCP service
	GetMCPServiceResources(ctx context.Context, tenantID uint64, id string) ([]*types.MCPResource, error)

	// UpdateMCPCredentials writes one or more credential fields on the auth
	// config. Nil pointer means "do not touch this field". Returns the updated
	// service (with current AuthConfig) so the handler can derive the
	// configured/not-configured metadata for the response.
	//
	// Implementations MUST close any active MCP client connection for this
	// service so the next upstream call reconnects with the new credential.
	UpdateMCPCredentials(
		ctx context.Context, tenantID uint64, id string, apiKey *string, token *string,
	) (*types.MCPService, error)

	// ClearMCPCredential removes a single credential field. field must be
	// "api_key" or "token"; other values must be rejected by the caller.
	// Implementations MUST close any active MCP client connection for this
	// service. Clearing a field that is already empty is a no-op (no error).
	ClearMCPCredential(ctx context.Context, tenantID uint64, id, field string) error
}
