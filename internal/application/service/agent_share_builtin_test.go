package service

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

type builtinShareAgentRepo struct {
	interfaces.CustomAgentRepository
	agent *types.CustomAgent
}

func (r *builtinShareAgentRepo) GetAgentByID(context.Context, string, uint64) (*types.CustomAgent, error) {
	return r.agent, nil
}

type builtinShareRepo struct {
	interfaces.AgentShareRepository
}

func (builtinShareRepo) GetShareByAgentIDForTenant(
	_ context.Context, _ uint64, agentID string, _ uint64,
) (*types.AgentShare, error) {
	return &types.AgentShare{AgentID: agentID, SourceTenantID: 84}, nil
}

func (builtinShareRepo) GetShareByAgentIDAndSourceForTenant(
	_ context.Context, _ uint64, agentID string, sourceTenantID uint64,
) (*types.AgentShare, error) {
	return &types.AgentShare{AgentID: agentID, SourceTenantID: sourceTenantID}, nil
}

// Every workspace owns a copy of each built-in agent under the same ID, so a
// shared one would be indistinguishable from the receiver's own agent.
func TestShareAgentRejectsBuiltinAgent(t *testing.T) {
	svc := &agentShareService{agentRepo: &builtinShareAgentRepo{agent: &types.CustomAgent{
		ID: types.BuiltinQuickAnswerID, TenantID: 84, IsBuiltin: true,
		Config: types.CustomAgentConfig{ModelID: "model-1", KBSelectionMode: "none"},
	}}}

	_, err := svc.ShareAgent(
		context.Background(), types.BuiltinQuickAnswerID, "org-1", "user-1", 84, types.OrgRoleViewer)
	require.ErrorIs(t, err, ErrBuiltinAgentNotShareable)
}

// Shares of built-in agents made before sharing them was refused must not
// resolve, with or without a source selector.
func TestGetSharedAgentForTenantRefusesBuiltinAgents(t *testing.T) {
	svc := &agentShareService{
		shareRepo: builtinShareRepo{},
		agentRepo: &builtinShareAgentRepo{agent: &types.CustomAgent{
			ID: "legacy-builtin-row", TenantID: 84, IsBuiltin: true,
		}},
	}
	for _, source := range []uint64{0, 84} {
		agent, err := svc.GetSharedAgentForTenant(
			context.Background(), 7, types.TenantRoleAdmin, "legacy-builtin-row", source)
		require.Error(t, err, "source=%d", source)
		require.Nil(t, agent)
	}
}
