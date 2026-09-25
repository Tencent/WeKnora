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
	apiKey := func(scope types.TenantAPIKeyScope) context.Context {
		return types.WithTenantAPIKeyScope(caller(types.TenantRoleViewer, "system-42"), scope)
	}

	require.Equal(t, []string{"own", "shared-editor"},
		kbWritableIDs(caller(types.TenantRoleContributor, "u"), shares, targets, true))
	require.Equal(t, []string{"own"}, kbWritableIDs(caller(types.TenantRoleContributor, ""), shares, targets, true),
		"callers without a user do not expand through org shares")

	// A tenant Viewer (also the IM / embed / MCP endpoint principals) is
	// read-only, like on the HTTP write routes; with RBAC enforcement off the
	// role check only logs, but a share never grants a Viewer more than read.
	require.Empty(t, kbWritableIDs(caller(types.TenantRoleViewer, "u"), shares, targets, true))
	require.Equal(t, []string{"own"}, kbWritableIDs(caller(types.TenantRoleViewer, "u"), shares, targets, false))

	// Scoped API keys write only with the ingest capability.
	chatOnly := types.TenantAPIKeyScope{Capabilities: types.StringArray{string(types.APIKeyCapabilityChat)}}
	require.Empty(t, kbWritableIDs(apiKey(chatOnly), shares, targets, true))
	ingest := types.TenantAPIKeyScope{Capabilities: types.StringArray{string(types.APIKeyCapabilityIngest)}}
	require.Equal(t, []string{"own"}, kbWritableIDs(apiKey(ingest), shares, targets, true))
}
