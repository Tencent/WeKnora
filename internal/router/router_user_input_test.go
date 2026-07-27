package router

import (
	"net/http"
	"testing"

	"github.com/Tencent/WeKnora/internal/agent/userinput"
	"github.com/Tencent/WeKnora/internal/handler"
	"github.com/gin-gonic/gin"
)

func TestUserInputRouteIsRegisteredAsInteractiveAgentEndpoint(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	RegisterUserInputRoutes(
		engine.Group("/api/v1"),
		handler.NewUserInputHandler(userinput.NewGate(nil, nil)),
		&rbacGuards{},
	)

	wantMethod := http.MethodPost
	wantPath := "/api/v1/agent/user-inputs/:pending_id"
	for _, route := range engine.Routes() {
		if route.Method == wantMethod && route.Path == wantPath {
			return
		}
	}
	t.Fatalf("route %s %s is not registered", wantMethod, wantPath)
}

func TestRegisterUserInputRoutesIncludesPendingRestore(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	RegisterUserInputRoutes(
		router.Group("/api/v1"),
		handler.NewUserInputHandler(userinput.NewGate(nil, nil)),
		&rbacGuards{},
	)
	found := false
	for _, route := range router.Routes() {
		if route.Method == http.MethodGet && route.Path == "/api/v1/agent/user-inputs/pending" {
			found = true
		}
	}
	if !found {
		t.Fatal("pending restore route is not registered")
	}
}
