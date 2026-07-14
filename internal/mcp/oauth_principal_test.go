package mcp

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/stretchr/testify/require"
)

type fakeOAuthRepo struct {
	clients map[string]*types.MCPOAuthClient
	tokens  map[string]*types.MCPOAuthToken
}

func newFakeOAuthRepo() *fakeOAuthRepo {
	return &fakeOAuthRepo{
		clients: map[string]*types.MCPOAuthClient{},
		tokens:  map[string]*types.MCPOAuthToken{},
	}
}

func fakeOAuthKey(tenantID uint64, principal types.Principal, serviceID string) string {
	return fmt.Sprintf("%d|%s|%s", tenantID, principal.Normalize().StorageID(), serviceID)
}

func (r *fakeOAuthRepo) GetClient(
	_ context.Context, tenantID uint64, serviceID string,
) (*types.MCPOAuthClient, error) {
	return r.clients[fmt.Sprintf("%d|%s", tenantID, serviceID)], nil
}

func (r *fakeOAuthRepo) SaveClient(_ context.Context, client *types.MCPOAuthClient) error {
	r.clients[fmt.Sprintf("%d|%s", client.TenantID, client.ServiceID)] = client
	return nil
}

func (r *fakeOAuthRepo) DeleteClient(_ context.Context, tenantID uint64, serviceID string) error {
	delete(r.clients, fmt.Sprintf("%d|%s", tenantID, serviceID))
	return nil
}

func (r *fakeOAuthRepo) GetToken(
	ctx context.Context, tenantID uint64, userID, serviceID string,
) (*types.MCPOAuthToken, error) {
	return r.GetTokenForPrincipal(ctx, tenantID, types.Principal{Type: types.PrincipalWebUser, ID: userID}, serviceID)
}

func (r *fakeOAuthRepo) GetTokenForPrincipal(
	_ context.Context, tenantID uint64, principal types.Principal, serviceID string,
) (*types.MCPOAuthToken, error) {
	return r.tokens[fakeOAuthKey(tenantID, principal, serviceID)], nil
}

func (r *fakeOAuthRepo) SaveToken(_ context.Context, token *types.MCPOAuthToken) error {
	return r.SaveTokenForPrincipal(context.Background(), token)
}

func (r *fakeOAuthRepo) SaveTokenForPrincipal(_ context.Context, token *types.MCPOAuthToken) error {
	principal := types.Principal{Type: token.PrincipalType, ID: token.PrincipalID}.Normalize()
	if !principal.Valid() {
		principal = types.Principal{Type: types.PrincipalWebUser, ID: token.UserID}.Normalize()
	}
	r.tokens[fakeOAuthKey(token.TenantID, principal, token.ServiceID)] = token
	return nil
}

func (r *fakeOAuthRepo) DeleteToken(
	ctx context.Context, tenantID uint64, userID, serviceID string,
) error {
	return r.DeleteTokenForPrincipal(ctx, tenantID, types.Principal{Type: types.PrincipalWebUser, ID: userID}, serviceID)
}

func (r *fakeOAuthRepo) DeleteTokenForPrincipal(
	_ context.Context, tenantID uint64, principal types.Principal, serviceID string,
) error {
	delete(r.tokens, fakeOAuthKey(tenantID, principal, serviceID))
	return nil
}

func TestDBTokenStoreUsesPrincipal(t *testing.T) {
	repo := newFakeOAuthRepo()
	principal := types.Principal{Type: types.PrincipalAPIExternalUser, ID: "7:external-42"}
	store := newDBTokenStore(repo, 7, principal, "svc-1")

	expiresAt := time.Now().Add(time.Hour).UTC()
	require.NoError(t, store.SaveToken(context.Background(), &transport.Token{
		AccessToken:  "access",
		RefreshToken: "refresh",
		TokenType:    "Bearer",
		ExpiresAt:    expiresAt,
	}))

	row, err := repo.GetTokenForPrincipal(context.Background(), 7, principal, "svc-1")
	require.NoError(t, err)
	require.NotNil(t, row)
	require.Equal(t, types.PrincipalAPIExternalUser, row.PrincipalType)
	require.Equal(t, "7:external-42", row.PrincipalID)
	require.Equal(t, principal.StorageID(), row.UserID)

	token, err := store.GetToken(context.Background())
	require.NoError(t, err)
	require.Equal(t, "access", token.AccessToken)
	require.Equal(t, "refresh", token.RefreshToken)
	require.Equal(t, expiresAt, token.ExpiresAt)
}

func TestOAuthCacheKeyUsesPrincipalForOAuthServices(t *testing.T) {
	service := &types.MCPService{
		ID:         "svc-1",
		AuthConfig: &types.MCPAuthConfig{AuthType: types.MCPAuthOAuth},
	}
	alice := types.Principal{Type: types.PrincipalAPIExternalUser, ID: "7:alice"}
	bob := types.Principal{Type: types.PrincipalAPIExternalUser, ID: "7:bob"}

	require.NotEqual(t, cacheKey(service, alice), cacheKey(service, bob))
	require.Contains(t, cacheKey(service, alice), alice.StorageID())

	service.AuthConfig.AuthType = types.MCPAuthAPIKey
	require.Equal(t, "svc-1", cacheKey(service, alice))
	require.Equal(t, cacheKey(service, alice), cacheKey(service, bob))
}
