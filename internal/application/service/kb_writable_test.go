package service

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

// writableShareLookup mimics CheckTenantKBPermission: editor shares are capped
// at viewer for a tenant Viewer caller.
type writableShareLookup map[string]types.OrgMemberRole

func (l writableShareLookup) CheckTenantKBPermission(
	_ context.Context, kbID string, _ uint64, role types.TenantRole,
) (types.OrgMemberRole, bool, error) {
	permission, ok := l[kbID]
	if ok && role == types.TenantRoleViewer {
		permission = types.OrgRoleViewer
	}
	return permission, ok, nil
}

func TestKBWritableIDs(t *testing.T) {
	targets := types.SearchTargets{
		{KnowledgeBaseID: "own", TenantID: 42},
		{KnowledgeBaseID: "shared-editor", TenantID: 7},
		{KnowledgeBaseID: "shared-viewer", TenantID: 7},
		{KnowledgeBaseID: "agent-scope", TenantID: 7},
	}
	shares := writableShareLookup{"shared-editor": types.OrgRoleEditor, "shared-viewer": types.OrgRoleViewer}
	caller := func(role types.TenantRole, userID string) context.Context {
		return types.WithCaller(context.Background(), types.Caller{TenantID: 42, UserID: userID, Role: role})
	}

	require.Equal(t, []string{"own", "shared-editor"},
		kbWritableIDs(caller(types.TenantRoleContributor, "u"), shares, targets))
	require.Equal(t, []string{"own"}, kbWritableIDs(caller(types.TenantRoleViewer, "u"), shares, targets),
		"a tenant Viewer never gets write access through a share")
	require.Equal(t, []string{"own"}, kbWritableIDs(caller(types.TenantRoleContributor, ""), shares, targets),
		"callers without a user do not expand through org shares")
}
