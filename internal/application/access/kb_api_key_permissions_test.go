package access

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestGranularAPIKeySharedKBPermissionLifecycle(t *testing.T) {
	caller := types.Caller{TenantID: 42, Role: types.TenantRoleViewer}
	kb := &types.KnowledgeBase{ID: "shared", TenantID: 99}
	ctx := types.WithCaller(context.Background(), caller)
	ctx = types.WithTenantAPIKeyScope(ctx, types.TenantAPIKeyScope{
		Capabilities: types.StringArray{"retrieve", "ingest"},
		KnowledgeBasePermissions: types.APIKeyKBPermissions{
			"shared":    types.APIKeyKBWrite,
			"read-only": types.APIKeyKBRead,
		},
	})
	shares := &shareLookup{permission: types.OrgRoleEditor}
	agents := &agentLookup{any: true}
	grant, err := ResolveKB(ctx, KBRequest{Caller: caller}, kb, types.OrgRoleEditor, shares, agents)
	require.NoError(t, err)
	require.Equal(t, uint64(42), shares.caller)
	require.Equal(t, types.TenantRoleOwner, shares.role, "machine grants do not inherit the compatibility Viewer cap")
	require.NoError(t, RequireKBWrite(grant.Context(ctx), kb))
	require.Equal(t, caller, types.CallerFromContext(grant.Context(ctx)))
	_, err = ResolveKB(
		ctx,
		KBRequest{Caller: caller},
		&types.KnowledgeBase{ID: "read-only", TenantID: 42},
		types.OrgRoleEditor,
		shares,
		agents,
	)
	require.Error(t, err, "global ingest cannot write a read-only owned KB")

	shares.permission = types.OrgRoleViewer
	_, err = ResolveKB(ctx, KBRequest{Caller: caller}, kb, types.OrgRoleEditor, shares, agents)
	require.Error(t, err, "share downgrade takes effect on the next resolution")
	_, err = ResolveKB(ctx, KBRequest{Caller: caller}, kb, types.OrgRoleViewer, shares, agents)
	require.NoError(t, err)

	shares.permission = ""
	_, err = ResolveKB(ctx, KBRequest{Caller: caller}, kb, types.OrgRoleViewer, shares, agents)
	require.Error(t, err, "shared-agent fallback must not resurrect a revoked direct share")
	require.Zero(t, agents.anyCalls)
	allowed, err := NewKBPermissions(ctx, shares).Check(kb.ID, kb.TenantID, types.OrgRoleViewer)
	require.NoError(t, err)
	require.False(t, allowed)
}

func TestGranularAPIKeyWriteGrantCannotBypassNarrowing(t *testing.T) {
	caller := types.Caller{TenantID: 42, Role: types.TenantRoleViewer}
	kb := &types.KnowledgeBase{ID: "kb", TenantID: 42}
	ctx := types.WithCaller(context.Background(), caller)
	grant, err := ResolveKB(ctx, KBRequest{Caller: caller}, kb, types.OrgRoleEditor, nil, nil)
	require.NoError(t, err)
	ctx = types.WithTenantAPIKeyScope(grant.Context(ctx), types.TenantAPIKeyScope{
		Capabilities:             types.StringArray{"ingest"},
		KnowledgeBasePermissions: types.APIKeyKBPermissions{"kb": types.APIKeyKBRead},
	})
	require.False(t, HasKBGrant(ctx, "kb", 42, types.OrgRoleEditor))
	require.Error(t, RequireKBWrite(ctx, kb))
}
