package interfaces

import (
	"context"

	"github.com/Tencent/WeKnora/internal/types"
)

// MCPToolApprovalRepository persists per-tool approval requirements.
type MCPToolApprovalRepository interface {
	ListByService(ctx context.Context, tenantID uint64, serviceID string) ([]*types.MCPToolApproval, error)
	IsRequired(ctx context.Context, tenantID uint64, serviceID, toolName string) (bool, error)
	Upsert(ctx context.Context, row *types.MCPToolApproval) error
}

// MCPToolApprovalService is the business layer for MCP tool approval flags.
type MCPToolApprovalService interface {
	ListByService(ctx context.Context, tenantID uint64, serviceID string) ([]*types.MCPToolApproval, error)
	SetRequireApproval(ctx context.Context, tenantID uint64, serviceID, toolName string, require bool) error
	IsRequired(ctx context.Context, tenantID uint64, serviceID, toolName string) (bool, error)
}
