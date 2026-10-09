package router

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/service"
	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/handler"
	"github.com/Tencent/WeKnora/internal/mcpserver"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type protocolEndpointService struct{ interfaces.MCPEndpointService }

func (*protocolEndpointService) Authenticate(_ context.Context, id, token string) (*types.MCPEndpoint, error) {
	if id != "ep-1" || token != "fixture" {
		return nil, service.ErrMCPEndpointTokenInvalid
	}
	return &types.MCPEndpoint{
		ID: id, TenantID: 1, Enabled: true, RateLimitPerMinute: 100,
		KnowledgeBaseIDs: types.StringArray{"kb-own"},
		Tools: types.StringArray{
			types.MCPEndpointToolListKnowledgeBases, types.MCPEndpointToolSearchKnowledge,
		},
	}, nil
}

type protocolTenantService struct{ interfaces.TenantService }

func (*protocolTenantService) GetTenantByID(context.Context, uint64) (*types.Tenant, error) {
	return &types.Tenant{ID: 1, Status: "active"}, nil
}

type protocolKBService struct {
	interfaces.KnowledgeBaseService
}

func (*protocolKBService) GetKnowledgeBaseByIDOnly(_ context.Context, id string) (*types.KnowledgeBase, error) {
	return &types.KnowledgeBase{ID: id, TenantID: 1, Name: id}, nil
}

func TestPublishedMCPRouteProtocolAndAuthorization(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv("LOCAL_STORAGE_BASE_DIR", t.TempDir())
	srv := mcpserver.NewServer(&protocolKBService{}, nil, nil, nil, nil, nil, nil, nil,
		nil, nil, nil, nil, nil, nil, nil, nil)
	r := NewRouter(RouterParams{
		Config: &config.Config{Tenant: &config.TenantConfig{}}, SystemHandler: &handler.SystemHandler{},
		MCPServer: srv, MCPEndpointService: &protocolEndpointService{}, TenantService: &protocolTenantService{},
	})
	for _, version := range []string{"2025-11-25", "2026-07-28"} {
		t.Run(version, func(t *testing.T) {
			call := func(method, token, headerVersion string, params map[string]any) *httptest.ResponseRecorder {
				t.Helper()
				if version == "2026-07-28" {
					if _, exists := params["_meta"]; !exists {
						params["_meta"] = map[string]any{
							"io.modelcontextprotocol/protocolVersion":    version,
							"io.modelcontextprotocol/clientCapabilities": map[string]any{},
						}
					}
				}
				body, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params})
				require.NoError(t, err)
				req := httptest.NewRequest(http.MethodPost, "/mcp/ep-1", bytes.NewReader(body))
				req.Header.Set("Content-Type", "application/json")
				req.Header.Set("Accept", "application/json, text/event-stream")
				req.Header.Set("MCP-Protocol-Version", headerVersion)
				if token != "" {
					req.Header.Set("Authorization", "Bearer "+token)
				}
				if version == "2026-07-28" {
					req.Header.Set("Mcp-Method", method)
					if name, ok := params["name"].(string); ok {
						req.Header.Set("Mcp-Name", name)
					}
				}
				w := httptest.NewRecorder()
				r.ServeHTTP(w, req)
				if version == "2026-07-28" {
					require.Empty(t, w.Header().Get("Mcp-Session-Id"))
				}
				return w
			}
			decode := func(w *httptest.ResponseRecorder) map[string]any {
				t.Helper()
				require.Contains(t, []int{http.StatusOK, http.StatusBadRequest}, w.Code, w.Body.String())
				raw := w.Body.String()
				for _, line := range strings.Split(raw, "\n") {
					if strings.HasPrefix(line, "data:") {
						raw = strings.TrimSpace(strings.TrimPrefix(line, "data:"))
						break
					}
				}
				var response map[string]any
				require.NoError(t, json.Unmarshal([]byte(raw), &response))
				return response
			}
			if version == "2026-07-28" {
				require.NotNil(t, decode(call("server/discover", "fixture", version, map[string]any{}))["result"])
			} else {
				params := map[string]any{
					"protocolVersion": version, "capabilities": map[string]any{},
					"clientInfo": map[string]any{"name": "fixture", "version": "1"},
				}
				initialized := decode(call("initialize", "fixture", version, params))
				require.Equal(t, version, initialized["result"].(map[string]any)["protocolVersion"])
			}
			for _, token := range []string{"", "wrong"} {
				require.Equal(t, http.StatusUnauthorized, call("tools/list", token, version, map[string]any{}).Code)
			}
			listed := decode(call("tools/list", "fixture", version, map[string]any{}))
			require.Nil(t, listed["error"])
			listResult := listed["result"].(map[string]any)
			require.Len(t, listResult["tools"], 2)
			if version == "2026-07-28" {
				require.Equal(t, "complete", listResult["resultType"])
				require.Equal(t, "private", listResult["cacheScope"])
				require.Equal(t, float64(0), listResult["ttlMs"])
			} else {
				require.Nil(t, listResult["resultType"])
			}
			called := decode(call("tools/call", "fixture", version, map[string]any{
				"name": types.MCPEndpointToolListKnowledgeBases, "arguments": map[string]any{},
			}))
			require.Nil(t, called["error"])
			rows := called["result"].(map[string]any)["structuredContent"].(map[string]any)["knowledge_bases"].([]any)
			require.Len(t, rows, 1)
			require.Equal(t, "kb-own", rows[0].(map[string]any)["id"])
			denied := decode(call("tools/call", "fixture", version, map[string]any{
				"name":      types.MCPEndpointToolSearchKnowledge,
				"arguments": map[string]any{"query": "fixture", "knowledge_base_ids": []string{"kb-other"}},
			}))
			require.Equal(t, true, denied["result"].(map[string]any)["isError"])
			hiddenResponse := call("tools/call", "fixture", version, map[string]any{
				"name": types.MCPEndpointToolAsk, "arguments": map[string]any{"query": "fixture"},
			})
			if version == "2026-07-28" {
				require.Equal(t, http.StatusBadRequest, hiddenResponse.Code)
			} else {
				require.Equal(t, http.StatusOK, hiddenResponse.Code)
			}
			hidden := decode(hiddenResponse)
			require.Equal(t, float64(-32602), hidden["error"].(map[string]any)["code"])
			if version == "2026-07-28" {
				for _, header := range []string{"2025-11-25", "2099-01-01"} {
					w := call("tools/list", "fixture", header, map[string]any{})
					require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
				}
				unknown := call("tools/list", "fixture", "2099-01-01", map[string]any{
					"_meta": map[string]any{
						"io.modelcontextprotocol/protocolVersion":    "2099-01-01",
						"io.modelcontextprotocol/clientCapabilities": map[string]any{},
					},
				})
				require.Equal(t, http.StatusBadRequest, unknown.Code, unknown.Body.String())
			}
		})
	}
}

