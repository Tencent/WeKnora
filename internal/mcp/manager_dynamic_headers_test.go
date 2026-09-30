package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/mcp/headertemplate"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/utils"
	sdkmcp "github.com/mark3labs/mcp-go/mcp"
	sdkserver "github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/require"
)

func TestManagerSeparatesDynamicHeadersAndForwardsThemOnToolRequests(t *testing.T) {
	utils.SetSSRFWhitelistFromRaw("127.0.0.1")
	t.Cleanup(utils.ResetSSRFWhitelistForTest)

	server := sdkserver.NewMCPServer("dynamic-header-test", "1")
	server.AddTool(sdkmcp.NewTool("echo"), func(context.Context, sdkmcp.CallToolRequest) (*sdkmcp.CallToolResult, error) {
		return sdkmcp.NewToolResultText("ok"), nil
	})
	transport := sdkserver.NewStreamableHTTPServer(server, sdkserver.WithStateLess(true))
	var observedMu sync.Mutex
	observed := make(map[string]int)
	missingBusinessHeader := 0
	fixedAuthorization := 0
	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		r.Body = io.NopCloser(bytes.NewReader(body))
		var request struct {
			Method string `json:"method"`
		}
		_ = json.Unmarshal(body, &request)
		if request.Method != "" {
			observedMu.Lock()
			observed[request.Method+":"+r.Header.Get("X-Resolved-Department")]++
			if len(r.Header.Values("X-Resolved-Department")) == 0 {
				missingBusinessHeader++
			}
			if r.Header.Get("Authorization") == "Bearer stored-token" {
				fixedAuthorization++
			}
			observedMu.Unlock()
		}
		transport.ServeHTTP(w, r)
	}))
	t.Cleanup(httpServer.Close)
	url := httpServer.URL + "/mcp"
	service := &types.MCPService{
		ID: "dynamic", Enabled: true, URL: &url, TransportType: types.MCPTransportHTTPStreamable,
		Headers: types.MCPHeaders{"X-Resolved-Department": "{{request.headers.X-AAA}}"},
		AuthConfig: &types.MCPAuthConfig{AuthType: types.MCPAuthBearer, Token: "stored-token"},
	}
	manager := NewMCPManager(nil)
	t.Cleanup(manager.Shutdown)
	makeContext := func(department string) context.Context {
		ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(7))
		ctx = types.WithCaller(ctx, types.Caller{TenantID: 7, UserID: "alice", Role: types.TenantRoleViewer})
		ctx = types.WithPrincipal(ctx, types.Principal{Type: types.PrincipalWebUser, ID: "alice"})
		snapshot := headertemplate.NewContext(map[string]string{
			"user.id": "alice", "user.email": "alice@example.com", "tenant.id": "7",
			"principal.id": "alice", "principal.type": types.PrincipalWebUser,
		}, http.Header{"X-Aaa": {department}})
		return types.WithMCPHeaderContext(ctx, snapshot)
	}
	ctxA, ctxB := makeContext("department-A"), makeContext("department-B")
	clientA, err := manager.GetOrCreateClient(ctxA, service)
	require.NoError(t, err)
	_, err = clientA.ListTools(ctxA)
	require.NoError(t, err)
	_, err = clientA.CallTool(ctxA, "echo", map[string]interface{}{})
	require.NoError(t, err)
	clientB, err := manager.GetOrCreateClient(ctxB, service)
	require.NoError(t, err)
	require.NotSame(t, clientA, clientB)
	_, err = clientB.ListTools(ctxB)
	require.NoError(t, err)
	_, err = clientB.CallTool(ctxB, "echo", map[string]interface{}{})
	require.NoError(t, err)
	reusedA, err := manager.GetOrCreateClient(ctxA, service)
	require.NoError(t, err)
	require.Same(t, clientA, reusedA)
	ctxMissing := makeContext("")
	clientMissing, err := manager.GetOrCreateClient(ctxMissing, service)
	require.NoError(t, err)
	_, err = clientMissing.ListTools(ctxMissing)
	require.NoError(t, err)
	_, err = clientMissing.CallTool(ctxMissing, "echo", map[string]interface{}{})
	require.NoError(t, err)
	require.Equal(t, []string{"dynamic"}, manager.ListActiveServices())

	observedMu.Lock()
	defer observedMu.Unlock()
	require.Greater(t, observed["tools/list:department-A"], 0)
	require.Greater(t, observed["tools/list:department-B"], 0)
	require.Greater(t, observed["tools/call:department-A"], 0)
	require.Greater(t, observed["tools/call:department-B"], 0)
	require.Greater(t, missingBusinessHeader, 0)
	require.Greater(t, fixedAuthorization, 0)
}

func TestDynamicConnectionIdleThreshold(t *testing.T) {
	client := &managedMCPClient{dynamic: true, lastUsed: time.Now().Add(-time.Hour)}
	require.True(t, client.retireIdle(time.Now()))
	require.False(t, client.isAvailable())
}

func TestDynamicConnectionCapacityIncludesPendingAttempts(t *testing.T) {
	manager := NewMCPManager(nil)
	t.Cleanup(manager.Shutdown)
	manager.maxDynamicPerService = 1
	manager.maxDynamicTotal = 1
	_, cancel := context.WithCancel(context.Background())
	manager.connecting["existing"] = &pendingMCPConnection{
		cancel: cancel, done: make(chan struct{}), serviceID: "svc", dynamic: true,
	}
	service := &types.MCPService{ID: "svc", Enabled: true,
		Headers: types.MCPHeaders{"AAA": "{{request.headers.X-Department-ID}}"}}
	_, err := manager.GetOrCreateClient(templateContext("new"), service)
	require.ErrorContains(t, err, "capacity")
	require.Len(t, manager.connecting, 1)
	service.ID = "other"
	_, err = manager.GetOrCreateClient(templateContext("new"), service)
	require.ErrorContains(t, err, "capacity")
}

type capacityConnectedClient struct {
	MCPClient
	disconnected bool
}

func (c *capacityConnectedClient) IsConnected() bool { return !c.disconnected }
func (c *capacityConnectedClient) Disconnect() error {
	c.disconnected = true
	return nil
}

func TestDynamicCleanupKeepsActiveCallsAndReclaimsExpiredConnections(t *testing.T) {
	manager := NewMCPManager(nil)
	t.Cleanup(manager.Shutdown)
	underlying := &capacityConnectedClient{}
	client := &managedMCPClient{
		MCPClient: underlying, cancel: func() {}, serviceID: "svc", dynamic: true,
		lastUsed: time.Now().Add(-3 * time.Minute),
	}
	manager.clients["svc\x00scope"] = client
	require.NoError(t, client.begin())
	client.usageMu.Lock()
	client.lastUsed = time.Now().Add(-3 * time.Minute)
	client.usageMu.Unlock()
	manager.removeDisconnectedClients()
	require.Contains(t, manager.clients, "svc\x00scope")
	require.False(t, underlying.disconnected)
	client.end()
	client.usageMu.Lock()
	client.lastUsed = time.Now().Add(-3 * time.Minute)
	client.usageMu.Unlock()
	manager.removeDisconnectedClients()
	require.NotContains(t, manager.clients, "svc\x00scope")
	require.True(t, underlying.disconnected)
}
