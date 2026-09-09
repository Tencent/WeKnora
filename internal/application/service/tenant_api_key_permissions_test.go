package service

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/access"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

func TestAPIKeyServicePreservesGranularConfiguration(t *testing.T) {
	svc := NewTenantAPIKeyService(newFakeTenantAPIKeyRepo())
	ctx := context.Background()
	grants := types.APIKeyKBPermissions{"a": types.APIKeyKBRead, "b": types.APIKeyKBWrite}
	created, err := svc.CreateAPIKey(ctx, interfaces.TenantAPIKeyCreateRequest{
		TenantID: 42, Name: "granular", Capabilities: []string{"retrieve", "ingest"}, KnowledgeBasePermissions: grants,
	})
	require.NoError(t, err)
	grants["a"] = types.APIKeyKBManage
	require.Equal(t, types.APIKeyKBRead, created.APIKey.KnowledgeBasePermissions["a"])
	updated, err := svc.UpdateAPIKey(ctx, interfaces.TenantAPIKeyUpdateRequest{
		TenantID:                 42,
		APIKeyID:                 created.APIKey.ID,
		Name:                     "no KBs",
		Capabilities:             []string{"retrieve"},
		KnowledgeBasePermissions: types.APIKeyKBPermissions{},
	})
	require.NoError(t, err)
	require.NotNil(t, updated.KnowledgeBasePermissions)
	require.Empty(t, updated.KnowledgeBasePermissions)
	updated, err = svc.UpdateAPIKey(ctx, interfaces.TenantAPIKeyUpdateRequest{
		TenantID: 42, APIKeyID: created.APIKey.ID, Name: "full", FullAccess: true, KnowledgeBasePermissions: grants,
	})
	require.NoError(t, err)
	require.Nil(t, updated.KnowledgeBasePermissions)
	_, err = svc.CreateAPIKey(ctx, interfaces.TenantAPIKeyCreateRequest{
		ScopeType:                types.APIKeyScopePlatform,
		Name:                     "platform",
		Capabilities:             []string{"retrieve"},
		KnowledgeBasePermissions: grants,
	})
	require.Error(t, err)
}

func TestGranularKeySharedAgentCannotBypassRevocationInBatchOrSearch(t *testing.T) {
	shares := &fakeKBShareService{allowedKBs: map[string]bool{"shared": true}}
	svc, db := newKnowledgeSharedAccessService(t, shares)
	require.NoError(t, db.AutoMigrate(&types.KnowledgeBase{}))
	require.NoError(
		t,
		db.Create(
			&types.KnowledgeBase{ID: "shared", TenantID: 99, Name: "Shared", Type: types.KnowledgeBaseTypeDocument},
		).Error,
	)
	seedKnowledge(
		t,
		db,
		&types.Knowledge{ID: "doc", TenantID: 99, KnowledgeBaseID: "shared", Title: "needle", Type: "document"},
	)
	ctx := types.WithCaller(
		context.Background(),
		types.Caller{TenantID: 42, UserID: "machine", Role: types.TenantRoleViewer},
	)
	ctx = types.WithTenantAPIKeyScope(
		ctx,
		types.TenantAPIKeyScope{KnowledgeBasePermissions: types.APIKeyKBPermissions{"shared": types.APIKeyKBRead}},
	)
	ctx = access.WithSharedAgent(
		ctx,
		&types.CustomAgent{ID: "agent", TenantID: 99, Config: types.CustomAgentConfig{KBSelectionMode: "all"}},
	)
	for _, allowed := range []bool{true, false} {
		shares.allowedKBs["shared"] = allowed
		rows, err := svc.GetKnowledgeBatch(ctx, 99, []string{"doc"})
		require.NoError(t, err)
		require.Equal(t, allowed, len(rows) == 1)
		rows, _, _, err = svc.SearchKnowledgeForScopes(
			ctx,
			[]types.KnowledgeSearchScope{{TenantID: 99, KBID: "shared"}},
			"needle",
			0,
			20,
			nil,
		)
		require.NoError(t, err)
		require.Equal(t, allowed, len(rows) == 1)
	}
}
