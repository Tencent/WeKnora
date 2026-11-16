package mcp

import (
	"context"
	"net/url"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func preRegisteredService(server *assertionTestServer) *types.MCPService {
	service := server.service()
	service.AuthConfig.ClientID = "pre-registered"
	service.AuthConfig.ClientSecret = "pre-registered-secret"
	return service
}

// A pre-registered client replaces dynamic registration in the
// authorization code flow: the authorization URL and the code exchange carry
// it, and nothing is registered or stored.
func TestAuthorizationCodeFlowUsesThePreRegisteredClient(t *testing.T) {
	server := newAssertionTestServer(t)
	service := preRegisteredService(server)
	repo := newFakeOAuthRepo()
	manager := NewOAuthManager(repo, fakeMCPServiceRepo{service: service}, nil)
	principal := types.Principal{Type: types.PrincipalWebUser, ID: "42"}

	authURL, state, err := manager.StartAuthorization(
		context.Background(), service, 7, principal, "http://localhost/callback", "/",
	)
	require.NoError(t, err)
	parsed, err := url.Parse(authURL)
	require.NoError(t, err)
	require.Equal(t, "pre-registered", parsed.Query().Get("client_id"))

	_, err = manager.CompleteAuthorization(context.Background(), state, "code")
	require.NoError(t, err)
	form := server.lastTokenForm(t)
	require.Equal(t, "authorization_code", form.Get("grant_type"))
	require.Equal(t, "pre-registered", form.Get("client_id"))
	require.Equal(t, "pre-registered-secret", form.Get("client_secret"))
	require.Zero(t, server.registrations)
	stored, err := repo.GetClient(context.Background(), 7, service.ID)
	require.NoError(t, err)
	require.Nil(t, stored, "a pre-registered client is not copied into the registration table")
}

// The pre-registered client wins over a registration stored earlier.
func TestAssertionGrantUsesThePreRegisteredClient(t *testing.T) {
	server := newAssertionTestServer(t)
	service := preRegisteredService(server)
	repo := newFakeOAuthRepo()
	require.NoError(t, repo.SaveClient(context.Background(), &types.MCPOAuthClient{
		TenantID: 7, ServiceID: service.ID, ClientID: "registered-earlier",
	}))
	manager := NewOAuthManager(repo, fakeMCPServiceRepo{service: service}, nil)

	require.NoError(t, manager.AuthorizeWithAssertion(
		context.Background(), service, 7, assertionTestPrincipal, "signed.jwt.value",
	))

	form := server.lastTokenForm(t)
	require.Equal(t, "pre-registered", form.Get("client_id"))
	require.Equal(t, "pre-registered-secret", form.Get("client_secret"))
	require.Zero(t, server.registrations)
}

// A pre-registered client is enough: no registration has to be stored first.
func TestAssertionGrantWithAPreRegisteredClientNeedsNoStoredRegistration(t *testing.T) {
	server := newAssertionTestServer(t)
	service := preRegisteredService(server)
	manager := NewOAuthManager(newFakeOAuthRepo(), fakeMCPServiceRepo{service: service}, nil)

	require.NoError(t, manager.AuthorizeWithAssertion(
		context.Background(), service, 7, assertionTestPrincipal, "signed.jwt.value",
	))
	require.Zero(t, server.registrations)
}

// Runtime connections, and so their token refreshes, use the pre-registered
// client too.
func TestRuntimeOAuthConfigPrefersThePreRegisteredClient(t *testing.T) {
	server := newAssertionTestServer(t)
	service := preRegisteredService(server)
	repo := newFakeOAuthRepo()
	require.NoError(t, repo.SaveClient(context.Background(), &types.MCPOAuthClient{
		TenantID: 7, ServiceID: service.ID, ClientID: "registered-earlier", ClientSecret: "old-secret",
	}))

	cfg, useOAuth, err := buildOAuthConfig(&ClientConfig{
		Service: service, TenantID: 7, Principal: assertionTestPrincipal, OAuthRepo: repo,
	}, nil)

	require.NoError(t, err)
	require.True(t, useOAuth)
	require.Equal(t, "pre-registered", cfg.ClientID)
	require.Equal(t, "pre-registered-secret", cfg.ClientSecret)
}

func TestRuntimeOAuthConfigFallsBackToTheStoredRegistration(t *testing.T) {
	server := newAssertionTestServer(t)
	service := server.service()
	repo := newFakeOAuthRepo()
	require.NoError(t, repo.SaveClient(context.Background(), &types.MCPOAuthClient{
		TenantID: 7, ServiceID: service.ID, ClientID: "registered-earlier",
	}))

	cfg, _, err := buildOAuthConfig(&ClientConfig{
		Service: service, TenantID: 7, Principal: assertionTestPrincipal, OAuthRepo: repo,
	}, nil)

	require.NoError(t, err)
	require.Equal(t, "registered-earlier", cfg.ClientID)
}
