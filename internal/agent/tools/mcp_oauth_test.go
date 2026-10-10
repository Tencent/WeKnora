package tools

import (
	"context"
	"fmt"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/agent/approval"
	"github.com/Tencent/WeKnora/internal/event"
	"github.com/Tencent/WeKnora/internal/mcp"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/Tencent/WeKnora/internal/utils"
	sdkserver "github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/require"
)

const oauthRetryTestTenant = uint64(7)

type oauthRetryTestRepo struct {
	interfaces.MCPOAuthRepository
	mu      sync.Mutex
	clients map[string]*types.MCPOAuthClient
	tokens  map[string]*types.MCPOAuthToken
}

func (r *oauthRetryTestRepo) GetClient(
	_ context.Context, tenantID uint64, serviceID string,
) (*types.MCPOAuthClient, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.clients[fmt.Sprintf("%d|%s", tenantID, serviceID)], nil
}

func (r *oauthRetryTestRepo) registerClient(serviceID, clientID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.clients[fmt.Sprintf("%d|%s", oauthRetryTestTenant, serviceID)] = &types.MCPOAuthClient{
		TenantID: oauthRetryTestTenant, ServiceID: serviceID, ClientID: clientID,
	}
}

func (r *oauthRetryTestRepo) GetTokenForPrincipal(
	_ context.Context, tenantID uint64, principal types.Principal, serviceID string,
) (*types.MCPOAuthToken, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.tokens[fmt.Sprintf("%d|%s|%s", tenantID, principal.StorageID(), serviceID)], nil
}

func (r *oauthRetryTestRepo) saveToken(principal types.Principal, serviceID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tokens[fmt.Sprintf("%d|%s|%s", oauthRetryTestTenant, principal.StorageID(), serviceID)] = &types.MCPOAuthToken{
		TenantID:      oauthRetryTestTenant,
		PrincipalType: principal.Type,
		PrincipalID:   principal.ID,
		ServiceID:     serviceID,
		AccessToken:   "access-" + principal.ID,
		TokenType:     "Bearer",
		ExpiresAt:     time.Now().Add(time.Hour),
	}
}

// oauthRetryTestGate completes the in-conversation authorization as soon as
// the agent asks for it, running authorized in place of the user.
type oauthRetryTestGate struct {
	approval.MCPApproval
	authorized func()
}

func (g oauthRetryTestGate) RequestOAuthAndWait(
	context.Context, approval.OAuthPendingRequest,
) (approval.Decision, error) {
	g.authorized()
	return approval.Decision{Approved: true}, nil
}

func ctxForOAuthRetryTest(principal types.Principal) context.Context {
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, oauthRetryTestTenant)
	return types.WithPrincipal(ctx, principal)
}

// After the user authorizes mid-conversation, the waiting replica recycles the
// user's connection and connections built with a replaced client registration,
// and leaves other users' connections, and their in-flight calls, alone.
func TestOAuthRetryRecyclesOnlyOutdatedConnections(t *testing.T) {
	for _, tc := range []struct {
		name              string
		reregister        bool
		otherUserSurvives bool
	}{
		{name: "client registration unchanged", otherUserSurvives: true},
		{name: "client registration replaced elsewhere", reregister: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			utils.SetSSRFWhitelistFromRaw("127.0.0.1")
			t.Cleanup(utils.ResetSSRFWhitelistForTest)
			server := httptest.NewServer(sdkserver.NewStreamableHTTPServer(
				sdkserver.NewMCPServer("test", "1"), sdkserver.WithStateLess(true),
			))
			t.Cleanup(server.Close)
			service := &types.MCPService{
				ID:            "svc-1",
				Enabled:       true,
				TransportType: types.MCPTransportHTTPStreamable,
				URL:           &server.URL,
				AuthConfig:    &types.MCPAuthConfig{AuthType: types.MCPAuthOAuth},
			}
			repo := &oauthRetryTestRepo{
				clients: map[string]*types.MCPOAuthClient{},
				tokens:  map[string]*types.MCPOAuthToken{},
			}
			repo.registerClient(service.ID, "client-1")
			manager := mcp.NewMCPManager(repo)
			t.Cleanup(manager.Shutdown)

			bob := types.Principal{Type: types.PrincipalWebUser, ID: "bob"}
			repo.saveToken(bob, service.ID)
			bobConn, err := manager.GetOrCreateClient(ctxForOAuthRetryTest(bob), service)
			require.NoError(t, err)

			alice := types.Principal{Type: types.PrincipalWebUser, ID: "alice"}
			ctx := ctxForOAuthRetryTest(alice)
			gate := oauthRetryTestGate{authorized: func() {
				repo.saveToken(alice, service.ID)
				if tc.reregister {
					repo.registerClient(service.ID, "client-2")
				}
			}}
			sess := &MCPOAuthSession{EventBus: event.NewEventBus(), ApprovalCtx: ctx, ExecTimeout: 5 * time.Second}

			aliceConn, err := getOrCreateMCPClientWithOAuthRetry(ctx, manager, service, gate, sess, "tool", "call-1")
			require.NoError(t, err)
			require.True(t, aliceConn.IsConnected())
			require.Equal(t, tc.otherUserSurvives, bobConn.IsConnected())
		})
	}
}
