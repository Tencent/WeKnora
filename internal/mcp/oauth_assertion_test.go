package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	secutils "github.com/Tencent/WeKnora/internal/utils"
	"github.com/stretchr/testify/require"
)

// assertionTestServer is an authorization server that records the requests it
// receives and answers token requests with reply.
type assertionTestServer struct {
	*httptest.Server
	mu            sync.Mutex
	registrations int
	tokenForms    []url.Values
	reply         func(w http.ResponseWriter)
}

func newAssertionTestServer(t *testing.T) *assertionTestServer {
	t.Helper()
	t.Setenv("SSRF_WHITELIST", "127.0.0.1")
	secutils.ResetSSRFWhitelistForTest()
	t.Cleanup(secutils.ResetSSRFWhitelistForTest)

	s := &assertionTestServer{reply: func(w http.ResponseWriter) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "issued-access", "token_type": "Bearer", "expires_in": 3600,
		})
	}}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/.well-known/oauth-authorization-server":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"issuer":                 s.URL,
				"authorization_endpoint": s.URL + "/authorize",
				"token_endpoint":         s.URL + "/token",
				"registration_endpoint":  s.URL + "/register",
			})
		case "/register":
			s.mu.Lock()
			s.registrations++
			s.mu.Unlock()
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]any{"client_id": "registered-client"})
		case "/token":
			_ = r.ParseForm()
			s.mu.Lock()
			s.tokenForms = append(s.tokenForms, r.PostForm)
			reply := s.reply
			s.mu.Unlock()
			reply(w)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(s.Close)
	return s
}

func (s *assertionTestServer) service(scopes ...string) *types.MCPService {
	serviceURL := s.URL + "/mcp"
	return &types.MCPService{
		ID:            "svc-1",
		TenantID:      7,
		TransportType: types.MCPTransportHTTPStreamable,
		URL:           &serviceURL,
		AuthConfig: &types.MCPAuthConfig{
			AuthType:              types.MCPAuthOAuth,
			Scopes:                scopes,
			AuthServerMetadataURL: s.URL + "/.well-known/oauth-authorization-server",
		},
	}
}

func (s *assertionTestServer) lastTokenForm(t *testing.T) url.Values {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	require.NotEmpty(t, s.tokenForms)
	return s.tokenForms[len(s.tokenForms)-1]
}

var assertionTestPrincipal = types.Principal{Type: types.PrincipalAPIExternalUser, ID: "7:user-1"}

func TestAuthorizeWithAssertionSendsAJWTBearerGrantAndStoresTheToken(t *testing.T) {
	server := newAssertionTestServer(t)
	service := server.service("tools.read", "tools.call")
	repo := newFakeOAuthRepo()
	require.NoError(t, repo.SaveClient(context.Background(), &types.MCPOAuthClient{
		TenantID: 7, ServiceID: service.ID, ClientID: "known-client", ClientSecret: "known-secret",
	}))
	manager := NewOAuthManager(repo, fakeMCPServiceRepo{service: service}, nil)

	before := time.Now()
	err := manager.AuthorizeWithAssertion(
		context.Background(), service, 7, assertionTestPrincipal, "signed.jwt.value",
	)
	require.NoError(t, err)

	form := server.lastTokenForm(t)
	require.Equal(t, "urn:ietf:params:oauth:grant-type:jwt-bearer", form.Get("grant_type"))
	require.Equal(t, "signed.jwt.value", form.Get("assertion"))
	require.Equal(t, "known-client", form.Get("client_id"))
	require.Equal(t, "known-secret", form.Get("client_secret"))
	require.Equal(t, "tools.read tools.call", form.Get("scope"))
	require.NotContains(t, form, "resource", "the assertion names the resource")
	require.Zero(t, server.registrations, "a stored client is reused")

	token, err := repo.GetTokenForPrincipal(context.Background(), 7, assertionTestPrincipal, service.ID)
	require.NoError(t, err)
	require.NotNil(t, token)
	require.Equal(t, "issued-access", token.AccessToken)
	require.WithinDuration(t, before.Add(time.Hour), token.ExpiresAt, time.Minute)

	status, err := manager.AuthorizationStatus(context.Background(), 7, assertionTestPrincipal, service.ID)
	require.NoError(t, err)
	require.True(t, status.Authorized)
}

func TestAuthorizeWithAssertionOmitsOptionalFields(t *testing.T) {
	server := newAssertionTestServer(t)
	service := server.service()
	repo := newFakeOAuthRepo()
	require.NoError(t, repo.SaveClient(context.Background(), &types.MCPOAuthClient{
		TenantID: 7, ServiceID: service.ID, ClientID: "public-client",
	}))
	manager := NewOAuthManager(repo, fakeMCPServiceRepo{service: service}, nil)

	require.NoError(t, manager.AuthorizeWithAssertion(
		context.Background(), service, 7, assertionTestPrincipal, "signed.jwt.value",
	))

	form := server.lastTokenForm(t)
	require.NotContains(t, form, "client_secret")
	require.NotContains(t, form, "scope")
}

