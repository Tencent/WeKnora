package service

import (
	"context"
	"fmt"

	"github.com/Tencent/WeKnora/internal/agent/policy"
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
	ctx context.Context, tenantID uint64, serviceID, toolName string, patch types.MCPToolPolicyPatch,
) error {
	if toolName == "" {
		return fmt.Errorf("tool_name is required")
	}
	// The observed risk is the service's own declaration, never an edit.
	patch.ObservedRisk = nil
	if patch.RequireApproval == nil && patch.Enabled == nil && patch.Risk == nil {
		return fmt.Errorf("require_approval, enabled or risk is required")
	}
	if patch.Risk != nil && *patch.Risk != "" && !policy.Risk(*patch.Risk).Valid() {
		return fmt.Errorf("%w: unknown risk %q", types.ErrInvalidMCPToolPolicy, *patch.Risk)
	}
	svc, err := s.mcpRepo.GetByID(ctx, tenantID, serviceID)
	if err != nil {
		return err
	}
	if svc == nil {
		return fmt.Errorf("mcp service not found")
	}
	return s.repo.UpsertPolicy(ctx, tenantID, serviceID, toolName, patch)
}

func (s *mcpToolApprovalService) SetRequireApproval(
	ctx context.Context, tenantID uint64, serviceID, toolName string, require bool,
) error {
	return s.SetPolicy(ctx, tenantID, serviceID, toolName, types.MCPToolPolicyPatch{RequireApproval: &require})
}

func (s *mcpToolApprovalService) SetEnabled(
	ctx context.Context, tenantID uint64, serviceID, toolName string, enabled bool,
) error {
	return s.SetPolicy(ctx, tenantID, serviceID, toolName, types.MCPToolPolicyPatch{Enabled: &enabled})
}

func (s *mcpToolApprovalService) IsRequired(ctx context.Context, tenantID uint64, serviceID, toolName string) (bool, error) {
	return s.repo.IsRequired(ctx, tenantID, serviceID, toolName)
}

func (s *mcpToolApprovalService) IsEnabled(ctx context.Context, tenantID uint64, serviceID, toolName string) (bool, error) {
	return s.repo.IsEnabled(ctx, tenantID, serviceID, toolName)
}

func (s *mcpToolApprovalService) Policy(
	ctx context.Context, tenantID uint64, serviceID, toolName string,
) (*types.MCPToolApproval, error) {
	return s.repo.Get(ctx, tenantID, serviceID, toolName)
}

// ObserveRisk records a risk the service declared when it is above the one
// recorded so far; a lower declaration is ignored.
func (s *mcpToolApprovalService) ObserveRisk(
	ctx context.Context, tenantID uint64, serviceID, toolName string, risk string,
) error {
	if !policy.Risk(risk).Valid() {
		return fmt.Errorf("%w: unknown risk %q", types.ErrInvalidMCPToolPolicy, risk)
	}
	row, err := s.repo.Get(ctx, tenantID, serviceID, toolName)
	if err != nil {
		return err
	}
	if row != nil && !policy.Risk(risk).Above(policy.Risk(row.ObservedRisk)) {
		return nil
	}
	return s.repo.UpsertPolicy(ctx, tenantID, serviceID, toolName, types.MCPToolPolicyPatch{ObservedRisk: &risk})
}
