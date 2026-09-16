package access

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestKBWriteConsumesOperationGrant(t *testing.T) {
	ctx := callerContext()
	kb := &types.KnowledgeBase{ID: "kb", TenantID: types.CallerFromContext(ctx).TenantID}
	read, err := ResolveKB(ctx, KBRequest{Caller: types.CallerFromContext(ctx)}, kb, types.OrgRoleViewer, nil, nil)
	require.NoError(t, err)
	require.Equal(t, types.OrgRoleAdmin, read.Permission, "owner permission projection remains unchanged")
	require.ErrorIs(t, RequireKBWrite(read.Context(ctx), kb), ErrForbidden, "an owner read does not grant mutation")
	require.ErrorIs(t, RequireKBWrite(types.WithExecutionTenant(ctx, kb.TenantID), kb), ErrForbidden)
	write, err := ResolveKB(ctx, KBRequest{Caller: types.CallerFromContext(ctx)}, kb, types.OrgRoleEditor, nil, nil)
	require.NoError(t, err)
	ctx = logger.CloneContext(write.Context(ctx))
	require.NoError(t, RequireKBWrite(ctx, kb))
	require.ErrorIs(t, RequireKBWrite(ctx, &types.KnowledgeBase{ID: "other", TenantID: kb.TenantID}), ErrForbidden)
	require.ErrorIs(t, RequireKBWrite(ctx, &types.KnowledgeBase{ID: kb.ID, TenantID: kb.TenantID + 1}), ErrForbidden)
	for _, tt := range []struct {
		name    string
		scope   types.TenantAPIKeyScope
		allowed bool
	}{
		{"retrieve only", types.TenantAPIKeyScope{Capabilities: types.StringArray{"retrieve"}}, false},
		{
			"ingest",
			types.TenantAPIKeyScope{
				Capabilities:     types.StringArray{"ingest"},
				KnowledgeBaseIDs: types.StringArray{"kb"},
			},
			true,
		},

		{
			"wrong KB",
			types.TenantAPIKeyScope{
				Capabilities:     types.StringArray{"ingest"},
				KnowledgeBaseIDs: types.StringArray{"other"},
			},
			false,
		},

		{"full access", types.TenantAPIKeyScope{FullAccess: true}, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			err := RequireKBWrite(types.WithTenantAPIKeyScope(ctx, tt.scope), kb)
			require.Equal(t, tt.allowed, err == nil)
		})
	}
}

func TestKBTaskWriteIsExplicitAndBounded(t *testing.T) {
	kb := &types.KnowledgeBase{ID: "kb", TenantID: 7}
	base := context.Background()
	ctx, err := WithKBTaskWrite(base, kb, 7)
	require.NoError(t, err)
	ctx = logger.CloneContext(ctx)
	require.Zero(t, types.CallerFromContext(ctx).TenantID, "a worker must not impersonate the tenant owner")
	require.NoError(t, RequireKBWrite(ctx, kb))
	allowed, err := NewKBPermissions(ctx, nil).Check("kb", 7, types.OrgRoleViewer)
	require.NoError(t, err)
	require.True(t, allowed, "a scoped write task can read its KB")
	allowed, err = NewKBPermissions(ctx, nil).Check("other", 7, types.OrgRoleViewer)
	require.NoError(t, err)
	require.False(t, allowed)
	require.ErrorIs(t, RequireKBWrite(ctx, &types.KnowledgeBase{ID: "other", TenantID: 7}), ErrForbidden)
	_, err = WithKBTaskWrite(base, kb, 8)
	require.ErrorIs(t, err, ErrForbidden)
	_, err = WithKBTaskWrite(base, kb, 0)
	require.ErrorIs(t, err, ErrForbidden)
	require.False(t, HasKBGrant(base, "kb", 7, types.OrgRoleEditor), "the parent context stays unmodified")
	request := callerContext()
	task, err := WithKBTaskWrite(request, kb, 7)
	require.NoError(t, err)
	require.Equal(t, types.CallerFromContext(request), types.CallerFromContext(task))
	changed := types.WithCaller(task, types.Caller{TenantID: 99, UserID: "another"})
	require.ErrorIs(t, RequireKBWrite(changed, kb), ErrForbidden)
}
