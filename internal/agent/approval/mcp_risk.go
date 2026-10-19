package approval

import (
	"context"

	"github.com/Tencent/WeKnora/internal/agent/policy"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
)

// RiskChecker reads and records the risk policy of MCP tools. A Checker that
// does not implement it leaves every MCP tool at RiskWrite.
type RiskChecker interface {
	// Policy returns the stored policy of a tool, or nil when it has none.
	Policy(ctx context.Context, tenantID uint64, serviceID, toolName string) (*types.MCPToolApproval, error)
	// ObserveRisk records a risk the service declared; the stored one only rises.
	ObserveRisk(ctx context.Context, tenantID uint64, serviceID, toolName string, risk string) error
}

// DeclaredRisk is the risk a service's annotations give its tool. Only
// readOnlyHint lowers it: MCP defaults destructiveHint to true, so reading
// it would make every unannotated tool destructive. Destructive is set by
// the workspace.
func DeclaredRisk(tool *types.MCPTool) policy.Risk {
	if tool.DeclaresReadOnly() {
		return policy.RiskRead
	}
	return policy.RiskWrite
}

// ToolRisk is the risk a stored policy and the service's declaration give a
// tool. The workspace's explicit risk wins; otherwise the declaration counts
// unless the service declared a higher risk before, so a service cannot
// lower its own tool's risk without the workspace agreeing.
func ToolRisk(row *types.MCPToolApproval, declared policy.Risk) policy.Risk {
	if row == nil {
		return declared
	}
	if explicit := policy.Risk(row.Risk); explicit.Valid() {
		return explicit
	}
	if observed := policy.Risk(row.ObservedRisk); observed.Above(declared) {
		return observed
	}
	return declared
}

// PendingDowngrade reports whether the service now declares a lower risk
// than the one in force, which only the workspace can accept.
func PendingDowngrade(row *types.MCPToolApproval, declared policy.Risk) bool {
	return row != nil && !policy.Risk(row.Risk).Valid() && policy.Risk(row.ObservedRisk).Above(declared)
}

// Action is the policy action of calling tool: its risk, and whether the
// workspace marked it as always requiring approval.
func (g *Gate) Action(ctx context.Context, tenantID uint64, serviceID string, tool *types.MCPTool) policy.Action {
	declared := DeclaredRisk(tool)
	if g == nil || g.checker == nil || tenantID == 0 || serviceID == "" || tool == nil || tool.Name == "" {
		return policy.Action{Risk: declared}
	}
	risks, ok := g.checker.(RiskChecker)
	if !ok {
		return policy.Action{Risk: policy.RiskWrite, Always: g.requiredByPolicy(ctx, tenantID, serviceID, tool.Name)}
	}
	row, err := risks.Policy(ctx, tenantID, serviceID, tool.Name)
	if err != nil {
		return g.failedAction(ctx, err)
	}
	if row == nil || declared.Above(policy.Risk(row.ObservedRisk)) {
		if err := risks.ObserveRisk(ctx, tenantID, serviceID, tool.Name, string(declared)); err != nil {
			logger.GetLogger(ctx).Warnf("record declared risk of MCP tool %s: %v", tool.Name, err)
		}
	}
	return policy.Action{Risk: ToolRisk(row, declared), Always: row != nil && row.RequireApproval}
}

// failedAction is the action used when the tool's policy cannot be read.
func (g *Gate) failedAction(ctx context.Context, err error) policy.Action {
	// Default fail-close: a transient DB error must NOT silently allow a
	// dangerous tool to run. Operators can opt into legacy behaviour via
	// WEKNORA_AGENT_TOOL_APPROVAL_FAIL_OPEN=true.
	if g.failClose {
		logger.GetLogger(ctx).Warnf("mcp tool approval check failed (fail-close: requiring approval): %v", err)
		return policy.Action{Risk: policy.RiskWrite, Always: true}
	}
	logger.GetLogger(ctx).Warnf("mcp tool approval check failed (fail-open: skip gate): %v", err)
	return policy.Action{Risk: policy.RiskWrite}
}

// ToolRisks returns the risk of each tool of a service in one policy read,
// without recording what the service declares.
func (g *Gate) ToolRisks(
	ctx context.Context, tenantID uint64, serviceID string, tools []*types.MCPTool,
) (map[string]policy.Risk, error) {
	rows := map[string]*types.MCPToolApproval{}
	if g != nil && g.checker != nil && tenantID != 0 && serviceID != "" {
		lister, ok := g.checker.(interface {
			ListByService(context.Context, uint64, string) ([]*types.MCPToolApproval, error)
		})
		if !ok {
			if adapter, isAdapter := g.checker.(*Adapter); isAdapter && adapter != nil {
				lister, ok = adapter.Svc.(interface {
					ListByService(context.Context, uint64, string) ([]*types.MCPToolApproval, error)
				})
			}
		}
		if ok {
			list, err := lister.ListByService(ctx, tenantID, serviceID)
			if err != nil {
				return nil, err
			}
			for _, row := range list {
				if row != nil && row.TenantID == tenantID && row.ServiceID == serviceID {
					rows[row.ToolName] = row
				}
			}
		}
	}
	risks := make(map[string]policy.Risk, len(tools))
	for _, tool := range tools {
		if tool != nil {
			risks[tool.Name] = ToolRisk(rows[tool.Name], DeclaredRisk(tool))
		}
	}
	return risks, nil
}
