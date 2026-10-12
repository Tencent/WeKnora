package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/mcp"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	secutils "github.com/Tencent/WeKnora/internal/utils"
	"github.com/gin-gonic/gin"
	sdkserver "github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/require"
)

const mcpOAuthTestTenant = uint64(7)

type mcpOAuthTestRepo struct {
	interfaces.MCPOAuthRepository
	mu      sync.Mutex
	clients map[string]*types.MCPOAuthClient
	tokens  map[string]*types.MCPOAuthToken
}

func mcpOAuthTestTokenKey(tenantID uint64, principal types.Principal, serviceID string) string {
	return fmt.Sprintf("%d|%s|%s", tenantID, principal.StorageID(), serviceID)
}

func (r *mcpOAuthTestRepo) GetClient(
	_ context.Context, tenantID uint64, serviceID string,
) (*types.MCPOAuthClient, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.clients[fmt.Sprintf("%d|%s", tenantID, serviceID)], nil
}

func (r *mcpOAuthTestRepo) SaveClient(_ context.Context, client *types.MCPOAuthClient) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.clients[fmt.Sprintf("%d|%s", client.TenantID, client.ServiceID)] = client
	return nil
}

func (r *mcpOAuthTestRepo) GetTokenForPrincipal(
	_ context.Context, tenantID uint64, principal types.Principal, serviceID string,
) (*types.MCPOAuthToken, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.tokens[mcpOAuthTestTokenKey(tenantID, principal, serviceID)], nil
}

func (r *mcpOAuthTestRepo) SaveTokenForPrincipal(_ context.Context, token *types.MCPOAuthToken) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	principal := types.Principal{Type: token.PrincipalType, ID: token.PrincipalID}
	r.tokens[mcpOAuthTestTokenKey(token.TenantID, principal, token.ServiceID)] = token
	return nil
}

func (r *mcpOAuthTestRepo) DeleteTokenForPrincipal(
	_ context.Context, tenantID uint64, principal types.Principal, serviceID string,
) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.tokens, mcpOAuthTestTokenKey(tenantID, principal, serviceID))
	return nil
}

type mcpOAuthTestServiceRepo struct {
	interfaces.MCPServiceRepository
	service *types.MCPService
}

func (r mcpOAuthTestServiceRepo) GetByID(_ context.Context, _ uint64, id string) (*types.MCPService, error) {
	if r.service.ID == id {
		return r.service, nil
	}
	return nil, nil
}

// mcpOAuthTestEnv is an OAuth MCP service whose authorization server and MCP
// server run in process, with a handler wired to a real connection manager.
type mcpOAuthTestEnv struct {
	service *types.MCPService
	repo    *mcpOAuthTestRepo
	oauth   *mcp.OAuthManager
	manager *mcp.MCPManager
	handler *MCPOAuthHandler
}

func newMCPOAuthTestEnv(t *testing.T) *mcpOAuthTestEnv {
	t.Helper()
	t.Setenv("SSRF_WHITELIST", "127.0.0.1")
	secutils.ResetSSRFWhitelistForTest()
	t.Cleanup(secutils.ResetSSRFWhitelistForTest)

	mux := http.NewServeMux()
	var serverURL string
	mux.HandleFunc("/.well-known/oauth-authorization-server", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer":                 serverURL,
			"authorization_endpoint": serverURL + "/authorize",
			"token_endpoint":         serverURL + "/token",
		})
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "access-new",
			"token_type":   "Bearer",
			"expires_in":   3600,
		})
	})
	mux.Handle("/mcp", sdkserver.NewStreamableHTTPServer(
		sdkserver.NewMCPServer("test", "1"), sdkserver.WithStateLess(true),
	))
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	serverURL = server.URL

	serviceURL := server.URL + "/mcp"
	service := &types.MCPService{
		ID:            "svc-1",
		TenantID:      mcpOAuthTestTenant,
		Enabled:       true,
		TransportType: types.MCPTransportHTTPStreamable,
		URL:           &serviceURL,
		AuthConfig: &types.MCPAuthConfig{
			AuthType:              types.MCPAuthOAuth,
			AuthServerMetadataURL: server.URL + "/.well-known/oauth-authorization-server",
		},
	}
	repo := &mcpOAuthTestRepo{
		clients: map[string]*types.MCPOAuthClient{},
		tokens:  map[string]*types.MCPOAuthToken{},
	}
	oauth := mcp.NewOAuthManager(repo, mcpOAuthTestServiceRepo{service: service}, nil)
	manager := mcp.NewMCPManager(repo)
	t.Cleanup(manager.Shutdown)
	return &mcpOAuthTestEnv{
		service: service,
		repo:    repo,
		oauth:   oauth,
		manager: manager,
		handler: NewMCPOAuthHandler(oauth, manager, nil, nil),
	}
}

