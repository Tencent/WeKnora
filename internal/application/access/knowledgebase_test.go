package access

import (
	"context"
	"errors"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

type shareLookup struct {
	permission types.OrgMemberRole
	err        error
	caller     uint64
	role       types.TenantRole
}

func (s *shareLookup) CheckTenantKBPermission(
	_ context.Context,
	_ string,
	caller uint64,
	role types.TenantRole,
) (types.OrgMemberRole, bool, error) {
	s.caller, s.role = caller, role
	return s.permission, s.permission != "", s.err
}

type agentLookup struct {
	agent    *types.CustomAgent
	any      bool
	err      error
	anyCalls int
	source   uint64
}

func (s *agentLookup) GetSharedAgentForTenant(
	_ context.Context,
	_ uint64,
	_ types.TenantRole,
	_ string,
	source ...uint64,
) (*types.CustomAgent, error) {
	s.source = source[0]
	return s.agent, s.err
}

func (s *agentLookup) TenantCanAccessKBViaSomeSharedAgent(
	context.Context,
	uint64,
	types.TenantRole,
	*types.KnowledgeBase,
) (bool, error) {
	s.anyCalls++
	return s.any, s.err
}

func TestResolveKBPermissionMatrix(t *testing.T) {
	for _, tt := range []struct {
		name                                            string
		own                                             bool
		permission                                      types.OrgMemberRole
		required                                        types.OrgMemberRole
		shareError                                      bool
		agentID, source, mode                           string
		selected                                        []string
		wrongTenant, missingAgent, agentError, anyAgent bool
		want                                            types.OrgMemberRole
		wantErr                                         error
	}{
		{name: "own KB", own: true, required: types.OrgRoleEditor, want: types.OrgRoleAdmin},
		{
			name:       "organization reader",
			permission: types.OrgRoleViewer,
			required:   types.OrgRoleViewer,
			want:       types.OrgRoleViewer,
		},

		{
			name:       "organization editor",
			permission: types.OrgRoleEditor,
			required:   types.OrgRoleEditor,
			want:       types.OrgRoleEditor,
		},

		{
			name:       "reader cannot write",
			permission: types.OrgRoleViewer,
			required:   types.OrgRoleEditor,
			wantErr:    ErrForbidden,
		},

		{
			name:       "share lookup fails closed",
			permission: types.OrgRoleEditor,
			shareError: true,
			required:   types.OrgRoleViewer,
			wantErr:    ErrForbidden,
		},

		{
			name:       "independent agent grant after share error",
			shareError: true,
			anyAgent:   true,
			required:   types.OrgRoleViewer,
			want:       types.OrgRoleViewer,
		},

		{name: "any shared agent read", anyAgent: true, required: types.OrgRoleViewer, want: types.OrgRoleViewer},
		{name: "any agent cannot write", anyAgent: true, required: types.OrgRoleEditor, wantErr: ErrForbidden},
		{
			name:       "agent lookup error",
			anyAgent:   true,
			agentError: true,
			required:   types.OrgRoleViewer,
			wantErr:    ErrForbidden,
		},

		{
			name:     "explicit all",
			agentID:  "agent",
			source:   "2",
			mode:     "all",
			required: types.OrgRoleViewer,
			want:     types.OrgRoleViewer,
		},

		{
			name:     "explicit selected",
			agentID:  "agent",
			mode:     "selected",
			selected: []string{"kb"},
			required: types.OrgRoleViewer,
			want:     types.OrgRoleViewer,
		},

		{
			name:     "selection miss does not fall back",
			agentID:  "agent",
			mode:     "selected",
			selected: []string{"other"},
			anyAgent: true,
			required: types.OrgRoleViewer,
			wantErr:  ErrForbidden,
		},

		{
			name:     "none does not fall back",
			agentID:  "agent",
			mode:     "none",
			anyAgent: true,
			required: types.OrgRoleViewer,
			wantErr:  ErrForbidden,
		},

		{
			name:     "unknown mode denied",
			agentID:  "agent",
			mode:     "invalid",
			required: types.OrgRoleViewer,
			wantErr:  ErrForbidden,
		},

		{
			name:        "tenant mismatch denied",
			agentID:     "agent",
			mode:        "all",
			wrongTenant: true,
			required:    types.OrgRoleViewer,
			wantErr:     ErrForbidden,
		},

		{
			name:         "missing explicit agent does not fall back",
			agentID:      "agent",
			missingAgent: true,
			anyAgent:     true,
			required:     types.OrgRoleViewer,
			wantErr:      ErrForbidden,
		},

		{
			name:     "invalid source rejected",
			agentID:  "agent",
			source:   "bad",
			mode:     "all",
			required: types.OrgRoleViewer,
			wantErr:  ErrInvalidAgentSource,
		},

		{
			name:     "own grant precedes agent parsing",
			own:      true,
			agentID:  "agent",
			source:   "bad",
			required: types.OrgRoleViewer,
			want:     types.OrgRoleAdmin,
		},

		{
			name:       "share grant precedes agent parsing",
			permission: types.OrgRoleViewer,
			agentID:    "agent",
			source:     "bad",
			required:   types.OrgRoleViewer,
			want:       types.OrgRoleViewer,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			kb := &types.KnowledgeBase{ID: "kb", TenantID: 2}
			if tt.own {
				kb.TenantID = 1
			}
			shares := &shareLookup{permission: tt.permission}
			if tt.shareError {
				shares.err = errors.New("unavailable")
			}
			agents := &agentLookup{
				any: tt.anyAgent,
				agent: &types.CustomAgent{
					TenantID: 2,
					Config:   types.CustomAgentConfig{KBSelectionMode: tt.mode, KnowledgeBases: tt.selected},
				},
			}
			if tt.wrongTenant {
				agents.agent.TenantID = 3
			}
			if tt.missingAgent {
				agents.agent = nil
			}
			if tt.agentError {
				agents.err = errors.New("unavailable")
			}
			request := KBRequest{
				Caller:              types.Caller{TenantID: 1, Role: types.TenantRoleViewer},
				AgentID:             tt.agentID,
				AgentSourceTenantID: tt.source,
			}
			grant, err := ResolveKB(context.Background(), request, kb, tt.required, shares, agents)
			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
				require.Nil(t, grant)
			} else {
				require.NoError(t, err)
				require.Equal(t, tt.want, grant.Permission)
				require.Equal(t, uint64(1), grant.Caller.TenantID)
				require.Equal(t, kb.TenantID, grant.EffectiveTenantID)
			}
			if !tt.own {
				require.Equal(t, uint64(1), shares.caller)
				require.Equal(t, types.TenantRoleViewer, shares.role)
			}
			if tt.agentID != "" {
				require.Zero(t, agents.anyCalls)
			}
			if tt.source == "2" {
				require.Equal(t, uint64(2), agents.source)
			}
		})
	}
}

func TestResolveKBMissingIdentityResourceAndAPIKeyScope(t *testing.T) {
	kb := &types.KnowledgeBase{ID: "kb", TenantID: 1}
	_, err := ResolveKB(context.Background(), KBRequest{}, kb, types.OrgRoleViewer, nil, nil)
	require.ErrorIs(t, err, ErrUnauthorized)
	_, err = ResolveKB(
		context.Background(),
		KBRequest{Caller: types.Caller{TenantID: 1}},
		nil,
		types.OrgRoleViewer,
		nil,
		nil,
	)
	require.ErrorIs(t, err, ErrNotFound)
	ctx := types.WithTenantAPIKeyScope(
		context.Background(),
		types.TenantAPIKeyScope{KnowledgeBaseIDs: types.StringArray{"other"}},
	)
	_, err = ResolveKB(ctx, KBRequest{Caller: types.Caller{TenantID: 1}}, kb, types.OrgRoleViewer, nil, nil)
	require.Error(t, err, "API key scope must apply even to owned KBs")
}
