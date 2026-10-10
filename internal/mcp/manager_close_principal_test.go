package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/utils"
	sdkmcp "github.com/mark3labs/mcp-go/mcp"
	sdkserver "github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/require"
)

func oauthTestService(id string) *types.MCPService {
	return &types.MCPService{
		ID:            id,
		Enabled:       true,
		TransportType: types.MCPTransportHTTPStreamable,
		AuthConfig:    &types.MCPAuthConfig{AuthType: types.MCPAuthOAuth},
	}
}

func webUser(id string) types.Principal {
	return types.Principal{Type: types.PrincipalWebUser, ID: id}
}

// newPendingConnection registers an in-flight connection attempt under key and
// returns the context its cancel func retires.
func newPendingConnection(m *MCPManager, key string) context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	m.connecting[key] = &pendingMCPConnection{done: make(chan struct{}), cancel: cancel}
	return ctx
}

// registeredClient is a cached OAuth connection built with a given client
// registration.
type registeredClient struct {
	*disconnectClient
	tenantID uint64
	clientID string
}

func newRegisteredClient(tenantID uint64, clientID string) registeredClient {
	return registeredClient{disconnectClient: &disconnectClient{}, tenantID: tenantID, clientID: clientID}
}

func (c registeredClient) oauthClientRegistration() (uint64, string, bool) {
	return c.tenantID, c.clientID, true
}

// failingClientRepo cannot read the OAuth client registration.
type failingClientRepo struct {
	*fakeOAuthRepo
}

func (failingClientRepo) GetClient(ctx context.Context, _ uint64, _ string) (*types.MCPOAuthClient, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return nil, errors.New("database unavailable")
}

func TestCloseClientForPrincipalClosesOnlyThatPrincipal(t *testing.T) {
	m := NewMCPManager(nil)
	defer m.cancel()
	service := oauthTestService("svc-1")
	other := oauthTestService("svc-2")
	alice, bob := webUser("alice"), webUser("bob")

	aliceConn := &disconnectClient{}
	bobConn := &disconnectClient{}
	aliceOther := &disconnectClient{}
	m.clients[cacheKey(service, alice)] = aliceConn
	m.clients[cacheKey(service, bob)] = bobConn
	m.clients[cacheKey(other, alice)] = aliceOther
	aliceConnecting := newPendingConnection(m, cacheKey(service, alice))
	bobConnecting := newPendingConnection(m, cacheKey(service, bob))

	// Surrounding whitespace must not change which connection is addressed.
	require.NoError(t, m.CloseClientForPrincipal(service.ID, types.Principal{Type: " web_user ", ID: "alice "}))

	require.True(t, aliceConn.disconnected.Load())
	require.NotContains(t, m.clients, cacheKey(service, alice), "the closed connection is detached")
	require.ErrorIs(t, aliceConnecting.Err(), context.Canceled)
	require.NotContains(t, m.connecting, cacheKey(service, alice), "the principal's attempt is retired")

	require.False(t, bobConn.disconnected.Load(), "another principal's connection stays open")
	require.Same(t, MCPClient(bobConn), m.clients[cacheKey(service, bob)])
	require.NoError(t, bobConnecting.Err(), "another principal's connection attempt keeps running")
	require.Contains(t, m.connecting, cacheKey(service, bob))
	require.False(t, aliceOther.disconnected.Load(), "the principal's connections to other services stay open")
}

// A service without OAuth has a single connection keyed by the service ID that
// every caller shares; closing one principal's connection must not touch it.
func TestCloseClientForPrincipalLeavesSharedConnectionOpen(t *testing.T) {
	m := NewMCPManager(nil)
	defer m.cancel()
	service := &types.MCPService{ID: "plain", Enabled: true, TransportType: types.MCPTransportHTTPStreamable}
	alice := webUser("alice")
	require.Equal(t, service.ID, cacheKey(service, alice))

	shared := &disconnectClient{}
	m.clients[service.ID] = shared
	connecting := newPendingConnection(m, service.ID)

	require.NoError(t, m.CloseClientForPrincipal(service.ID, alice))
	require.False(t, shared.disconnected.Load())
	require.Contains(t, m.clients, service.ID)
	require.NoError(t, connecting.Err())

	require.NoError(t, m.CloseClientForPrincipal(service.ID, types.Principal{}))
	require.False(t, shared.disconnected.Load(), "an empty principal addresses no connection")
	require.Contains(t, m.connecting, service.ID)
}

