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

func (s *mcpToolApprovalService) SetPolicy(
	ctx context.Context, tenantID uint64, serviceID, toolName string, requireApproval, enabled *bool,
) error {
	if toolName == "" {
		return fmt.Errorf("tool_name is required")
	}
	if requireApproval == nil && enabled == nil {
		return fmt.Errorf("require_approval or enabled is required")
	}
	svc, err := s.mcpRepo.GetByID(ctx, tenantID, serviceID)
	if err != nil {
		return err
	}
	if svc == nil {
		return fmt.Errorf("mcp service not found")
	}
	return s.repo.UpsertPolicy(ctx, tenantID, serviceID, toolName, types.MCPToolPolicyPatch{
		RequireApproval: requireApproval,
		Enabled:         enabled,
	})
}

func (s *mcpToolApprovalService) SetRequireApproval(
	ctx context.Context, tenantID uint64, serviceID, toolName string, require bool,
) error {
	return s.SetPolicy(ctx, tenantID, serviceID, toolName, &require, nil)
}

func (s *mcpToolApprovalService) SetEnabled(
	ctx context.Context, tenantID uint64, serviceID, toolName string, enabled bool,
) error {
	return s.SetPolicy(ctx, tenantID, serviceID, toolName, nil, &enabled)
}

func (s *mcpToolApprovalService) IsRequired(ctx context.Context, tenantID uint64, serviceID, toolName string) (bool, error) {
	return s.repo.IsRequired(ctx, tenantID, serviceID, toolName)
}

func (s *mcpToolApprovalService) IsEnabled(ctx context.Context, tenantID uint64, serviceID, toolName string) (bool, error) {
	return s.repo.IsEnabled(ctx, tenantID, serviceID, toolName)
}
