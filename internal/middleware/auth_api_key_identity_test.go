package middleware

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/require"
)

func TestAPIKeyIdentityIsolationAndLegacyCompatibility(t *testing.T) {
	ctx := context.Background()
	tenant := &types.Tenant{
		ID: 7,
		APIPrincipalConfig: &types.APIPrincipalConfig{
			Mode:       types.APIPrincipalModeSignedToken,
			HMACSecret: "workspace-secret",
		},
	}
	direct := &types.TenantAPIKey{
		IdentityNamespace: "app-a",
		APIPrincipalConfig: &types.APIPrincipalConfig{
			Mode:                types.APIPrincipalModeDirect,
			RequireDirectHeader: true,
		},
	}
	header := http.Header{"X-External-User-Id": {"user-1"}}
	a, err := resolveAPIPrincipal(ctx, tenant, header, direct)
	require.NoError(t, err)
	require.Equal(t, types.PrincipalAPIApplicationUser, a.Type)
	other := *direct
	other.IdentityNamespace = "app-b"
	b, err := resolveAPIPrincipal(ctx, tenant, header, &other)
	require.NoError(t, err)
	require.NotEqual(t, a.StorageID(), b.StorageID())
	rotated := *direct
	rotated.ID = 999
	again, err := resolveAPIPrincipal(ctx, tenant, header, &rotated)
	require.NoError(t, err)
	require.Equal(t, a, again)
	_, err = resolveAPIPrincipal(ctx, tenant, http.Header{}, direct)
	require.Error(t, err)
	optional := *direct.APIPrincipalConfig
	optional.RequireDirectHeader = false
	direct.APIPrincipalConfig = &optional
	anon, err := resolveAPIPrincipal(ctx, tenant, http.Header{}, direct)
	require.NoError(t, err)
	require.Equal(t, types.PrincipalAPIApplication, anon.Type)
	require.NotEqual(t, anon, a)
	legacy := &types.TenantAPIKey{APIPrincipalConfig: &optional}
	old, err := resolveAPIPrincipal(ctx, tenant, header, legacy)
	require.NoError(t, err)
	require.Equal(t, types.Principal{Type: types.PrincipalAPIExternalUser, ID: "7:user-1"}, old)
	// A legacy caller cannot forge a namespaced principal by choosing its ID.
	header.Set("X-External-User-ID", "app-a:direct_header:user-1")
	forged, err := resolveAPIPrincipal(ctx, tenant, header, legacy)
	require.NoError(t, err)
	require.NotEqual(t, a.StorageID(), forged.StorageID())
	broken := &types.TenantAPIKey{IdentityNamespace: "app-a"}
	_, err = resolveAPIPrincipal(ctx, tenant, header, broken)
	require.Error(t, err)
}

func TestAPIKeySignedIdentityBindsApplicationAndTrustMode(t *testing.T) {
	tenant := &types.Tenant{ID: 7}
	secret := "same-secret-on-two-apps"
	key := &types.TenantAPIKey{
		IdentityNamespace: "app-a",
		APIPrincipalConfig: &types.APIPrincipalConfig{
			Mode:       types.APIPrincipalModeSignedToken,
			HMACSecret: secret,
		},
	}
	claims := jwt.MapClaims{
		"sub":                "user-1",
		"tenant_id":          "7",
		"aud":                "weknora",
		"exp":                time.Now().Add(time.Hour).Unix(),
		"identity_namespace": "app-a",
	}
	header := http.Header{}
	header.Set("X-External-User-Token", signedExternalUserToken(t, secret, claims))
	signed, err := resolveAPIPrincipal(context.Background(), tenant, header, key)
	require.NoError(t, err)
	other := *key
	other.IdentityNamespace = "app-b"
	_, err = resolveAPIPrincipal(context.Background(), tenant, header, &other)
	require.Error(t, err, "shared signing secrets must not permit cross-application JWT replay")
	other.IdentityNamespace = ""
	_, err = resolveAPIPrincipal(context.Background(), tenant, header, &other)
	require.Error(t, err, "namespaced JWT must not be usable by legacy keys")
	direct := *key
	direct.APIPrincipalConfig = &types.APIPrincipalConfig{Mode: types.APIPrincipalModeDirect}
	header.Set("X-External-User-ID", "user-1")
	unsigned, err := resolveAPIPrincipal(context.Background(), tenant, header, &direct)
	require.NoError(t, err)
	require.NotEqual(t, signed.StorageID(), unsigned.StorageID())
	delete(claims, "identity_namespace")
	header.Set("X-External-User-Token", signedExternalUserToken(t, secret, claims))
	_, err = resolveAPIPrincipal(context.Background(), tenant, header, key)
	require.Error(t, err)
	_, err = resolveAPIPrincipal(context.Background(), tenant, header, &other)
	require.NoError(t, err, "old JWT format remains valid for migrated keys")
}
