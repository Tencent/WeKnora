package service

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

// Workspace 42: "mine" was created by the sharer, "colleague" by someone else,
// "legacy" has no creator; "foreign" belongs to another workspace.
func scopeTestLookup(_ context.Context, ids []string) ([]*types.KnowledgeBase, error) {
	all := map[string]*types.KnowledgeBase{
		"mine":      {ID: "mine", TenantID: 42, CreatorID: "sharer"},
		"colleague": {ID: "colleague", TenantID: 42, CreatorID: "someone-else"},
		"legacy":    {ID: "legacy", TenantID: 42},
		"foreign":   {ID: "foreign", TenantID: 7, CreatorID: "someone-else"},
	}
	var out []*types.KnowledgeBase
	for _, id := range ids {
		if kb, ok := all[id]; ok {
			out = append(out, kb)
		}
	}
	return out, nil
}

func scopedAgent(mode string, kbs ...string) *types.CustomAgent {
	return &types.CustomAgent{
		ID: "agent-1", TenantID: 42,
		Config: types.CustomAgentConfig{KBSelectionMode: mode, KnowledgeBases: kbs},
	}
}

func TestCheckAgentKBScopeShareable(t *testing.T) {
	role := func(r types.TenantRole) context.Context {
		return context.WithValue(context.Background(), types.TenantRoleContextKey, r)
	}
	contributor := role(types.TenantRoleContributor)
	fullAccess := types.WithTenantAPIKeyScope(contributor, types.TenantAPIKeyScope{FullAccess: true})
	cases := []struct {
		name          string
		ctx           context.Context
		before, after *types.CustomAgent
		wantErr       bool
	}{
		{name: "own KBs", ctx: contributor, after: scopedAgent("selected", "mine")},
		{
			name: "another workspace's KB is outside the scope", ctx: contributor,
			after: scopedAgent("selected", "mine", "foreign"),
		},
		{name: "colleague's KB", ctx: contributor, after: scopedAgent("selected", "mine", "colleague"), wantErr: true},
		{name: "KB without a creator", ctx: contributor, after: scopedAgent("selected", "legacy"), wantErr: true},
		{name: "all KBs", ctx: contributor, after: scopedAgent("all"), wantErr: true},
		{name: "no KBs", ctx: contributor, after: scopedAgent("none")},
		{name: "admin shares anything", ctx: role(types.TenantRoleAdmin), after: scopedAgent("all")},
		{name: "full-access key", ctx: fullAccess, after: scopedAgent("all")},
		{
			name: "edit keeps an admin-shared all scope", ctx: contributor,
			before: scopedAgent("all"), after: scopedAgent("all"),
		},
		{
			name: "edit narrows the scope", ctx: contributor,
			before: scopedAgent("selected", "colleague", "mine"), after: scopedAgent("selected", "colleague"),
		},
		{
			name: "edit adds a colleague's KB", ctx: contributor,
			before: scopedAgent("selected", "mine"), after: scopedAgent("selected", "mine", "colleague"), wantErr: true,
		},
		{
			name: "edit widens to all", ctx: contributor,
			before: scopedAgent("selected", "mine"), after: scopedAgent("all"), wantErr: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := checkAgentKBScopeShareable(tc.ctx, scopeTestLookup, tc.before, tc.after, "sharer")
			if tc.wantErr {
				require.ErrorIs(t, err, ErrAgentKBScopeNotShareable)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

type scopeTestAgentShareRepo struct {
	interfaces.AgentShareRepository
	shares []*types.AgentShare
}

func (r *scopeTestAgentShareRepo) ListByAgent(context.Context, string) ([]*types.AgentShare, error) {
	return r.shares, nil
}

type scopeTestKBService struct {
	interfaces.KnowledgeBaseService
}

func (scopeTestKBService) GetKnowledgeBasesByIDsOnly(
	ctx context.Context, ids []string,
) ([]*types.KnowledgeBase, error) {
	return scopeTestLookup(ctx, ids)
}

// Only a shared agent is held to the share rule when it is edited.
func TestUpdateAgentChecksSharedAgentKBScope(t *testing.T) {
	ctx := context.WithValue(context.Background(), types.TenantRoleContextKey, types.TenantRoleContributor)
	ctx = context.WithValue(ctx, types.UserIDContextKey, "sharer")
	widened := types.CustomAgentConfig{KBSelectionMode: "selected", KnowledgeBases: []string{"mine", "colleague"}}

	unshared := &customAgentService{agentShareRepo: &scopeTestAgentShareRepo{}, kbService: scopeTestKBService{}}
	require.NoError(t, unshared.checkSharedAgentKBScope(ctx, scopedAgent("selected", "mine"), widened))

	shared := &customAgentService{
		agentShareRepo: &scopeTestAgentShareRepo{shares: []*types.AgentShare{{AgentID: "agent-1", SourceTenantID: 42}}},
		kbService:      scopeTestKBService{},
	}
	require.ErrorIs(t, shared.checkSharedAgentKBScope(ctx, scopedAgent("selected", "mine"), widened),
		ErrAgentKBScopeNotShareable)
}
