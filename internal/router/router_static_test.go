package router

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/handler"
	"github.com/Tencent/WeKnora/internal/mcpserver"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestNewRouterFrontendStaticEditionOptIn(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		name      string
		edition   string
		webDir    string
		wantIndex bool
	}{
		{name: "standard explicit directory", edition: "standard", webDir: "web", wantIndex: true},
		{name: "standard default directory", edition: "standard"},
		{name: "standard missing directory", edition: "standard", webDir: "missing"},
		{name: "standard missing index", edition: "standard", webDir: "empty"},
		{name: "lite default directory", edition: "lite", wantIndex: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			t.Chdir(root)
			require.NoError(t, os.MkdirAll(filepath.Join(root, "web", "assets"), 0o700))
			require.NoError(t, os.Mkdir(filepath.Join(root, "empty"), 0o700))
			index := "<!doctype html><html><body>WeKnora</body></html>"
			asset := "console.log('WeKnora');"
			require.NoError(t, os.WriteFile(filepath.Join(root, "web", "index.html"), []byte(index), 0o600))
			require.NoError(t, os.WriteFile(filepath.Join(root, "web", "assets", "app-test.js"), []byte(asset), 0o600))
			webDir := ""
			if tc.webDir != "" {
				webDir = filepath.Join(root, tc.webDir)
			}
			t.Setenv("WEKNORA_WEB_DIR", webDir)
			t.Setenv("LOCAL_STORAGE_BASE_DIR", filepath.Join(root, "files"))
			previousEdition := handler.Edition
			handler.Edition = tc.edition
			t.Cleanup(func() { handler.Edition = previousEdition })

			r := NewRouter(RouterParams{
				Config:             &config.Config{},
				SystemHandler:      &handler.SystemHandler{},
				MCPServer:          &mcpserver.Server{},
				MCPEndpointService: &stubMCPEndpointServiceForRoutes{},
			})
			for _, path := range []string{"/", "/login", "/assets/app-test.js"} {
				recorder := httptest.NewRecorder()
				r.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
				if !tc.wantIndex {
					require.Equal(t, http.StatusUnauthorized, recorder.Code, path)
					continue
				}
				require.Equal(t, http.StatusOK, recorder.Code, path)
				if path == "/assets/app-test.js" {
					require.Equal(t, asset, recorder.Body.String())
				} else {
					require.Contains(t, recorder.Header().Get("Content-Type"), "text/html")
					require.Equal(t, index, recorder.Body.String())
				}
			}

			recorder := httptest.NewRecorder()
			r.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", nil))
			require.Equal(t, http.StatusUnauthorized, recorder.Code)
			require.Contains(t, recorder.Body.String(), "missing authentication")

			// Anonymous requests must be rejected before the unused MCP transport.
			for _, request := range []struct {
				method string
				path   string
			}{
				{method: http.MethodGet, path: "/mcp/endpoint"},
				{method: http.MethodHead, path: "/mcp/endpoint"},
				{method: http.MethodGet, path: "/mcp/endpoint/unknown"},
			} {
				t.Run(request.method+" "+request.path, func(t *testing.T) {
					recorder := httptest.NewRecorder()
					r.ServeHTTP(recorder, httptest.NewRequest(request.method, request.path, nil))
					require.Equal(t, http.StatusUnauthorized, recorder.Code)
					require.Contains(t, recorder.Header().Get("Content-Type"), "application/json")
					if request.method == http.MethodGet && request.path == "/mcp/endpoint" {
						require.Equal(t, `Bearer realm="weknora-mcp"`, recorder.Header().Get("WWW-Authenticate"))
						require.Contains(t, recorder.Body.String(), "missing bearer token")
					}
				})
			}
		})
	}
}

func TestFrontendStaticDoesNotInterceptMatchedBackendRoute(t *testing.T) {
	gin.SetMode(gin.TestMode)
	webDir := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(webDir, "backend"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(webDir, "index.html"), []byte("spa"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(webDir, "backend", "collision"), []byte("static file"), 0o600))
	t.Setenv("WEKNORA_WEB_DIR", webDir)

	for _, method := range []string{http.MethodGet, http.MethodHead} {
		t.Run(method, func(t *testing.T) {
			r := gin.New()
			serveFrontendStatic(r)
			authCalled := false
			handlerCalled := false
			r.Handle(method, "/backend/:id", func(c *gin.Context) {
				authCalled = true
				c.AbortWithStatus(http.StatusUnauthorized)
			}, func(c *gin.Context) {
				handlerCalled = true
				c.Status(http.StatusOK)
			})

			recorder := httptest.NewRecorder()
			r.ServeHTTP(recorder, httptest.NewRequest(method, "/backend/collision", nil))

			require.Equal(t, http.StatusUnauthorized, recorder.Code)
			require.True(t, authCalled, "matched backend authentication must run before static serving")
			require.False(t, handlerCalled, "authentication rejection must stop the backend handler")
			require.Empty(t, recorder.Header().Get("Cache-Control"))
		})
	}
}

func TestFrontendStaticDoesNotInterceptResourceGrant(t *testing.T) {
	gin.SetMode(gin.TestMode)
	webDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(webDir, "index.html"), []byte("spa"), 0o600))
	t.Setenv("WEKNORA_WEB_DIR", webDir)

	r := gin.New()
	serveFrontendStatic(r)
	r.GET("/r/:token", func(c *gin.Context) { c.String(http.StatusOK, "resource") })

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/r/token", nil)
	r.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, "resource", recorder.Body.String())
}
