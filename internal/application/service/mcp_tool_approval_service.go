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
	enabled, err := s.repo.IsEnabled(ctx, tenantID, serviceID, toolName)
	if err != nil {
		return err
	}
	row := &types.MCPToolApproval{
		TenantID:        tenantID,
		ServiceID:       serviceID,
		ToolName:        toolName,
		RequireApproval: require,
		Enabled:         enabled,
	}
	return s.repo.Upsert(ctx, row)
}

func (s *mcpToolApprovalService) SetEnabled(
	ctx context.Context, tenantID uint64, serviceID, toolName string, enabled bool,
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
	// Preserve an existing approval setting when changing only enabled.
	requireApproval, err := s.repo.IsRequired(ctx, tenantID, serviceID, toolName)
	if err != nil {
		return err
	}
	return s.repo.Upsert(ctx, &types.MCPToolApproval{
		TenantID: tenantID, ServiceID: serviceID, ToolName: toolName,
		RequireApproval: requireApproval, Enabled: enabled,
	})
}

func (s *mcpToolApprovalService) IsRequired(ctx context.Context, tenantID uint64, serviceID, toolName string) (bool, error) {
	return s.repo.IsRequired(ctx, tenantID, serviceID, toolName)
}

func (s *mcpToolApprovalService) IsEnabled(ctx context.Context, tenantID uint64, serviceID, toolName string) (bool, error) {
	return s.repo.IsEnabled(ctx, tenantID, serviceID, toolName)
}
