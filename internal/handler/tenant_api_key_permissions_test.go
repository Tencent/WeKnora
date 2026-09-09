package handler

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

type apiKeyShareLookup struct {
	permission types.OrgMemberRole
	err        error
}

func (s apiKeyShareLookup) CheckTenantKBPermission(
	context.Context, string, uint64, types.TenantRole,
) (types.OrgMemberRole, bool, error) {
	return s.permission, s.permission != "", s.err
}

func TestAPIKeyDelegationValidatesLiveShareCeiling(t *testing.T) {
	lookup := func(_ context.Context, id string) (*types.KnowledgeBase, error) {
		if id == "missing" {
			return nil, nil
		}
		owner := uint64(42)
		if id == "shared" {
			owner = 99
		}
		return &types.KnowledgeBase{ID: id, TenantID: owner}, nil
	}
	for _, tc := range []struct {
		id      string
		grant   types.APIKeyKBPermission
		share   types.OrgMemberRole
		allowed bool
	}{
		{"owned", types.APIKeyKBManage, "", true},
		{"shared", types.APIKeyKBRead, types.OrgRoleViewer, true},
		{"shared", types.APIKeyKBWrite, types.OrgRoleViewer, false},
		{"shared", types.APIKeyKBManage, types.OrgRoleEditor, true},
		{"shared", types.APIKeyKBRead, "", false},
		{"missing", types.APIKeyKBRead, types.OrgRoleViewer, false},
	} {
		err := validateAPIKeyKBGrants(
			context.Background(),
			42,
			types.APIKeyKBPermissions{tc.id: tc.grant},
			lookup,
			apiKeyShareLookup{permission: tc.share},
		)
		require.Equal(t, tc.allowed, err == nil, "%+v", tc)
	}
	require.Error(
		t,
		validateAPIKeyKBGrants(
			context.Background(),
			42,
			types.APIKeyKBPermissions{"shared": types.APIKeyKBRead},
			lookup,
			apiKeyShareLookup{err: context.DeadlineExceeded},
		),
	)
	require.Nil(t, validateTenantAPIKeyRequest(context.Background(), nil, 42, tenantAPIKeyCreateRequest{
		Name: "no KBs", Capabilities: []string{"chat"}, KnowledgeBasePermissions: types.APIKeyKBPermissions{},
	}, nil))
}

func TestGranularAPIKeySearchUsesSourceTenantAndLiveShare(t *testing.T) {
	ctx := types.WithCaller(context.Background(), types.Caller{TenantID: 42, Role: types.TenantRoleViewer})
	ctx = types.WithTenantAPIKeyScope(ctx, types.TenantAPIKeyScope{
		KnowledgeBasePermissions: types.APIKeyKBPermissions{"owned": types.APIKeyKBRead, "shared": types.APIKeyKBRead},
	})
	lookup := func(_ context.Context, id string) (*types.KnowledgeBase, error) {
		owner := uint64(42)
		if id == "shared" {
			owner = 99
		}
		return &types.KnowledgeBase{ID: id, TenantID: owner}, nil
	}
	scopes, restricted, err := resolveAPIKeySearchScopes(
		ctx,
		lookup,
		apiKeyShareLookup{permission: types.OrgRoleViewer},
	)
	require.NoError(t, err)
	require.True(t, restricted)
	require.Equal(
		t,
		[]types.KnowledgeSearchScope{{TenantID: 42, KBID: "owned"}, {TenantID: 99, KBID: "shared"}},
		scopes,
	)
	scopes, _, err = resolveAPIKeySearchScopes(ctx, lookup, apiKeyShareLookup{})
	require.NoError(t, err)
	require.Equal(t, []types.KnowledgeSearchScope{{TenantID: 42, KBID: "owned"}}, scopes)
	ctx = types.WithTenantAPIKeyScope(
		ctx,
		types.TenantAPIKeyScope{KnowledgeBasePermissions: types.APIKeyKBPermissions{}},
	)
	scopes, restricted, err = resolveAPIKeySearchScopes(ctx, lookup, apiKeyShareLookup{})
	require.NoError(t, err)
	require.True(t, restricted)
	require.Empty(t, scopes)
	require.Empty(t, filterKnowledgeBasesForAPIKeyScope(ctx, []*types.KnowledgeBase{{ID: "owned", TenantID: 42}}))
}
