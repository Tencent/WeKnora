package router

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/service"
	"github.com/Tencent/WeKnora/internal/handler"
	sessionhandler "github.com/Tencent/WeKnora/internal/handler/session"
	"github.com/Tencent/WeKnora/internal/middleware"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
)

func TestWorkbenchLoggerOmitsTicketsBodiesAndQueries(t *testing.T) {
	var logs bytes.Buffer
	log := logrus.New()
	log.SetOutput(&logs)
	r := gin.New()
	r.ContextWithFallback = true
	r.Use(func(c *gin.Context) {
		c.Request = c.Request.WithContext(
			context.WithValue(c.Request.Context(), types.LoggerContextKey, logrus.NewEntry(log)),
		)
		c.Next()
	})
	r.Use(workbenchAwareRequestLogger())
	r.POST(
		"/api/v1/sessions/:id/sandbox/command-ticket",
		func(c *gin.Context) { c.JSON(200, gin.H{"success": true, "data": gin.H{"ticket": "response-secret"}}) },
	)
	req := httptest.NewRequest(
		"POST",
		"/api/v1/sessions/session/sandbox/command-ticket?ticket=query-secret",
		strings.NewReader(`{"command":"request-secret"}`),
	)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, 200, w.Code)
	require.Contains(t, w.Body.String(), "response-secret")
	require.Contains(t, logs.String(), "workbench request")
	for _, secret := range []string{"request-secret", "response-secret", "query-secret"} {
		require.NotContains(t, logs.String(), secret)
	}
}

func TestWorkbenchRoutesWildcardCompatibilityAndPreAuthSocket(t *testing.T) {
	t.Setenv("WEKNORA_SANDBOX_WORKBENCH_ENABLED", "false")
	t.Setenv("WEKNORA_SANDBOX_WORKBENCH_ORIGINS", "")
	h, err := handler.NewWorkbenchHandler(service.NewWorkbenchService(service.WorkbenchServiceDeps{}))
	require.NoError(t, err)
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(middleware.ErrorHandler())
	RegisterWorkbenchPublicRoutes(r, h)
	sessionHandler := &sessionhandler.Handler{}
	RegisterSandboxTerminalRoutes(r, sessionHandler)
	r.Use(func(c *gin.Context) { c.AbortWithStatus(http.StatusUnauthorized) })
	v1 := r.Group("/api/v1")
	g := &rbacGuards{}
	RegisterSessionRoutes(v1, sessionHandler, &handler.MessageSuggestionHandler{}, g)
	require.NotPanics(t, func() { RegisterWorkbenchRoutes(v1, h) })

	routes := make(map[string]bool)
	for _, route := range r.Routes() {
		routes[route.Method+" "+route.Path] = true
	}
	for _, path := range []string{
		"GET /api/v1/sandbox-terminal",
		"GET /api/v1/sessions/:id/sandbox/terminal",
		"POST /api/v1/sessions/:session_id/sandbox/terminal-ticket",
		"POST /api/v1/sessions/:session_id/sandbox/command-ticket",
	} {
		require.True(t, routes[path], path)
	}
	_, terminalPolicy := g.apiKeyAuthorizer.Lookup(
		http.MethodPost, "/api/v1/sessions/:session_id/sandbox/terminal-ticket",
	)
	require.True(t, terminalPolicy)
	_, commandPolicy := g.apiKeyAuthorizer.Lookup(
		http.MethodPost, "/api/v1/sessions/:session_id/sandbox/command-ticket",
	)
	require.False(t, commandPolicy, "command terminals must remain web-only")

	for _, test := range []struct {
		method, path string
		status       int
	}{
		{"GET", "/api/v1/sandbox-terminal", 404},
		{"GET", "/api/v1/sessions/one/sandbox/workbench", 401},
		{"POST", "/api/v1/sessions/one/sandbox/terminal-ticket", 401},
		{"POST", "/api/v1/sessions/one/sandbox/command-ticket", 401},
		{"PATCH", "/api/v1/sessions/one/sandbox/files", 401},
	} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(test.method, test.path, nil))
		require.Equal(t, test.status, w.Code, test.path)
	}
}
