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

type receiverViewOrgRepo struct {
	interfaces.OrganizationRepository
}

func (receiverViewOrgRepo) GetTenantMember(context.Context, string, uint64) (*types.OrganizationTenantMember, error) {
	return &types.OrganizationTenantMember{Role: types.OrgRoleViewer}, nil
}

type receiverViewShareRepo struct {
	interfaces.AgentShareRepository
	shares []*types.AgentShare
}

func (r receiverViewShareRepo) ListByOrganization(context.Context, string) ([]*types.AgentShare, error) {
	return r.shares, nil
}

type receiverViewDisabledRepo struct {
	interfaces.TenantDisabledSharedAgentRepository
}

func (receiverViewDisabledRepo) ListByTenantID(context.Context, uint64) ([]*types.TenantDisabledSharedAgent, error) {
	return nil, nil
}

// Other workspaces see what a shared agent can do and reach, not its prompts
// or its creator; the sharing workspace still gets the full config to edit.
func TestSharedAgentListsWithholdPromptsFromReceivers(t *testing.T) {
	agent := func(tenantID uint64) *types.CustomAgent {
		return &types.CustomAgent{
			ID: "agent", TenantID: tenantID, CreatedBy: "owner-user",
			Config: types.CustomAgentConfig{
				SystemPrompt: "secret prompt", RewritePromptSystem: "secret rewrite",
				KBSelectionMode: "selected", KnowledgeBases: []string{"kb-1"}, ModelID: "model-1",
				QuestionSuggestions: &types.QuestionSuggestionConfig{
					Starters:  types.StarterSuggestionConfig{Items: []string{"What is new?"}},
					FollowUps: types.FollowUpSuggestionConfig{AdditionalInstruction: "secret follow-up instruction"},
				},
			},
		}
	}
	svc := &agentShareService{
		orgRepo:      receiverViewOrgRepo{},
		disabledRepo: receiverViewDisabledRepo{},
		shareRepo: receiverViewShareRepo{shares: []*types.AgentShare{
			{AgentID: "agent", SourceTenantID: 84, Permission: types.OrgRoleViewer, Agent: agent(84)},
			{AgentID: "agent", SourceTenantID: 7, Permission: types.OrgRoleViewer, Agent: agent(7)},
		}},
	}

	items, err := svc.ListSharedAgentsInOrganization(context.Background(), "org", 7, types.TenantRoleAdmin)
	require.NoError(t, err)
	require.Len(t, items, 2)
	for _, item := range items {
		if item.IsMine {
			require.Equal(t, "secret prompt", item.Agent.Config.SystemPrompt)
			require.Equal(t, "secret follow-up instruction",
				item.Agent.Config.QuestionSuggestions.FollowUps.AdditionalInstruction)
			continue
		}
		require.Empty(t, item.Agent.Config.SystemPrompt)
		require.Empty(t, item.Agent.Config.RewritePromptSystem)
		require.Empty(t, item.Agent.Config.QuestionSuggestions.FollowUps.AdditionalInstruction)
		require.Equal(t, []string{"What is new?"}, item.Agent.Config.QuestionSuggestions.Starters.Items,
			"starter questions are shown to receivers anyway")
		require.Empty(t, item.Agent.CreatedBy)
		require.Equal(t, []string{"kb-1"}, item.Agent.Config.KnowledgeBases, "the scope stays visible")
		require.Equal(t, "model-1", item.Agent.Config.ModelID)
	}
}

type receiverViewTenantShareRepo struct {
	interfaces.AgentShareRepository
	shares []*types.AgentShare
}

func (r receiverViewTenantShareRepo) ListSharedAgentsForTenant(context.Context, uint64) ([]*types.AgentShare, error) {
	return r.shares, nil
}

// /shared-agents lists only other workspaces' agents, all as receiver views.
func TestListSharedAgentsWithholdsPrompts(t *testing.T) {
	agent := &types.CustomAgent{
		ID: "agent", TenantID: 84, CreatedBy: "owner-user",
		Config: types.CustomAgentConfig{SystemPrompt: "secret prompt", ModelID: "model-1"},
	}
	svc := &agentShareService{
		orgRepo:      receiverViewOrgRepo{},
		disabledRepo: receiverViewDisabledRepo{},
		shareRepo: receiverViewTenantShareRepo{shares: []*types.AgentShare{
			{AgentID: "agent", SourceTenantID: 84, Permission: types.OrgRoleViewer, Agent: agent},
		}},
	}

	infos, err := svc.ListSharedAgents(context.Background(), 7, types.TenantRoleAdmin)
	require.NoError(t, err)
	require.Len(t, infos, 1)
	require.Empty(t, infos[0].Agent.Config.SystemPrompt)
	require.Empty(t, infos[0].Agent.CreatedBy)
	require.Equal(t, "model-1", infos[0].Agent.Config.ModelID)
}
