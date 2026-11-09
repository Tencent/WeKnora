package handler

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Tencent/WeKnora/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// TestMCPFrontendRedirectDefault pins the subpath-aware default: "/<subpath>/"
// with a FRONTEND_BASE_URL subpath, "/" otherwise (including nil config).
func TestMCPFrontendRedirectDefault(t *testing.T) {
	t.Setenv("FRONTEND_BASE_URL", "")

	require.Equal(t, "/", mcpFrontendRedirectDefault(nil))
	require.Equal(t, "/", mcpFrontendRedirectDefault(&config.Config{}))
	require.Equal(t, "/weknora/",
		mcpFrontendRedirectDefault(&config.Config{FrontendBaseURL: "https://example.com/weknora"}))
	require.Equal(t, "/",
		mcpFrontendRedirectDefault(&config.Config{FrontendBaseURL: "https://example.com"}))
	require.Equal(t, "/a/b/",
		mcpFrontendRedirectDefault(&config.Config{FrontendBaseURL: "https://example.com/a/b/"}))
}

// TestMCPFrontendRedirectDefaultPathOnlyEnv covers the env fallback: a
// path-only FRONTEND_BASE_URL normalizes to "/<subpath>/".
func TestMCPFrontendRedirectDefaultPathOnlyEnv(t *testing.T) {
	t.Setenv("FRONTEND_BASE_URL", "/env-path")
	require.Equal(t, "/env-path/", mcpFrontendRedirectDefault(&config.Config{}))
}

// TestMCPCallbackFallbackRedirect pins the callback's error fallback redirect:
// subpath deployments bounce back into the SPA, root deployments stay "/".
func TestMCPCallbackFallbackRedirect(t *testing.T) {
	t.Setenv("FRONTEND_BASE_URL", "")

	for _, tc := range []struct {
		name    string
		baseURL string
		want    string
	}{
		{"subpath default", "https://example.com/weknora", "/weknora/#mcp_oauth_error=access_denied"},
		{"root default", "", "/#mcp_oauth_error=access_denied"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := &MCPOAuthHandler{cfg: &config.Config{FrontendBaseURL: tc.baseURL}}
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest(
				http.MethodGet,
				"/api/v1/mcp-oauth/callback?error=access_denied",
				nil,
			)

			h.Callback(c)

			require.Equal(t, http.StatusFound, w.Code)
			require.Equal(t, tc.want, w.Header().Get("Location"))
		})
	}
}
