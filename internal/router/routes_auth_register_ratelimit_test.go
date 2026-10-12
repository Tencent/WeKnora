package router

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/handler"
	"github.com/Tencent/WeKnora/internal/middleware"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
)

// registerRateLimitUsers is only used to prove POST /auth/register is wrapped
// in the same public auth limiter as register-by-invite. Registration itself
// is self_serve so the bootstrap gate is not under test.
type registerRateLimitUsers struct {
	interfaces.UserService
}

func (s *registerRateLimitUsers) Register(context.Context, *types.RegisterRequest) (*types.User, error) {
	return &types.User{ID: "u-rate", Username: "alice", Email: "alice@example.com", IsActive: true}, nil
}

func TestRegisterRouteSharesPublicAuthRateLimit(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.Use(middleware.ErrorHandler())
	h := handler.NewAuthHandler(&config.Config{
		Auth: &config.AuthConfig{RegistrationMode: config.AuthRegistrationModeSelfServe},
	}, &registerRateLimitUsers{}, nil, nil, nil)
	RegisterAuthRoutes(engine.Group("/api/v1"), h, &rbacGuards{})

	body, err := json.Marshal(map[string]string{
		"username": "alice",
		"email":    "alice@example.com",
		"password": "supersecret1",
	})
	if err != nil {
		t.Fatal(err)
	}
	// publicAuthRateLimitMax is 30 per IP per minute, shared with the other
	// public auth routes. The 31st request must be 429.
	const limit = 30
	for i := 0; i < limit; i++ {
		w := postRegister(engine, body)
		if w.Code == http.StatusTooManyRequests {
			t.Fatalf("request %d was rate-limited before the budget was spent", i+1)
		}
		if w.Code != http.StatusCreated {
			t.Fatalf("request %d status = %d, want 201 body=%s", i+1, w.Code, w.Body.String())
		}
	}
	blocked := postRegister(engine, body)
	if blocked.Code != http.StatusTooManyRequests {
		t.Fatalf("request %d status = %d, want 429 body=%s", limit+1, blocked.Code, blocked.Body.String())
	}
}

func postRegister(engine *gin.Engine, body []byte) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/register", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = "203.0.113.10:1234"
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)
	return w
}
