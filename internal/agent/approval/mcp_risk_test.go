package approval

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/agent/policy"
	"github.com/Tencent/WeKnora/internal/types"
)

type riskChecker struct {
	stubChecker
	row      *types.MCPToolApproval
	observed []string
}

func (c *riskChecker) Policy(context.Context, uint64, string, string) (*types.MCPToolApproval, error) {
	return c.row, c.err
}

func (c *riskChecker) ObserveRisk(_ context.Context, _ uint64, _, _ string, risk string) error {
	c.observed = append(c.observed, risk)
	return nil
}

func readOnlyTool(readOnly bool) *types.MCPTool {
	return &types.MCPTool{Name: "t", Annotations: &types.MCPToolAnnotations{ReadOnlyHint: &readOnly}}
}

func TestDeclaredRiskReadsOnlyTheReadOnlyHint(t *testing.T) {
	destructive := true
	require.Equal(t, policy.RiskRead, DeclaredRisk(readOnlyTool(true)))
	require.Equal(t, policy.RiskWrite, DeclaredRisk(readOnlyTool(false)))
	require.Equal(t, policy.RiskWrite, DeclaredRisk(&types.MCPTool{Name: "t"}))
	require.Equal(t, policy.RiskWrite, DeclaredRisk(&types.MCPTool{
		Name: "t", Annotations: &types.MCPToolAnnotations{DestructiveHint: &destructive},
	}), "destructive is the workspace's call, not the service's")
}

func TestToolRisk(t *testing.T) {
	require.Equal(t, policy.RiskRead, ToolRisk(nil, policy.RiskRead))
	raised := &types.MCPToolApproval{ObservedRisk: "write"}
	require.Equal(t, policy.RiskWrite, ToolRisk(raised, policy.RiskRead), "a service cannot lower its own risk")
	require.True(t, PendingDowngrade(raised, policy.RiskRead))
	require.Equal(t, policy.RiskWrite, ToolRisk(&types.MCPToolApproval{ObservedRisk: "read"}, policy.RiskWrite),
		"a raised declaration counts at once")
	explicit := &types.MCPToolApproval{Risk: "read", ObservedRisk: "write"}
	require.Equal(t, policy.RiskRead, ToolRisk(explicit, policy.RiskWrite), "the workspace's setting wins")
	require.False(t, PendingDowngrade(explicit, policy.RiskRead))
}

func TestGateVerdictAppliesTheRunLevelToMCP(t *testing.T) {
	ctxAt := func(level types.AssistantAccessLevel) context.Context {
		return types.WithAccessLevel(context.Background(), level)
	}
	g := NewGate(nil, &riskChecker{}, nil)
	read, write := readOnlyTool(true), readOnlyTool(false)

	require.Equal(t, policy.Allow, g.Verdict(ctxAt(types.AssistantAccessReadOnly), 1, "svc", read))
	require.Equal(t, policy.Deny, g.Verdict(ctxAt(types.AssistantAccessReadOnly), 1, "svc", write))
	require.Equal(t, policy.Ask, g.Verdict(ctxAt(types.AssistantAccessConfirm), 1, "svc", write))
	require.Equal(t, policy.Allow, g.Verdict(ctxAt(types.AssistantAccessAutoWrite), 1, "svc", write))
	require.Equal(t, policy.Allow, g.Verdict(context.Background(), 1, "svc", write),
		"a run without a level keeps the unconfigured agent's auto_write")

	always := NewGate(nil, &riskChecker{row: &types.MCPToolApproval{RequireApproval: true}}, nil)
	require.Equal(t, policy.Ask, always.Verdict(ctxAt(types.AssistantAccessAutoWrite), 1, "svc", read))

	destructive := NewGate(nil, &riskChecker{row: &types.MCPToolApproval{Risk: "destructive"}}, nil)
	require.Equal(t, policy.Ask, destructive.Verdict(ctxAt(types.AssistantAccessAutoWrite), 1, "svc", write))
}

func TestGateVerdictRecordsOnlyRaisedDeclarations(t *testing.T) {
	ctx := types.WithAccessLevel(context.Background(), types.AssistantAccessConfirm)
	checker := &riskChecker{}
	g := NewGate(nil, checker, nil)

	g.Verdict(ctx, 1, "svc", readOnlyTool(true))
	require.Equal(t, []string{"read"}, checker.observed, "a first declaration is recorded")

	checker.row, checker.observed = &types.MCPToolApproval{ObservedRisk: "read"}, nil
	require.Equal(t, policy.Ask, g.Verdict(ctx, 1, "svc", readOnlyTool(false)))
	require.Equal(t, []string{"write"}, checker.observed, "a raised declaration is recorded")

	checker.row, checker.observed = &types.MCPToolApproval{ObservedRisk: "write"}, nil
	require.Equal(t, policy.Ask, g.Verdict(ctx, 1, "svc", readOnlyTool(true)),
		"a lowered declaration waits for the workspace")
	require.Empty(t, checker.observed)
}

func TestGateToolRisksReadsPoliciesInBulk(t *testing.T) {
	g := NewGate(nil, nil, nil)
	risks, err := g.ToolRisks(context.Background(), 1, "svc", []*types.MCPTool{readOnlyTool(true)})
	require.NoError(t, err)
	require.Equal(t, policy.RiskRead, risks["t"])
}
