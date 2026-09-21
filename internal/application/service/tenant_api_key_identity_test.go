package service

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

func TestAPIKeyIdentityCreationRotationAndUpdate(t *testing.T) {
	ctx := context.Background()
	repo := newFakeTenantAPIKeyRepo()
	svc := NewTenantAPIKeyService(repo)
	secret := "application-signing-secret"
	input := interfaces.TenantAPIKeyCreateRequest{
		TenantID: 7, Name: "app", FullAccess: true,
		APIPrincipalConfig: &types.APIPrincipalConfigInput{
			Mode:       types.APIPrincipalModeSignedToken,
			HMACSecret: &secret,
		},
	}
	first, err := svc.CreateAPIKey(ctx, input)
	require.NoError(t, err)
	second, err := svc.CreateAPIKey(ctx, input)
	require.NoError(t, err)
	require.NotEmpty(t, first.APIKey.IdentityNamespace)
	require.NotEqual(t, first.APIKey.IdentityNamespace, second.APIKey.IdentityNamespace)
	rotation, err := svc.CreateAPIKey(ctx, interfaces.TenantAPIKeyCreateRequest{
		TenantID:            7,
		Name:                "rotation",
		Capabilities:        []string{"chat"},
		IdentitySourceKeyID: first.APIKey.ID,
	})
	require.NoError(t, err)
	require.Equal(t, first.APIKey.IdentityNamespace, rotation.APIKey.IdentityNamespace)
	require.Equal(t, secret, rotation.APIKey.APIPrincipalConfig.HMACSecret)
	require.False(t, rotation.APIKey.FullAccess)
	require.Equal(t, types.StringArray{"chat"}, rotation.APIKey.Capabilities)
	updated, err := svc.UpdateAPIKey(ctx, interfaces.TenantAPIKeyUpdateRequest{
		TenantID: 7, APIKeyID: rotation.APIKey.ID, Name: "updated", FullAccess: true,
		APIPrincipalConfig: &types.APIPrincipalConfigInput{
			Mode:                types.APIPrincipalModeSignedToken,
			RequireDirectHeader: true,
		},
	})
	require.NoError(t, err)
	require.Equal(t, secret, updated.APIPrincipalConfig.HMACSecret)
	require.Equal(t, first.APIKey.IdentityNamespace, updated.IdentityNamespace)
	require.False(t, first.APIKey.APIPrincipalConfig.RequireDirectHeader, "copied policies must not alias")
	updated, err = svc.UpdateAPIKey(ctx, interfaces.TenantAPIKeyUpdateRequest{
		TenantID:   7,
		APIKeyID:   rotation.APIKey.ID,
		Name:       "scope only",
		FullAccess: true,
	})
	require.NoError(t, err)
	require.Equal(t, secret, updated.APIPrincipalConfig.HMACSecret)
	require.Equal(t, first.APIKey.IdentityNamespace, updated.IdentityNamespace)
	_, err = svc.CreateAPIKey(ctx, interfaces.TenantAPIKeyCreateRequest{
		TenantID:            8,
		Name:                "foreign",
		IdentitySourceKeyID: first.APIKey.ID,
	})
	require.Error(t, err)
	require.NoError(t, svc.RevokeAPIKey(ctx, 7, first.APIKey.ID))
	_, err = svc.CreateAPIKey(ctx, interfaces.TenantAPIKeyCreateRequest{
		TenantID:            7,
		Name:                "revoked",
		IdentitySourceKeyID: first.APIKey.ID,
	})
	require.Error(t, err)
}

func TestAPIKeyIdentityValidationAndLegacyRotation(t *testing.T) {
	ctx := context.Background()
	repo := newFakeTenantAPIKeyRepo()
	svc := NewTenantAPIKeyService(repo)
	for _, mode := range []types.APIPrincipalMode{"invalid", types.APIPrincipalModeSignedToken} {
		_, err := svc.CreateAPIKey(ctx, interfaces.TenantAPIKeyCreateRequest{
			TenantID:           7,
			Name:               "bad",
			APIPrincipalConfig: &types.APIPrincipalConfigInput{Mode: mode},
		})
		require.Error(t, err)
	}
	tid := uint64(7)
	legacy := &types.TenantAPIKey{
		TenantID:           &tid,
		Name:               "legacy",
		KeyHash:            "legacy",
		APIPrincipalConfig: &types.APIPrincipalConfig{Mode: types.APIPrincipalModeTenant},
	}
	require.NoError(t, repo.CreateAPIKey(ctx, legacy))
	// Legacy empty mode also means tenant and must survive a no-op edit.
	legacy.APIPrincipalConfig.Mode = ""
	repo.byHash["legacy"].APIPrincipalConfig.Mode = ""
	unchanged, err := svc.UpdateAPIKey(ctx, interfaces.TenantAPIKeyUpdateRequest{
		TenantID: tid, APIKeyID: legacy.ID, Name: "legacy", FullAccess: true,
		APIPrincipalConfig: &types.APIPrincipalConfigInput{Mode: types.APIPrincipalModeTenant},
	})
	require.NoError(t, err)
	require.Empty(t, unchanged.IdentityNamespace)
	rotation, err := svc.CreateAPIKey(ctx, interfaces.TenantAPIKeyCreateRequest{
		TenantID:            tid,
		Name:                "rotation",
		IdentitySourceKeyID: legacy.ID,
	})
	require.NoError(t, err)
	require.Empty(t, rotation.APIKey.IdentityNamespace)
	require.Equal(t, legacy.ID, rotation.APIKey.LegacySessionKeyID)
	updated, err := svc.UpdateAPIKey(ctx, interfaces.TenantAPIKeyUpdateRequest{
		TenantID: tid, APIKeyID: legacy.ID, Name: "legacy", FullAccess: true,
		APIPrincipalConfig: &types.APIPrincipalConfigInput{Mode: types.APIPrincipalModeDirect},
	})
	require.NoError(t, err)
	require.NotEmpty(t, updated.IdentityNamespace, "changing legacy trust must detach its shared identities")
}