// The grant is sent with a client the service already has. It does not run
// dynamic registration, so without one nothing reaches the server.
func TestAuthorizeWithAssertionNeedsARegisteredClient(t *testing.T) {
	server := newAssertionTestServer(t)
	service := server.service()
	repo := newFakeOAuthRepo()
	manager := NewOAuthManager(repo, fakeMCPServiceRepo{service: service}, nil)

	err := manager.AuthorizeWithAssertion(context.Background(), service, 7, assertionTestPrincipal, "signed.jwt.value")

	require.ErrorIs(t, err, ErrOAuthClientNotRegistered)
	require.Zero(t, server.registrations)
	require.Empty(t, server.tokenForms)
}

func TestAuthorizeWithAssertionClassifiesTokenEndpointFailures(t *testing.T) {
	cases := []struct {
		name       string
		status     int
		body       map[string]any
		rejected   bool
		rejectCode string
	}{
		{"invalid grant", http.StatusBadRequest, map[string]any{"error": "invalid_grant"}, true, "invalid_grant"},
		{"invalid client", http.StatusUnauthorized, map[string]any{"error": "invalid_client"}, true, "invalid_client"},
		{
			"grant not allowed for the client", http.StatusBadRequest,
			map[string]any{"error": "unauthorized_client"},
			true, "unauthorized_client",
		},
		{"error in a 200 body", http.StatusOK, map[string]any{"error": "invalid_grant"}, true, "invalid_grant"},
		{"4xx without an OAuth body", http.StatusForbidden, map[string]any{"message": "no"}, true, ""},
		{
			"5xx carrying an OAuth error", http.StatusServiceUnavailable,
			map[string]any{"error": "temporarily_unavailable"},
			false, "",
		},
		{"empty access token", http.StatusOK, map[string]any{"token_type": "Bearer"}, false, ""},
		{"rate limited", http.StatusTooManyRequests, map[string]any{"error": "slow_down"}, false, ""},
		{"request timeout", http.StatusRequestTimeout, map[string]any{}, false, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := newAssertionTestServer(t)
			server.reply = func(w http.ResponseWriter) {
				w.WriteHeader(tc.status)
				_ = json.NewEncoder(w).Encode(tc.body)
			}
			service := server.service()
			repo := newFakeOAuthRepo()
			require.NoError(t, repo.SaveClient(context.Background(), &types.MCPOAuthClient{
				TenantID: 7, ServiceID: service.ID, ClientID: "known-client",
			}))
			manager := NewOAuthManager(repo, fakeMCPServiceRepo{service: service}, nil)

			err := manager.AuthorizeWithAssertion(
				context.Background(), service, 7, assertionTestPrincipal, "signed.jwt.value",
			)

			require.Error(t, err)
			var rejected *OAuthGrantRejectedError
			require.Equal(t, tc.rejected, errors.As(err, &rejected), err.Error())
			if tc.rejected {
				require.Equal(t, tc.rejectCode, rejected.Code)
			}
			require.NotContains(t, err.Error(), "signed.jwt.value")
			token, getErr := repo.GetTokenForPrincipal(context.Background(), 7, assertionTestPrincipal, service.ID)
			require.NoError(t, getErr)
			require.Nil(t, token, "a failed grant stores nothing")
		})
	}
}

// The token request is detached from the caller: an authorization server may
// accept an assertion only once, so a token it has issued is kept even when
// the caller has already gone away.
func TestAuthorizeWithAssertionOutlivesTheCaller(t *testing.T) {
	server := newAssertionTestServer(t)
	service := server.service()
	repo := newFakeOAuthRepo()
	require.NoError(t, repo.SaveClient(context.Background(), &types.MCPOAuthClient{
		TenantID: 7, ServiceID: service.ID, ClientID: "known-client",
	}))
	manager := NewOAuthManager(repo, fakeMCPServiceRepo{service: service}, nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	require.NoError(t, manager.AuthorizeWithAssertion(ctx, service, 7, assertionTestPrincipal, "signed.jwt.value"))

	token, err := repo.GetTokenForPrincipal(context.Background(), 7, assertionTestPrincipal, service.ID)
	require.NoError(t, err)
	require.NotNil(t, token)
}

func TestAuthorizeWithAssertionKeepsPrincipalsApart(t *testing.T) {
	server := newAssertionTestServer(t)
	service := server.service()
	repo := newFakeOAuthRepo()
	require.NoError(t, repo.SaveClient(context.Background(), &types.MCPOAuthClient{
		TenantID: 7, ServiceID: service.ID, ClientID: "known-client",
	}))
	manager := NewOAuthManager(repo, fakeMCPServiceRepo{service: service}, nil)
	other := types.Principal{Type: types.PrincipalAPIExternalUser, ID: "7:user-2"}

	require.NoError(t, manager.AuthorizeWithAssertion(
		context.Background(), service, 7, assertionTestPrincipal, "signed.jwt.value",
	))

	status, err := manager.AuthorizationStatus(context.Background(), 7, other, service.ID)
	require.NoError(t, err)
	require.False(t, status.Authorized)
}

func TestAuthorizeWithAssertionRequiresAnOAuthService(t *testing.T) {
	server := newAssertionTestServer(t)
	service := server.service()
	service.AuthConfig.AuthType = types.MCPAuthAPIKey
	manager := NewOAuthManager(newFakeOAuthRepo(), fakeMCPServiceRepo{service: service}, nil)

	err := manager.AuthorizeWithAssertion(
		context.Background(), service, 7, assertionTestPrincipal, "signed.jwt.value",
	)

	require.Error(t, err)
	require.Empty(t, server.tokenForms)
}
