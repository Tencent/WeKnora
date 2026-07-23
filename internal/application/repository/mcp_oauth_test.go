package repository

import (
	"context"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func newMCPOAuthTestRepo(t *testing.T) *mcpOAuthRepository {
	t.Helper()

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&types.MCPOAuthClient{}, &types.MCPOAuthToken{}))

	return NewMCPOAuthRepository(db).(*mcpOAuthRepository)
}

func TestMCPOAuthRepositoryTokenForPrincipalIsolated(t *testing.T) {
	repo := newMCPOAuthTestRepo(t)
	ctx := context.Background()
	expiresAt := time.Now().Add(time.Hour).UTC()

	webPrincipal := types.Principal{Type: types.PrincipalWebUser, ID: "u1"}
	apiPrincipal := types.Principal{Type: types.PrincipalAPIExternalUser, ID: "7:external-u1"}

	require.NoError(t, repo.SaveTokenForPrincipal(ctx, &types.MCPOAuthToken{
		TenantID:      7,
		UserID:        webPrincipal.StorageID(),
		PrincipalType: webPrincipal.Type,
		PrincipalID:   webPrincipal.ID,
		ServiceID:     "svc1",
		AccessToken:   "web-token",
		RefreshToken:  "web-refresh",
		TokenType:     "Bearer",
		ExpiresAt:     expiresAt,
	}))
	require.NoError(t, repo.SaveTokenForPrincipal(ctx, &types.MCPOAuthToken{
		TenantID:      7,
		UserID:        apiPrincipal.StorageID(),
		PrincipalType: apiPrincipal.Type,
		PrincipalID:   apiPrincipal.ID,
		ServiceID:     "svc1",
		AccessToken:   "api-token",
		RefreshToken:  "api-refresh",
		TokenType:     "Bearer",
		ExpiresAt:     expiresAt,
	}))

	webToken, err := repo.GetTokenForPrincipal(ctx, 7, webPrincipal, "svc1")
	require.NoError(t, err)
	require.Equal(t, "web-token", webToken.AccessToken)

	apiToken, err := repo.GetTokenForPrincipal(ctx, 7, apiPrincipal, "svc1")
	require.NoError(t, err)
	require.Equal(t, "api-token", apiToken.AccessToken)
}

func TestMCPOAuthRepositoryLegacyUserTokenUsesWebPrincipal(t *testing.T) {
	repo := newMCPOAuthTestRepo(t)
	ctx := context.Background()

	require.NoError(t, repo.SaveToken(ctx, &types.MCPOAuthToken{
		TenantID:     7,
		UserID:       "u1",
		ServiceID:    "svc1",
		AccessToken:  "legacy-token",
		RefreshToken: "legacy-refresh",
		TokenType:    "Bearer",
		ExpiresAt:    time.Now().Add(time.Hour).UTC(),
	}))

	token, err := repo.GetTokenForPrincipal(ctx, 7, types.Principal{Type: types.PrincipalWebUser, ID: "u1"}, "svc1")
	require.NoError(t, err)
	require.Equal(t, "legacy-token", token.AccessToken)
}

func TestMCPOAuthRepositoryRefreshLeaseHasSingleOwner(t *testing.T) {
	repo := newMCPOAuthTestRepo(t)
	ctx := context.Background()
	principal := types.Principal{Type: types.PrincipalWebUser, ID: "u1"}
	require.NoError(t, repo.SaveTokenForPrincipal(ctx, &types.MCPOAuthToken{
		TenantID:      7,
		UserID:        principal.StorageID(),
		PrincipalType: principal.Type,
		PrincipalID:   principal.ID,
		ServiceID:     "svc1",
		AccessToken:   "access",
		RefreshToken:  "refresh",
		ExpiresAt:     time.Now().Add(-time.Minute),
	}))

	first, err := repo.TryAcquireTokenRefreshLease(
		ctx, 7, principal, "svc1", "lease-1", time.Now().Add(time.Minute),
	)
	require.NoError(t, err)
	require.True(t, first)

	second, err := repo.TryAcquireTokenRefreshLease(
		ctx, 7, principal, "svc1", "lease-2", time.Now().Add(time.Minute),
	)
	require.NoError(t, err)
	require.False(t, second)

	// A non-owner cannot release the current owner's lease.
	require.NoError(t, repo.ReleaseTokenRefreshLease(ctx, 7, principal, "svc1", "lease-2"))
	second, err = repo.TryAcquireTokenRefreshLease(
		ctx, 7, principal, "svc1", "lease-2", time.Now().Add(time.Minute),
	)
	require.NoError(t, err)
	require.False(t, second)

	require.NoError(t, repo.ReleaseTokenRefreshLease(ctx, 7, principal, "svc1", "lease-1"))
	second, err = repo.TryAcquireTokenRefreshLease(
		ctx, 7, principal, "svc1", "lease-2", time.Now().Add(time.Minute),
	)
	require.NoError(t, err)
	require.True(t, second)
}