func TestMCPModernHeadersPreflight(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv("LOCAL_STORAGE_BASE_DIR", t.TempDir())
	r := NewRouter(RouterParams{
		Config: &config.Config{Tenant: &config.TenantConfig{}}, SystemHandler: &handler.SystemHandler{},
	})
	req := httptest.NewRequest(http.MethodOptions, "/mcp/ep-1", nil)
	req.Header.Set("Origin", "https://client.example")
	req.Header.Set("Access-Control-Request-Method", "POST")
	req.Header.Set("Access-Control-Request-Headers",
		"authorization,content-type,mcp-protocol-version,mcp-method,mcp-name")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("preflight status: %d", w.Code)
	}
	allowed := strings.ToLower(w.Header().Get("Access-Control-Allow-Headers"))
	for _, name := range []string{"authorization", "content-type", "mcp-protocol-version", "mcp-method", "mcp-name"} {
		if !strings.Contains(","+allowed+",", ","+name+",") {
			t.Errorf("preflight does not allow %s", name)
		}
	}
}

// TestMCPEndpointRoutesDeclareValidAPIKeyPolicies guards the startup
// self-check for the MCP endpoint management routes: every declared API-key
// policy must resolve to a registered route.
func TestMCPEndpointRoutesDeclareValidAPIKeyPolicies(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	v1 := engine.Group("/api/v1")
	g := &rbacGuards{}
	RegisterMCPEndpointRoutes(v1, handler.NewMCPEndpointHandler(nil), g)
	g.assertAPIKeyPoliciesMatchRoutes(engine)

	routes := map[string]bool{}
	for _, r := range engine.Routes() {
		routes[r.Method+" "+r.Path] = true
	}
	for _, want := range []string{
		"GET /api/v1/mcp-endpoints",
		"GET /api/v1/mcp-endpoints/tools",
		"POST /api/v1/mcp-endpoints",
		"PUT /api/v1/mcp-endpoints/:endpoint_id",
		"DELETE /api/v1/mcp-endpoints/:endpoint_id",
		"POST /api/v1/mcp-endpoints/:endpoint_id/rotate-token",
	} {
		if !routes[want] {
			t.Errorf("route %s not registered", want)
		}
	}
}

type stubMCPEndpointServiceForRoutes struct {
	interfaces.MCPEndpointService
}

// TestMCPServerRoutesAreMountedAndReported guards the public MCP surface:
// the transport must be reachable on every Streamable HTTP method and the
// deployment capability must flip on only when all three collaborators are
// injected, mirroring RegisterMCPServerRoutes' nil guard.
func TestMCPServerRoutesAreMountedAndReported(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	srv := mcpserver.NewServer(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	RegisterMCPServerRoutes(engine, srv, &stubMCPEndpointServiceForRoutes{}, nil)

	routes := map[string]bool{}
	for _, r := range engine.Routes() {
		routes[r.Method+" "+r.Path] = true
	}
	for _, method := range []string{"POST", "GET", "DELETE"} {
		if !routes[method+" /mcp/:endpoint_id"] {
			t.Errorf("%s /mcp/:endpoint_id not registered", method)
		}
	}

	params := RouterParams{
		MCPEndpointHandler: handler.NewMCPEndpointHandler(nil),
		MCPEndpointService: &stubMCPEndpointServiceForRoutes{},
		MCPServer:          srv,
	}
	if !deploymentCapabilitiesFromRouter(params).Capabilities["integrations.mcpserver"].Supported {
		t.Fatal("integrations.mcpserver must be reported when the MCP server is wired")
	}
	params.MCPServer = nil
	if deploymentCapabilitiesFromRouter(params).Capabilities["integrations.mcpserver"].Supported {
		t.Fatal("integrations.mcpserver must be off without the MCP server")
	}
}
