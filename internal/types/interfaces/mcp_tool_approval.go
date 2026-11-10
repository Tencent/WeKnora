package interfaces

import (
	"context"

	"github.com/Tencent/WeKnora/internal/types"
)

// MCPToolApprovalRepository persists per-tool MCP policies.
type MCPToolApprovalRepository interface {
	ListByService(ctx context.Context, tenantID uint64, serviceID string) ([]*types.MCPToolApproval, error)
	IsRequired(ctx context.Context, tenantID uint64, serviceID, toolName string) (bool, error)
	IsEnabled(ctx context.Context, tenantID uint64, serviceID, toolName string) (bool, error)
	// Get returns the stored policy of a tool, or nil when it has none.
	Get(ctx context.Context, tenantID uint64, serviceID, toolName string) (*types.MCPToolApproval, error)
	UpsertPolicy(ctx context.Context, tenantID uint64, serviceID, toolName string, patch types.MCPToolPolicyPatch) error
}

// MCPToolApprovalService is the business layer for MCP tool policies.
type MCPToolApprovalService interface {
	ListByService(ctx context.Context, tenantID uint64, serviceID string) ([]*types.MCPToolApproval, error)
	SetPolicy(ctx context.Context, tenantID uint64, serviceID, toolName string, patch types.MCPToolPolicyPatch) error
	SetRequireApproval(ctx context.Context, tenantID uint64, serviceID, toolName string, require bool) error
	SetEnabled(ctx context.Context, tenantID uint64, serviceID, toolName string, enabled bool) error
	IsRequired(ctx context.Context, tenantID uint64, serviceID, toolName string) (bool, error)
	IsEnabled(ctx context.Context, tenantID uint64, serviceID, toolName string) (bool, error)
	// Policy returns the stored policy of a tool, or nil when it has none.
	Policy(ctx context.Context, tenantID uint64, serviceID, toolName string) (*types.MCPToolApproval, error)
	// ObserveRisk records a risk the service declared for a tool; the
	// recorded risk only rises.
	ObserveRisk(ctx context.Context, tenantID uint64, serviceID, toolName string, risk string) error
}
