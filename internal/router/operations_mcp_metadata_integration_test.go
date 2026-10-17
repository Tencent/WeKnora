package router

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/Tencent/WeKnora/internal/agent/approval"
	"github.com/Tencent/WeKnora/internal/agent/operations"
	"github.com/Tencent/WeKnora/internal/agent/tools"
	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/application/service"
	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/event"
	"github.com/Tencent/WeKnora/internal/handler"
	"github.com/Tencent/WeKnora/internal/mcp"
	"github.com/Tencent/WeKnora/internal/middleware"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/utils"
	"github.com/gin-gonic/gin"
	sdkmcp "github.com/mark3labs/mcp-go/mcp"
	sdkserver "github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

type metadataApproval struct {
	approval.BuiltinApproval
	approved bool
	requests []approval.PendingRequest
}

func (g *metadataApproval) RequestBuiltinApproval(
	_ context.Context,
	req approval.PendingRequest,
) (approval.Decision, error) {
	g.requests = append(g.requests, req)
	return approval.Decision{Approved: g.approved, PendingID: "metadata-approval"}, nil
}

func (*metadataApproval) MarkExecuted(context.Context, string) {}

// Exercise the operation catalog and approval path through the real dispatcher,
// route guards, handlers, service, SQLite repository and an HTTP MCP server.
// Authentication is supplied by the fixture; this does not test JWT login or an LLM.
func TestAssistantMCPDirectoryEndToEnd(t *testing.T) {
	gin.SetMode(gin.TestMode)
	utils.SetSSRFWhitelistFromRaw("127.0.0.1")
	t.Cleanup(utils.ResetSSRFWhitelistForTest)
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "mcp.db")), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })
	require.NoError(t, db.AutoMigrate(&types.MCPService{}, &types.MCPMetadata{}))
	repo := repository.NewMCPServiceRepository(db)
	manager := mcp.NewMCPManager(nil)
	t.Cleanup(manager.Shutdown)
	svc := service.NewMCPServiceService(repo, manager, nil)

	var requests, executions atomic.Int32
	var fail atomic.Bool
	upstream := sdkserver.NewMCPServer(
		"Directory fixture",
		"1",
		sdkserver.WithToolCapabilities(false),
		sdkserver.WithInstructions("PRIVATE SERVER INSTRUCTIONS"),
	)
	addTool := func(name string) {
		upstream.AddTool(
			sdkmcp.Tool{
				Name:           name,
				Description:    "Find documents",
				RawInputSchema: json.RawMessage(`{"type":"object","properties":{}}`),
			},
			func(context.Context, sdkmcp.CallToolRequest) (*sdkmcp.CallToolResult, error) {
				executions.Add(1)
				return sdkmcp.NewToolResultText("unused"), nil
			},
		)
	}
	addTool("search")
	upstreamHandler := sdkserver.NewStreamableHTTPServer(upstream, sdkserver.WithStateLess(true))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if fail.Load() {
			http.Error(w, "private upstream failure", http.StatusUnauthorized)
			return
		}
		upstreamHandler.ServeHTTP(w, r)
	}))
	t.Cleanup(server.Close)
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(1))
	savedService := &types.MCPService{
		ID:            "svc",
		TenantID:      1,
		Name:          "Docs server",
		Enabled:       true,
		URL:           &server.URL,
		TransportType: types.MCPTransportHTTPStreamable,
	}
	require.NoError(t, repo.Create(ctx, savedService))

	role := types.TenantRoleAdmin
	enforce := true
	guards := &rbacGuards{cfg: &config.Config{Tenant: &config.TenantConfig{EnableRBAC: &enforce}}}
	router := gin.New()
	router.Use(middleware.ErrorHandler(), func(c *gin.Context) {
		identity, ok := types.InternalDispatchFromContext(c.Request.Context())
		if !ok {
			c.AbortWithStatus(http.StatusUnauthorized)
			return
		}
		requestCtx := types.WithCaller(
			c.Request.Context(),
			types.Caller{TenantID: identity.TenantID, UserID: identity.UserID, Role: role},
		)
		requestCtx = types.WithPrincipal(requestCtx, types.Principal{Type: types.PrincipalWebUser, ID: identity.UserID})
		requestCtx = context.WithValue(requestCtx, types.TenantIDContextKey, identity.TenantID)
		c.Set(types.TenantIDContextKey.String(), identity.TenantID)
		c.Request = c.Request.WithContext(requestCtx)
		c.Next()
	})
	RegisterMCPServiceRoutes(
		router.Group("/api/v1"),
		handler.NewMCPServiceHandler(svc, nil, nil, nil, nil),
		&handler.MCPCredentialsHandler{},
		&handler.MCPOAuthHandler{},
		guards,
	)
	dispatcher := operations.NewHandlerDispatcher()
	dispatcher.Bind(router)
	gate := &metadataApproval{approved: true}
	runtime := tools.OperationsRuntime{
		Catalog: operations.Builtin(), Dispatcher: dispatcher, Gate: gate,
		Identity: operations.Identity{UserID: "user-a", TenantID: 1},
		Access:   operations.Access{Role: role, EnforceRole: true},
	}
	interactive := tools.WithToolExecContext(
		ctx,
		&tools.ToolExecContext{
			SessionID:   "session",
			UserID:      "web_user:user-a",
			ToolCallID:  "call",
			EventBus:    event.NewEventBus(),
			Interactive: true,
		},
	)
	run := func(id string) *types.ToolResult {
		t.Helper()
		runtime.Access.Role = role
		args, err := json.Marshal(map[string]any{"operation": id, "params": map[string]any{"mcp_service_id": "svc"}})
		require.NoError(t, err)
		result, err := tools.NewRunOperationTool(runtime).Execute(interactive, args)
		require.NoError(t, err)
		return result
	}

	resultOf := func(result *types.ToolResult) string {
		t.Helper()
		require.True(t, result.Success, result.Error)
		var output struct {
			Result json.RawMessage `json:"result"`
		}
		require.NoError(t, json.Unmarshal([]byte(result.Output), &output))
		return string(output.Result)
	}
	found, err := tools.NewFindOperationsTool(runtime.Catalog, runtime.Access).
		Execute(ctx, json.RawMessage(`{"query":"metadata","domain":"mcp_service"}`))
	require.NoError(t, err)
	require.Contains(t, found.Output, "mcp_service.get_metadata")
	require.Contains(t, found.Output, "mcp_service.refresh_metadata")
	initial := run("mcp_service.get_metadata")
	require.True(t, initial.Success, initial.Error)
	require.Contains(t, initial.Output, `"synced":false`)
	require.Zero(t, requests.Load(), "saved reads must never connect upstream")

	gate.approved = false
	declined := run("mcp_service.refresh_metadata")
	require.False(t, declined.Success)
	require.Empty(t, declined.Data)
	require.Zero(t, requests.Load())
	require.Len(t, gate.requests, 1)
	require.Contains(t, gate.requests[0].Description, "Docs server")
	require.Contains(t, gate.requests[0].Description, "without executing tools")
	gate.approved = true
	refreshed := run("mcp_service.refresh_metadata")
	require.True(t, refreshed.Success, refreshed.Error)
	require.Contains(t, refreshed.Output, `"synced":true`)
	require.Contains(t, refreshed.Output, `"tool_count":1`)
	require.Contains(t, refreshed.Output, `"name":"search"`)
	require.NotContains(t, refreshed.Output, "PRIVATE SERVER INSTRUCTIONS")
	require.NotContains(t, refreshed.Output, "inputSchema")
	change, err := json.Marshal(refreshed.Data)
	require.NoError(t, err)
	require.Contains(t, string(change), `"area":"mcp_service"`)
	read := run("mcp_service.get_metadata")
	require.JSONEq(t, resultOf(refreshed), resultOf(read))
	require.Empty(t, read.Data, "reads do not emit data changes")

	before := requests.Load()
	addTool("lookup")
	require.Contains(t, run("mcp_service.get_metadata").Output, `"tool_count":1`)
	require.Equal(t, before, requests.Load())
	live := run("mcp_service.list_tools")
	require.True(t, live.Success, live.Error)
	require.Contains(t, live.Output, `"name":"lookup"`)
	require.Empty(t, live.Data)
	require.Greater(t, requests.Load(), before)
	require.Contains(
		t,
		run("mcp_service.get_metadata").Output,
		`"tool_count":1`,
		"live queries do not save the directory",
	)
	runtime.Access.Level = types.AssistantAccessAutoWrite
	prompts := len(gate.requests)
	updated := run("mcp_service.refresh_metadata")
	require.True(t, updated.Success, updated.Error)
	require.Contains(t, updated.Output, `"tool_count":2`)
	require.Contains(t, updated.Output, `"name":"lookup"`)
	require.Len(t, gate.requests, prompts, "auto-write uses the saved policy")
	require.JSONEq(t, resultOf(updated), resultOf(run("mcp_service.get_metadata")))

	before = requests.Load()
	runtime.Access.Level = types.AssistantAccessReadOnly
	require.False(t, run("mcp_service.refresh_metadata").Success)
	require.Equal(t, before, requests.Load())
	runtime.Access.Level = types.AssistantAccessAutoWrite
	role = types.TenantRoleViewer
	denied := run("mcp_service.refresh_metadata")
	require.False(t, denied.Success)
	require.Contains(t, denied.Error, "administrator")
	require.Empty(t, denied.Data)
	require.Equal(t, before, requests.Load(), "shared refresh must fail before connecting")
	require.True(t, run("mcp_service.get_metadata").Success)
	role = types.TenantRoleAdmin
	runtime.Identity.TenantID = 2
	require.False(t, run("mcp_service.get_metadata").Success)
	require.False(t, run("mcp_service.refresh_metadata").Success)
	require.Equal(t, before, requests.Load())
	runtime.Identity.TenantID = 1

	fail.Store(true)
	failed := run("mcp_service.refresh_metadata")
	require.False(t, failed.Success)
	require.Empty(t, failed.Data)
	require.NotContains(t, failed.Error, "private upstream failure")
	require.JSONEq(
		t,
		resultOf(updated),
		resultOf(run("mcp_service.get_metadata")),
		"a failed sync keeps the complete prior snapshot",
	)
	require.Zero(t, executions.Load(), "directory synchronization must never execute a tool")

	// A connection edit marks the existing directory stale rather than silently
	// fetching or claiming it represents the new configuration.
	stored, err := repo.GetByID(ctx, 1, "svc")
	require.NoError(t, err)
	newURL := server.URL + "/changed"
	stored.URL = &newURL
	require.NoError(t, repo.Update(ctx, stored))
	require.Contains(t, run("mcp_service.get_metadata").Output, `"stale":true`)

	// OAuth queries use the dispatching user's identity and cannot fall back to
	// another principal's snapshot, even when there used to be a shared one.
	stored.AuthConfig = &types.MCPAuthConfig{AuthType: types.MCPAuthOAuth}
	require.NoError(t, repo.Update(ctx, stored))
	personal := &types.MCPMetadata{
		TenantID:  1,
		ServiceID: "svc",
		Principal: types.MCPOAuthPrincipalFromContext(
			types.WithPrincipal(
				ctx,
				types.Principal{
					Type: types.PrincipalWebUser,
					ID:   "user-a",
				},
			),
		).
			StorageID(),
		ConfigFingerprint: types.MCPConfigFingerprint(stored),
		Tools:             []*types.MCPTool{{Name: "personal_tool"}},
	}
	require.NoError(t, db.Create(personal).Error)
	require.Contains(t, run("mcp_service.get_metadata").Output, "personal_tool")
	runtime.Identity.UserID = "user-b"
	require.Contains(t, run("mcp_service.get_metadata").Output, `"synced":false`)
}
