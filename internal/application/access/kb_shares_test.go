package access

import (
	"context"
	"errors"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

type permissionLookupFunc func(context.Context, string, uint64, types.TenantRole) (types.OrgMemberRole, bool, error)

func (f permissionLookupFunc) CheckTenantKBPermission(
	ctx context.Context,
	id string,
	tenant uint64,
	role types.TenantRole,
) (types.OrgMemberRole, bool, error) {
	return f(ctx, id, tenant, role)
}

func TestKBSharePermissionsCachePerOperation(t *testing.T) {
	calls := map[string]int{}
	granted := true
	lookup := permissionLookupFunc(
		func(_ context.Context, kb string, tenant uint64, role types.TenantRole) (types.OrgMemberRole, bool, error) {
			require.Equal(t, uint64(1), tenant)
			require.Equal(t, types.TenantRoleContributor, role)
			calls[kb]++
			return types.OrgRoleEditor, granted, nil
		},
	)
	permissions := NewKBSharePermissions(context.Background(), lookup, 1, types.TenantRoleContributor)
	for _, required := range []types.OrgMemberRole{types.OrgRoleViewer, types.OrgRoleEditor, types.OrgRoleAdmin} {
		allowed, err := permissions.Check("kb", required)
		require.NoError(t, err)
		require.Equal(t, required != types.OrgRoleAdmin, allowed)
	}
	require.Equal(t, 1, calls["kb"], "different minimum roles share the same membership lookup")
	_, err := permissions.Check("other", types.OrgRoleViewer)
	require.NoError(t, err)
	require.Equal(t, 1, calls["other"])
	granted = false
	fresh := NewKBSharePermissions(context.Background(), lookup, 1, types.TenantRoleContributor)
	allowed, err := fresh.Check("kb", types.OrgRoleViewer)
	require.NoError(t, err)
	require.False(t, allowed, "a new operation must observe share revocation")
	require.Equal(t, 2, calls["kb"])
}

func TestKBSharePermissionsCachesFailuresWithoutGranting(t *testing.T) {
	failure := errors.New("database unavailable")
	calls := 0
	lookup := permissionLookupFunc(
		func(context.Context, string, uint64, types.TenantRole) (types.OrgMemberRole, bool, error) {
			calls++
			return types.OrgRoleAdmin, true, failure
		},
	)
	permissions := NewKBSharePermissions(context.Background(), lookup, 1, types.TenantRoleAdmin)
	for i := 0; i < 3; i++ {
		allowed, err := permissions.Check("kb", types.OrgRoleViewer)
		require.False(t, allowed)
		require.ErrorIs(t, err, failure, "callers must retain the infrastructure error")
	}
	require.Equal(t, 1, calls)
}

func TestKBSharePermissionsMissingLookupAndInvalidRolesDeny(t *testing.T) {
	permissions := NewKBSharePermissions(context.Background(), nil, 1, types.TenantRoleAdmin)
	allowed, err := permissions.Check("kb", types.OrgRoleViewer)
	require.NoError(t, err)
	require.False(t, allowed)
	lookup := permissionLookupFunc(
		func(context.Context, string, uint64, types.TenantRole) (types.OrgMemberRole, bool, error) {
			return types.OrgRoleEditor, true, nil
		},
	)
	permissions = NewKBSharePermissions(context.Background(), lookup, 1, types.TenantRoleAdmin)
	allowed, err = permissions.Check("kb", "unknown")
	require.NoError(t, err)
	require.False(t, allowed)
}
