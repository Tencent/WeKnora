package service

import (
	"context"
	"fmt"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

type mcpToolApprovalService struct {
	repo    interfaces.MCPToolApprovalRepository
	mcpRepo interfaces.MCPServiceRepository
}

// NewMCPToolApprovalService constructs the MCP tool approval service.
func NewMCPToolApprovalService(
	repo interfaces.MCPToolApprovalRepository,
	mcpRepo interfaces.MCPServiceRepository,
) interfaces.MCPToolApprovalService {
	return &mcpToolApprovalService{repo: repo, mcpRepo: mcpRepo}
}

func (s *mcpToolApprovalService) ListByService(ctx context.Context, tenantID uint64, serviceID string) ([]*types.MCPToolApproval, error) {
	svc, err := s.mcpRepo.GetByID(ctx, tenantID, serviceID)
	if err != nil {
		return nil, err
	}
	if svc == nil {
		return nil, fmt.Errorf("mcp service not found")
	}
	return s.repo.ListByService(ctx, tenantID, serviceID)
}

func (s *mcpToolApprovalService) SetRequireApproval(
	ctx context.Context, tenantID uint64, serviceID, toolName string, require bool,
) error {
	if toolName == "" {
		return fmt.Errorf("tool_name is required")
	}
	svc, err := s.mcpRepo.GetByID(ctx, tenantID, serviceID)
	if err != nil {
		return err
	}
	if svc == nil {
		return fmt.Errorf("mcp service not found")
	}
	row := &types.MCPToolApproval{
		TenantID:        tenantID,
		ServiceID:       serviceID,
		ToolName:        toolName,
		RequireApproval: require,
	}
	return s.repo.Upsert(ctx, row)
}

func (s *mcpToolApprovalService) IsRequired(ctx context.Context, tenantID uint64, serviceID, toolName string) (bool, error) {
	return s.repo.IsRequired(ctx, tenantID, serviceID, toolName)
}
