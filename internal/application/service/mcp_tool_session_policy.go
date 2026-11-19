package service

import (
	"context"
	"fmt"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

type mcpToolSessionPolicyService struct {
	repo interfaces.MCPToolSessionPolicyRepository
	mcp  interfaces.MCPServiceRepository
}

// NewMCPToolSessionPolicyService validates rules and MCP service ownership.
func NewMCPToolSessionPolicyService(
	repo interfaces.MCPToolSessionPolicyRepository, mcp interfaces.MCPServiceRepository,
) interfaces.MCPToolSessionPolicyService {
	return &mcpToolSessionPolicyService{repo: repo, mcp: mcp}
}

func (s *mcpToolSessionPolicyService) checkService(ctx context.Context, tenantID uint64, serviceID string) error {
	service, err := s.mcp.GetByID(ctx, tenantID, serviceID)
	if err != nil {
		return err
	}
	if service == nil || (!service.IsBuiltin && service.TenantID != tenantID) {
		return fmt.Errorf("MCP service not found")
	}
	return nil
}

func (s *mcpToolSessionPolicyService) ListByService(
	ctx context.Context, tenantID uint64, serviceID string,
) ([]*types.MCPToolSessionPolicy, error) {
	if err := s.checkService(ctx, tenantID, serviceID); err != nil {
		return nil, err
	}
	return s.repo.ListByService(ctx, tenantID, serviceID)
}

func (s *mcpToolSessionPolicyService) SetPolicy(
	ctx context.Context, tenantID uint64, serviceID, toolName string, rules []types.MCPToolSessionRule,
) error {
	if tenantID == 0 || toolName == "" || len(toolName) > 512 || rules == nil {
		return fmt.Errorf("%w: tenant, tool_name and allowed_sessions are required",
			types.ErrInvalidMCPToolSessionPolicy)
	}
	for _, rule := range rules {
		if err := rule.Validate(); err != nil {
			return err
		}
	}
	if err := s.checkService(ctx, tenantID, serviceID); err != nil {
		return err
	}
	return s.repo.Upsert(ctx, &types.MCPToolSessionPolicy{
		TenantID: tenantID, ServiceID: serviceID, ToolName: toolName, AllowedSessions: rules,
	})
}

func (s *mcpToolSessionPolicyService) DeletePolicy(
	ctx context.Context, tenantID uint64, serviceID, toolName string,
) error {
	if err := s.checkService(ctx, tenantID, serviceID); err != nil {
		return err
	}
	return s.repo.Delete(ctx, tenantID, serviceID, toolName)
}
