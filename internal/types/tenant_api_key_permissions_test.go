package types

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAPIKeyKBPermissionsProjectOperations(t *testing.T) {
	grants := APIKeyKBPermissions{"a": APIKeyKBRead, "b": APIKeyKBWrite, "c": APIKeyKBManage}
	ctx := WithTenantAPIKeyScope(context.Background(), TenantAPIKeyScope{KnowledgeBasePermissions: grants})
	// The context owns a snapshot; caller mutation cannot widen it.
	grants["a"] = APIKeyKBManage
	scope, _ := TenantAPIKeyScopeFromContext(ctx)
	for _, tc := range []struct {
		permission APIKeyKBPermission
		ids        StringArray
	}{
		{APIKeyKBRead, StringArray{"a", "b", "c"}},
		{APIKeyKBWrite, StringArray{"b", "c"}},
		{APIKeyKBManage, StringArray{"c"}},
	} {
		t.Run(string(tc.permission), func(t *testing.T) {
			projected := scope.ForKnowledgeBasePermission(tc.permission)
			require.Equal(t, tc.ids, projected.KnowledgeBaseIDs)
			require.False(t, projected.AllowsKnowledgeBase("outside"))
			require.True(t, projected.AllowsKnowledgeBases(tc.ids))
			require.False(t, projected.AllowsKnowledgeBases(append(tc.ids, "outside")))
		})
	}
	require.Equal(t, APIKeyKBRead, scope.KnowledgeBasePermissions["a"])
	require.False(t, scope.HasCapability(APIKeyCapabilityIngest), "KB grants must never mint route capabilities")
}

func TestAPIKeyKBPermissionsEmptyIsDenyAll(t *testing.T) {
	for _, scope := range []TenantAPIKeyScope{
		{KnowledgeBasePermissions: APIKeyKBPermissions{}},
		{KnowledgeBasePermissions: APIKeyKBPermissions{"a": APIKeyKBRead}, KnowledgeBasePermission: APIKeyKBWrite},
		{KnowledgeBasePermissions: APIKeyKBPermissions{"a": "invalid"}},
	} {
		ctx := WithTenantAPIKeyScope(context.Background(), scope)
		scope, _ = TenantAPIKeyScopeFromContext(ctx)
		require.True(t, scope.IsKnowledgeBaseRestricted())
		require.Empty(t, scope.KnowledgeBaseIDs)
		require.False(t, scope.AllowsKnowledgeBase("a"))
		require.False(t, scope.AllowsKnowledgeBases(nil))
		require.Error(t, AuthorizeTenantAPIKeyKnowledgeBases(ctx, "a"))
		filtered, err := FilterKnowledgeBasesForTenantAPIKeyScope(ctx, nil, []string{"a", "b"})
		require.NoError(t, err)
		require.Empty(t, filtered)
	}
	require.True(t, (TenantAPIKeyScope{}).AllowsKnowledgeBase("a"), "legacy empty scope stays unrestricted")
}

func TestAPIKeyKBPermissionsValidationAndStorage(t *testing.T) {
	for _, grants := range []APIKeyKBPermissions{nil, {}, {"kb": APIKeyKBManage}} {
		value, err := grants.Value()
		require.NoError(t, err)
		var restored APIKeyKBPermissions
		require.NoError(t, restored.Scan(value))
		require.Equal(t, grants, restored)
	}
	var restored APIKeyKBPermissions
	require.NoError(t, restored.Scan(`{}`))
	require.NotNil(t, restored)
	require.Error(t, restored.Scan(42))
	require.Error(t, restored.Scan(`[]`))
	for _, grants := range []APIKeyKBPermissions{{"": APIKeyKBRead}, {" kb ": APIKeyKBRead}, {"kb": "owner"}} {
		require.Error(t, ValidateAPIKeyKBPermissions(grants, nil))
	}
	require.Error(t, ValidateAPIKeyKBPermissions(APIKeyKBPermissions{}, []string{"kb"}))
}