// After an authorization, the authorizing principal's connection and the
// connections built with a client registration that has since been replaced
// are closed. Connections built with the current registration, and those of
// another tenant, whose registration was not read, stay open.
func TestCloseClientsAfterAuthorizationClosesStaleRegistrations(t *testing.T) {
	const tenantID = uint64(7)
	service := oauthTestService("svc-1")
	alice, bob, carol := webUser("alice"), webUser("bob"), webUser("carol")
	repo := newFakeOAuthRepo()
	require.NoError(t, repo.SaveClient(context.Background(), &types.MCPOAuthClient{
		TenantID: tenantID, ServiceID: service.ID, ClientID: "client-new",
	}))
	m := NewMCPManager(repo)
	defer m.cancel()

	aliceConn := newRegisteredClient(tenantID, "client-new")
	bobConn := newRegisteredClient(tenantID, "client-new")
	carolConn := newRegisteredClient(tenantID, "client-old")
	otherTenantConn := newRegisteredClient(8, "client-old")
	m.clients[cacheKey(service, alice)] = aliceConn
	m.clients[cacheKey(service, bob)] = &managedMCPClient{MCPClient: bobConn, cancel: func() {}}
	m.clients[cacheKey(service, carol)] = &managedMCPClient{MCPClient: carolConn, cancel: func() {}}
	m.clients[cacheKey(service, webUser("dave"))] = otherTenantConn
	bobConnecting := newPendingConnection(m, cacheKey(service, bob))

	require.NoError(t, m.CloseClientsAfterAuthorization(context.Background(), tenantID, service.ID, alice))

	require.True(t, aliceConn.disconnected.Load(), "the authorizing principal's connection is closed")
	require.True(t, carolConn.disconnected.Load(), "a connection with the replaced client ID is closed")
	require.False(t, bobConn.disconnected.Load(), "a connection with the current client ID stays open")
	require.NoError(t, bobConnecting.Err())
	require.False(t, otherTenantConn.disconnected.Load(), "another tenant's connection stays open")
	require.Len(t, m.clients, 2)
}

// Without the stored registration no connection can be judged current, so the
// whole service is closed, unless the read failed because the caller's
// context ended: closing everything then would cancel other principals'
// requests just because one caller went away.
func TestCloseClientsAfterAuthorizationWhenTheRegistrationIsUnreadable(t *testing.T) {
	service := oauthTestService("svc-1")
	alice, bob := webUser("alice"), webUser("bob")
	setup := func(t *testing.T) (*MCPManager, registeredClient, registeredClient) {
		m := NewMCPManager(failingClientRepo{newFakeOAuthRepo()})
		t.Cleanup(m.cancel)
		aliceConn, bobConn := newRegisteredClient(7, "client-1"), newRegisteredClient(7, "client-1")
		m.clients[cacheKey(service, alice)] = aliceConn
		m.clients[cacheKey(service, bob)] = bobConn
		return m, aliceConn, bobConn
	}

	t.Run("read error closes the service", func(t *testing.T) {
		m, aliceConn, bobConn := setup(t)
		require.NoError(t, m.CloseClientsAfterAuthorization(context.Background(), 7, service.ID, alice))
		require.True(t, aliceConn.disconnected.Load())
		require.True(t, bobConn.disconnected.Load())
	})

	t.Run("ended context closes only the principal", func(t *testing.T) {
		m, aliceConn, bobConn := setup(t)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		require.NoError(t, m.CloseClientsAfterAuthorization(ctx, 7, service.ID, alice))
		require.True(t, aliceConn.disconnected.Load())
		require.False(t, bobConn.disconnected.Load())
	})
}

