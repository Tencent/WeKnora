// Package interfaces defines the interface contracts for custom agent management
package interfaces

import (
	"context"

	"github.com/Tencent/WeKnora/internal/types"
)

// CustomAgentService defines the custom agent service interface
// Provides high-level operations for agent creation, querying, updating, and deletion
type CustomAgentService interface {
	// CreateAgent creates a new custom agent
	// Parameters:
	//   - ctx: Context information, carrying request tracking, user identity, etc.
	//   - agent: Agent object containing basic information and configuration
	// Returns:
	//   - Created agent object (including automatically generated ID)
	//   - Possible errors such as insufficient permissions, validation errors, etc.
	CreateAgent(ctx context.Context, agent *types.CustomAgent) (*types.CustomAgent, error)

	// GetAgentByID retrieves agent information by ID (uses tenant from context)
	// Parameters:
	//   - ctx: Context information
	//   - id: Unique identifier of the agent
	// Returns:
	//   - Agent object, if found (including built-in agents)
	//   - Possible errors such as not existing, insufficient permissions, etc.
	GetAgentByID(ctx context.Context, id string) (*types.CustomAgent, error)

	// GetAgentByIDAndTenant retrieves agent by ID and tenant (for shared agents; skips built-in resolution)
	GetAgentByIDAndTenant(ctx context.Context, id string, tenantID uint64) (*types.CustomAgent, error)

	// ListAgents lists all agents under the current tenant (including built-in agents)
	// Parameters:
	//   - ctx: Context information, containing tenant information
	// Returns:
	//   - List of agent objects (built-in agents first, then custom agents sorted by creation time)
	//   - Possible errors such as insufficient permissions, etc.
	ListAgents(ctx context.Context) ([]*types.CustomAgent, error)

	// UpdateAgent updates agent information
	// Parameters:
	//   - ctx: Context information
	//   - agent: Agent object containing update information
	// Returns:
	//   - Updated agent object
	//   - Possible errors such as not existing, insufficient permissions, cannot modify built-in, etc.
	UpdateAgent(ctx context.Context, agent *types.CustomAgent) (*types.CustomAgent, error)

	// DeleteAgent deletes an agent
	// Parameters:
	//   - ctx: Context information
	//   - id: Unique identifier of the agent
	// Returns:
	//   - Possible errors such as not existing, insufficient permissions, cannot delete built-in, etc.
	DeleteAgent(ctx context.Context, id string) error

	// CopyAgent creates a copy of an existing agent
	// Parameters:
	//   - ctx: Context information
	//   - id: Unique identifier of the agent to copy
	// Returns:
	//   - The newly created agent copy
	//   - Possible errors such as not existing, insufficient permissions, etc.
	CopyAgent(ctx context.Context, id string) (*types.CustomAgent, error)
}

// CustomAgentRepository defines the custom agent repository interface
// Responsible for agent data persistence and retrieval
type CustomAgentRepository interface {
	// CreateAgent creates an agent record
	// Parameters:
	//   - ctx: Context information
	//   - agent: Agent object
	// Returns:
	//   - Possible errors such as database connection failure, unique constraint conflicts, etc.
	CreateAgent(ctx context.Context, agent *types.CustomAgent) error

	// GetAgentByID queries an agent by ID and tenant
	// Parameters:
	//   - ctx: Context information
	//   - id: Agent ID
	//   - tenantID: Tenant ID for isolation
	// Returns:
	//   - Agent object, if found
	//   - Possible errors such as record not existing, database errors, etc.
	GetAgentByID(ctx context.Context, id string, tenantID uint64) (*types.CustomAgent, error)

	// ListAgentsByTenantID lists all agents for a specific tenant
	// Parameters:
	//   - ctx: Context information
	//   - tenantID: Tenant ID
	// Returns:
	//   - List of agent objects
	//   - Possible errors such as database errors, etc.
	ListAgentsByTenantID(ctx context.Context, tenantID uint64) ([]*types.CustomAgent, error)

	// UpdateAgent updates an agent record
	// Parameters:
	//   - ctx: Context information
	//   - agent: Agent object containing update information
	// Returns:
	//   - Possible errors such as record not existing, database errors, etc.
	UpdateAgent(ctx context.Context, agent *types.CustomAgent) error

	// DeleteAgent deletes an agent record
	// Parameters:
	//   - ctx: Context information
	//   - id: Agent ID
	//   - tenantID: Tenant ID for isolation (required for composite primary key)
	// Returns:
	//   - Possible errors such as record not existing, database errors, etc.
	DeleteAgent(ctx context.Context, id string, tenantID uint64) error
}