func (e *mcpOAuthTestEnv) registerClient(t *testing.T, clientID string) {
	t.Helper()
	require.NoError(t, e.repo.SaveClient(context.Background(), &types.MCPOAuthClient{
		TenantID: mcpOAuthTestTenant, ServiceID: e.service.ID, ClientID: clientID,
	}))
}

// connect authorizes principal and opens its cached connection.
func (e *mcpOAuthTestEnv) connect(t *testing.T, principal types.Principal) mcp.MCPClient {
	t.Helper()
	require.NoError(t, e.repo.SaveTokenForPrincipal(context.Background(), &types.MCPOAuthToken{
		TenantID:      mcpOAuthTestTenant,
		PrincipalType: principal.Type,
		PrincipalID:   principal.ID,
		ServiceID:     e.service.ID,
		AccessToken:   "access-" + principal.ID,
		TokenType:     "Bearer",
		ExpiresAt:     time.Now().Add(time.Hour),
	}))
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, mcpOAuthTestTenant)
	ctx, cancel := context.WithTimeout(types.WithPrincipal(ctx, principal), 5*time.Second)
	defer cancel()
	client, err := e.manager.GetOrCreateClient(ctx, e.service)
	require.NoError(t, err)
	return client
}

// authorize runs one authorization attempt of principal through the callback.
func (e *mcpOAuthTestEnv) authorize(t *testing.T, principal types.Principal) {
	t.Helper()
	_, state, err := e.oauth.StartAuthorization(
		context.Background(), e.service, mcpOAuthTestTenant, principal, "http://localhost/callback", "/settings",
	)
	require.NoError(t, err)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	query := url.Values{"state": {state}, "code": {"code"}}
	c.Request = httptest.NewRequest(http.MethodGet, "/mcp-services/oauth/callback?"+query.Encode(), nil)
	e.handler.Callback(c)
	require.Equal(t, http.StatusFound, w.Code)
	require.Equal(t, "/settings#mcp_oauth_result=success", w.Header().Get("Location"))
}

func TestMCPOAuthCallbackRecyclesOnlyTheAuthorizingUsersConnection(t *testing.T) {
	env := newMCPOAuthTestEnv(t)
	env.registerClient(t, "client-1")
	alice := types.Principal{Type: types.PrincipalWebUser, ID: "alice"}
	bob := types.Principal{Type: types.PrincipalWebUser, ID: "bob"}
	aliceConn, bobConn := env.connect(t, alice), env.connect(t, bob)

	env.authorize(t, alice)

	require.False(t, aliceConn.IsConnected())
	require.True(t, bobConn.IsConnected(), "another user's connection and its in-flight calls are left alone")
}

// The client registration is shared by every user of the service. Once it has
// been replaced, for example by another replica re-registering after the old
// client was rejected, connections still holding the old client ID are
// recycled by the next authorization as well.
func TestMCPOAuthCallbackRecyclesConnectionsOfAReplacedRegistration(t *testing.T) {
	env := newMCPOAuthTestEnv(t)
	env.registerClient(t, "client-1")
	alice := types.Principal{Type: types.PrincipalWebUser, ID: "alice"}
	bob := types.Principal{Type: types.PrincipalWebUser, ID: "bob"}
	aliceConn, bobConn := env.connect(t, alice), env.connect(t, bob)
	env.registerClient(t, "client-2")
	carol := types.Principal{Type: types.PrincipalWebUser, ID: "carol"}
	carolConn := env.connect(t, carol)

	env.authorize(t, alice)

	require.False(t, aliceConn.IsConnected())
	require.False(t, bobConn.IsConnected(), "built with the replaced client registration")
	require.True(t, carolConn.IsConnected(), "built with the current client registration")
}

func TestMCPOAuthRevokeRecyclesOnlyTheRevokingUsersConnection(t *testing.T) {
	env := newMCPOAuthTestEnv(t)
	env.registerClient(t, "client-1")
	alice := types.Principal{Type: types.PrincipalWebUser, ID: "alice"}
	bob := types.Principal{Type: types.PrincipalWebUser, ID: "bob"}
	aliceConn, bobConn := env.connect(t, alice), env.connect(t, bob)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	req := httptest.NewRequest(http.MethodDelete, "/mcp-services/svc-1/oauth/token", nil)
	c.Request = req.WithContext(types.WithPrincipal(req.Context(), alice))
	c.Params = gin.Params{{Key: "id", Value: env.service.ID}}
	c.Set(types.TenantIDContextKey.String(), mcpOAuthTestTenant)
	env.handler.Revoke(c)
	c.Writer.WriteHeaderNow()

	require.Equal(t, http.StatusNoContent, w.Code)
	token, err := env.repo.GetTokenForPrincipal(context.Background(), mcpOAuthTestTenant, alice, env.service.ID)
	require.NoError(t, err)
	require.Nil(t, token)
	require.False(t, aliceConn.IsConnected())
	require.True(t, bobConn.IsConnected())
}