// liveOAuthService is an OAuth MCP service backed by an in-process MCP server
// that records the bearer token of every tool call.
type liveOAuthService struct {
	service     *types.MCPService
	repo        *fakeOAuthRepo
	manager     *MCPManager
	callStarted chan struct{}
	release     chan struct{}

	mu        sync.Mutex
	callAuths []string
}

const liveTenantID = uint64(7)

func newLiveOAuthService(t *testing.T) *liveOAuthService {
	t.Helper()
	utils.SetSSRFWhitelistFromRaw("127.0.0.1")
	t.Cleanup(utils.ResetSSRFWhitelistForTest)

	live := &liveOAuthService{
		repo:        newFakeOAuthRepo(),
		callStarted: make(chan struct{}),
		release:     make(chan struct{}),
	}
	var startOnce sync.Once
	server := sdkserver.NewMCPServer("test", "1")
	slow := func(ctx context.Context, _ sdkmcp.CallToolRequest) (*sdkmcp.CallToolResult, error) {
		startOnce.Do(func() { close(live.callStarted) })
		select {
		case <-live.release:
			return sdkmcp.NewToolResultText("done"), nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	echo := func(context.Context, sdkmcp.CallToolRequest) (*sdkmcp.CallToolResult, error) {
		return sdkmcp.NewToolResultText("echo"), nil
	}
	server.AddTool(sdkmcp.NewTool("slow"), slow)
	server.AddTool(sdkmcp.NewTool("echo"), echo)
	transport := sdkserver.NewStreamableHTTPServer(server, sdkserver.WithStateLess(true))
	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		r.Body = io.NopCloser(bytes.NewReader(body))
		var request struct {
			Method string `json:"method"`
		}
		if json.Unmarshal(body, &request) == nil && request.Method == string(sdkmcp.MethodToolsCall) {
			live.mu.Lock()
			live.callAuths = append(live.callAuths, r.Header.Get("Authorization"))
			live.mu.Unlock()
		}
		transport.ServeHTTP(w, r)
	}))
	t.Cleanup(httpServer.Close)

	live.service = oauthTestService("svc-1")
	live.service.URL = &httpServer.URL
	live.manager = NewMCPManager(live.repo)
	t.Cleanup(live.manager.Shutdown)
	return live
}

func (l *liveOAuthService) saveToken(t *testing.T, principal types.Principal, accessToken string) {
	t.Helper()
	require.NoError(t, l.repo.SaveTokenForPrincipal(context.Background(), &types.MCPOAuthToken{
		TenantID:      liveTenantID,
		PrincipalType: principal.Type,
		PrincipalID:   principal.ID,
		ServiceID:     l.service.ID,
		AccessToken:   accessToken,
		TokenType:     "Bearer",
		ExpiresAt:     time.Now().Add(time.Hour),
	}))
}

func (l *liveOAuthService) connect(ctx context.Context, t *testing.T) MCPClient {
	t.Helper()
	ctx = context.WithValue(ctx, types.TenantIDContextKey, liveTenantID)
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	client, err := l.manager.GetOrCreateClient(ctx, l.service)
	require.NoError(t, err)
	return client
}

func (l *liveOAuthService) connectAs(t *testing.T, principal types.Principal) MCPClient {
	t.Helper()
	return l.connect(types.WithPrincipal(context.Background(), principal), t)
}

func (l *liveOAuthService) lastCallAuth() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.callAuths) == 0 {
		return ""
	}
	return l.callAuths[len(l.callAuths)-1]
}

