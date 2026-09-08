package access

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func callerContext() context.Context {
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(1))
	ctx = context.WithValue(ctx, types.UserIDContextKey, "user")
	return context.WithValue(ctx, types.TenantRoleContextKey, types.TenantRoleContributor)
}

func TestKBGrantSurvivesExecutionSwitchAndDetachWithoutWidening(t *testing.T) {
	ctx := callerContext()
	kb := &types.KnowledgeBase{ID: "shared", TenantID: 2}
	grant, err := ResolveKB(
		ctx,
		KBRequest{Caller: types.CallerFromContext(ctx)},
		kb,
		types.OrgRoleViewer,
		&shareLookup{permission: types.OrgRoleViewer},
		nil,
	)
	require.NoError(t, err)
	ctx = grant.Context(ctx)
	require.Equal(t, uint64(2), types.MustTenantIDFromContext(ctx))
	ctx = logger.CloneContext(types.WithExecutionTenant(ctx, 3))
	require.Equal(
		t,
		types.Caller{TenantID: 1, UserID: "user", Role: types.TenantRoleContributor},
		types.CallerFromContext(ctx),
	)
	require.True(t, HasKBGrant(ctx, "shared", 2, types.OrgRoleViewer))
	// A later mutation of the service model cannot mutate the captured grant.
	kb.ID = "private"
	require.False(t, HasKBGrant(ctx, "private", 2, types.OrgRoleViewer))
	require.False(t, HasKBGrant(ctx, "shared", 3, types.OrgRoleViewer))
	require.False(t, HasKBGrant(ctx, "shared", 2, types.OrgRoleEditor))
	for _, caller := range []types.Caller{
		{TenantID: 9, UserID: "user", Role: types.TenantRoleContributor},
		{TenantID: 1, UserID: "other", Role: types.TenantRoleContributor},
		{TenantID: 1, UserID: "user", Role: types.TenantRoleViewer},
	} {
		otherCtx := types.WithCaller(ctx, caller)
		require.False(t, HasKBGrant(otherCtx, "shared", 2, types.OrgRoleViewer))
		require.Equal(
			t,
			caller,
			types.CallerFromContext(grant.Context(otherCtx)),
			"reusing a grant cannot impersonate its caller",
		)
		require.Equal(
			t,
			uint64(3),
			types.MustTenantIDFromContext(grant.Context(otherCtx)),
			"a caller mismatch must not change execution scope",
		)
	}
	narrowed := types.WithTenantAPIKeyScope(
		ctx,
		types.TenantAPIKeyScope{KnowledgeBaseIDs: types.StringArray{"another"}},
	)
	require.False(t, HasKBGrant(narrowed, "shared", 2, types.OrgRoleViewer))
}

func TestExecutionTenantDoesNotGrantOwnershipOrChangeShareIdentity(t *testing.T) {
	ctx := types.WithExecutionTenant(callerContext(), 2)
	shares := &shareLookup{}
	allowed, err := NewKBPermissions(ctx, shares).Check("private", 2, types.OrgRoleViewer)
	require.NoError(t, err)
	require.False(t, allowed)
	require.Equal(t, uint64(1), shares.caller)
	require.Equal(t, types.TenantRoleContributor, shares.role)
	ctx = types.WithExecutionTenant(context.Background(), 2)
	allowed, err = NewKBPermissions(ctx, nil).Check("private", 2, types.OrgRoleViewer)
	require.NoError(t, err)
	require.False(t, allowed, "an absent caller must not become the execution tenant")
}

func TestSharedAgentGrantPreservesConfiguredScopeAcrossDetach(t *testing.T) {
	for _, mode := range []string{"all", "selected", "none", "unknown"} {
		t.Run(mode, func(t *testing.T) {
			agent := &types.CustomAgent{
				TenantID: 2,
				Config:   types.CustomAgentConfig{KBSelectionMode: mode, KnowledgeBases: []string{"selected"}},
			}
			ctx := logger.CloneContext(WithSharedAgent(callerContext(), agent))
			require.Equal(t, uint64(1), types.CallerFromContext(ctx).TenantID)
			require.Equal(t, uint64(2), types.MustTenantIDFromContext(ctx))
			require.Equal(t, mode == "all" || mode == "selected", HasKBGrant(ctx, "selected", 2, types.OrgRoleViewer))
			require.Equal(t, mode == "all", HasKBGrant(ctx, "other", 2, types.OrgRoleViewer))
			require.False(t, HasKBGrant(ctx, "selected", 3, types.OrgRoleViewer))
			require.False(t, HasKBGrant(ctx, "selected", 2, types.OrgRoleEditor))
		})
	}
}

func TestKBGrantBranchesRemainIndependent(t *testing.T) {
	base := callerContext()
	grant := &KBAccess{Caller: types.CallerFromContext(base), EffectiveTenantID: 2, Permission: types.OrgRoleViewer}
	grant.KnowledgeBase = &types.KnowledgeBase{ID: "first", TenantID: 2}
	first := grant.WithGrant(base)
	grant.KnowledgeBase = &types.KnowledgeBase{ID: "second", TenantID: 2}
	second := grant.WithGrant(base)
	require.True(t, HasKBGrant(first, "first", 2, types.OrgRoleViewer))
	require.False(t, HasKBGrant(first, "second", 2, types.OrgRoleViewer))
	require.True(t, HasKBGrant(second, "second", 2, types.OrgRoleViewer))
	require.False(t, HasKBGrant(second, "first", 2, types.OrgRoleViewer))
}

func TestKBPermissionsOwnerShortcutOnlyGrantsRead(t *testing.T) {
	roles := []types.TenantRole{types.TenantRoleViewer, types.TenantRoleContributor, types.TenantRoleAdmin}
	for _, role := range roles {
		ctx := context.WithValue(callerContext(), types.TenantRoleContextKey, role)
		permissions := NewKBPermissions(ctx, nil)
		for _, required := range []types.OrgMemberRole{types.OrgRoleViewer, types.OrgRoleEditor, types.OrgRoleAdmin} {
			allowed, err := permissions.Check("kb", 1, required)
			require.NoError(t, err)
			require.Equal(t, required == types.OrgRoleViewer, allowed)
		}
		grant := &KBAccess{
			Caller:            types.CallerFromContext(ctx),
			KnowledgeBase:     &types.KnowledgeBase{ID: "kb", TenantID: 1},
			EffectiveTenantID: 1,
			Permission:        types.OrgRoleEditor,
		}
		permissions = NewKBPermissions(grant.Context(ctx), nil)
		allowed, err := permissions.Check("kb", 1, types.OrgRoleEditor)
		require.NoError(t, err)
		require.True(t, allowed)
		allowed, err = permissions.Check("kb", 1, types.OrgRoleAdmin)
		require.NoError(t, err)
		require.False(t, allowed)
	}
}
