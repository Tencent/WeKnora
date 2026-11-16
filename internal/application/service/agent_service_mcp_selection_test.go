package service

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/agent/tools"
	"github.com/Tencent/WeKnora/internal/mcp"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

// recordingMCPService pins how many times each list path is reached. It
// embeds the full interface and overrides only the methods registerMCPTools
// consults, so an unset mode that must not load services fails loudly if it
// reaches any of them.
type recordingMCPService struct {
	interfaces.MCPServiceService
	listAllCalls int
	listByIDs    []string
}

func (s *recordingMCPService) ListMCPServices(_ context.Context, _ uint64) ([]*types.MCPService, error) {
	s.listAllCalls++
	return nil, nil
}

func (s *recordingMCPService) ListMCPServicesByIDs(
	_ context.Context, _ uint64, ids []string,
) ([]*types.MCPService, error) {
	s.listByIDs = append([]string(nil), ids...)
	return nil, nil
}

func TestRegisterMCPToolsUnsetModeLoadsNoServices(t *testing.T) {
	ctx := types.WithExecutionTenant(context.Background(), 7)
	rec := &recordingMCPService{}
	svc := &agentService{
		mcpServiceService: rec,
		mcpManager:        &mcp.MCPManager{},
	}

	cfg := &types.AgentConfig{} // MCPSelectionMode left unset
	svc.registerMCPTools(ctx, tools.NewToolRegistry(), cfg)

	require.Equal(t, 0, rec.listAllCalls,
		"an unset MCP mode must not enumerate every service in the tenant")
	require.Empty(t, rec.listByIDs,
		"an unset MCP mode must not resolve a selected list")
	require.Empty(t, cfg.MCPServices,
		"an unset MCP mode must freeze to no services, not to every service")
	require.NotEqual(t, "all", cfg.MCPSelectionMode,
		"an unset MCP mode must not resolve to the all-services scope")
}