// Closing a connection cancels the requests it has in flight. Recycling the
// connection of a user whose token changed must not cancel a tool call another
// user is running against the same OAuth service.
func TestCloseClientForPrincipalKeepsOtherPrincipalsCallsRunning(t *testing.T) {
	live := newLiveOAuthService(t)
	alice, bob := webUser("alice"), webUser("bob")
	live.saveToken(t, alice, "access-alice")
	live.saveToken(t, bob, "access-bob")
	aliceConn, bobConn := live.connectAs(t, alice), live.connectAs(t, bob)
	require.NotSame(t, aliceConn, bobConn)

	callDone := make(chan error, 1)
	go func() {
		_, err := bobConn.CallTool(context.Background(), "slow", nil)
		callDone <- err
	}()
	select {
	case <-live.callStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("tool call never reached the server")
	}

	require.NoError(t, live.manager.CloseClientForPrincipal(live.service.ID, alice))
	require.False(t, aliceConn.IsConnected())
	require.True(t, bobConn.IsConnected())

	close(live.release)
	select {
	case err := <-callDone:
		require.NoError(t, err, "the other principal's in-flight call must complete")
	case <-time.After(5 * time.Second):
		t.Fatal("tool call did not finish")
	}
	require.NotSame(t, aliceConn, live.connectAs(t, alice), "the closed principal reconnects on next use")
	require.Same(t, bobConn, live.connectAs(t, bob))
}

// Leaving other principals' connections open after a token change relies on
// connections reading the token from the store on every request rather than
// keeping the one they were opened with.
func TestCachedOAuthConnectionSendsTheCurrentToken(t *testing.T) {
	live := newLiveOAuthService(t)
	bob := webUser("bob")
	live.saveToken(t, bob, "access-bob")
	bobConn := live.connectAs(t, bob)

	_, err := bobConn.CallTool(context.Background(), "echo", nil)
	require.NoError(t, err)
	require.Equal(t, "Bearer access-bob", live.lastCallAuth())

	live.saveToken(t, bob, "access-bob-rotated")
	require.Same(t, bobConn, live.connectAs(t, bob))
	_, err = bobConn.CallTool(context.Background(), "echo", nil)
	require.NoError(t, err)
	require.Equal(t, "Bearer access-bob-rotated", live.lastCallAuth())
}

// An embed chat connects with a per-visitor principal derived from the
// session principal and the visitor ID. Callers that close a principal's
// connection resolve the principal from the same context and must address
// the connection the manager cached.
func TestCloseClientForPrincipalAddressesEmbedVisitorConnections(t *testing.T) {
	live := newLiveOAuthService(t)
	session := types.Principal{Type: types.PrincipalEmbedSession, ID: "7:channel-1:session-1"}
	ctx := types.WithEmbedVisitorID(types.WithPrincipal(context.Background(), session), "visitor-1")
	visitor := types.MCPOAuthPrincipalFromContext(ctx)
	require.Equal(t, types.PrincipalEmbedVisitor, visitor.Type)
	live.saveToken(t, visitor, "access-visitor")
	conn := live.connect(ctx, t)

	require.NoError(t, live.manager.CloseClientForPrincipal(live.service.ID, session))
	require.True(t, conn.IsConnected(), "the session principal does not own the visitor's connection")

	require.NoError(t, live.manager.CloseClientForPrincipal(live.service.ID, visitor))
	require.False(t, conn.IsConnected())
}

// A real connection reports the client registration it was built with, which
// is what decides whether an authorization leaves it stale.
func TestOAuthConnectionReportsItsClientRegistration(t *testing.T) {
	live := newLiveOAuthService(t)
	require.NoError(t, live.repo.SaveClient(context.Background(), &types.MCPOAuthClient{
		TenantID: liveTenantID, ServiceID: live.service.ID, ClientID: "client-1",
	}))
	bob := webUser("bob")
	live.saveToken(t, bob, "access-bob")

	tenantID, clientID, ok := oauthClientRegistrationOf(live.connectAs(t, bob))
	require.True(t, ok)
	require.Equal(t, liveTenantID, tenantID)
	require.Equal(t, "client-1", clientID)
}
