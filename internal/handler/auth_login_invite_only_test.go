package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
)

// stubLoginUserService is a UserService whose ONLY useful method is Login.
// Mirrors stubRegisterUserService so we can pin the invite_only contract
// for the login path without dragging in the full user service.
type stubLoginUserService struct {
	interfaces.UserService
	login func(ctx context.Context, req *types.LoginRequest) (*types.LoginResponse, error)
}

func (s *stubLoginUserService) Login(ctx context.Context, req *types.LoginRequest) (*types.LoginResponse, error) {
	return s.login(ctx, req)
}

func newLoginTestRouter(h *AuthHandler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(errorCapture())
	r.POST("/auth/login", h.Login)
	return r
}

func doLogin(t *testing.T, r *gin.Engine, body any) *httptest.ResponseRecorder {
	t.Helper()
	buf, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/auth/login", bytes.NewReader(buf))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// TestLogin_InviteOnlyStillAllowsExistingUsers is the regression for #3296.
// DISABLE_REGISTRATION=true coerces auth.registration_mode to invite_only,
// which must gate /auth/register only. Existing accounts continue to log in
// via /auth/login — the handler must not consult registration_mode at all.
func TestLogin_InviteOnlyStillAllowsExistingUsers(t *testing.T) {
	called := false
	us := &stubLoginUserService{
		login: func(_ context.Context, req *types.LoginRequest) (*types.LoginResponse, error) {
			called = true
			if req.Email != "alice@example.com" {
				t.Fatalf("email = %q, want alice@example.com", req.Email)
			}
			return &types.LoginResponse{
				Success: true,
				Message: "Login successful",
				Token:   "access-token",
				User:    &types.User{ID: "u1", Email: "alice@example.com"},
			}, nil
		},
	}
	h := NewAuthHandler(&config.Config{
		Auth: &config.AuthConfig{RegistrationMode: config.AuthRegistrationModeInviteOnly},
	}, us, nil, nil, nil)

	w := doLogin(t, newLoginTestRouter(h), map[string]string{
		"email":    "alice@example.com",
		"password": "supersecret1",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("invite_only must still allow login, got %d body=%s", w.Code, w.Body.String())
	}
	if !called {
		t.Fatalf("UserService.Login must be called when registration_mode=invite_only")
	}
	if !bytes.Contains(w.Body.Bytes(), []byte(`"success":true`)) && !bytes.Contains(w.Body.Bytes(), []byte(`"success": true`)) {
		// dto may omit spaces; accept either. Prefer checking Token field.
		if !bytes.Contains(w.Body.Bytes(), []byte("access-token")) {
			t.Fatalf("expected successful login payload, body=%s", w.Body.String())
		}
	}
}
