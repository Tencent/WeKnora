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
	Upsert(ctx context.Context, row *types.MCPToolApproval) error
}

// MCPToolApprovalService is the business layer for MCP tool policies.
type MCPToolApprovalService interface {
	ListByService(ctx context.Context, tenantID uint64, serviceID string) ([]*types.MCPToolApproval, error)
	SetRequireApproval(ctx context.Context, tenantID uint64, serviceID, toolName string, require bool) error
	SetEnabled(ctx context.Context, tenantID uint64, serviceID, toolName string, enabled bool) error
	IsRequired(ctx context.Context, tenantID uint64, serviceID, toolName string) (bool, error)
	IsEnabled(ctx context.Context, tenantID uint64, serviceID, toolName string) (bool, error)
}
