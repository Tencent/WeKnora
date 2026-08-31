package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	apprepo "github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
)

type bootstrapUserServiceStub struct {
	interfaces.UserService

	user         *types.User
	lookupErr    error
	adminTotal   int64
	registerReq  *types.RegisterRequest
	updatedUser  *types.User
	loginInvoked bool
	validateErr  error
}

func (s *bootstrapUserServiceStub) GetUserByEmail(context.Context, string) (*types.User, error) {
	return s.user, s.lookupErr
}

func (s *bootstrapUserServiceStub) ListSystemAdmins(context.Context, int, int) ([]*types.User, int64, error) {
	return nil, s.adminTotal, nil
}

func (s *bootstrapUserServiceStub) Register(_ context.Context, req *types.RegisterRequest) (*types.User, error) {
	s.registerReq = req
	if s.user == nil {
		s.user = &types.User{ID: "u-bootstrap", Username: req.Username, Email: req.Email, IsActive: true}
	}
	return s.user, nil
}

func (s *bootstrapUserServiceStub) ValidatePassword(context.Context, string, string) error {
	return s.validateErr
}

func (s *bootstrapUserServiceStub) UpdateUser(_ context.Context, user *types.User) error {
	s.updatedUser = user
	return nil
}

func (s *bootstrapUserServiceStub) Login(_ context.Context, _ *types.LoginRequest) (*types.LoginResponse, error) {
	s.loginInvoked = true
	return &types.LoginResponse{
		Success: true,
		User:    s.updatedUser,
		Token:   "access-token",
	}, nil
}

func newBootstrapTestRouter(h *AuthHandler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(errorCapture())
	r.POST("/auth/bootstrap", h.BootstrapSystemAdmin)
	r.GET("/auth/config", h.GetAuthConfig)
	return r
}

func doBootstrap(t *testing.T, r *gin.Engine, body any) *httptest.ResponseRecorder {
	t.Helper()
	payload, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal bootstrap body: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/auth/bootstrap", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestBootstrapSystemAdminCreatesAccountWhenRegistrationIsDisabled(t *testing.T) {
	t.Setenv(bootstrapSystemAdminEmailEnv, "admin@example.com")
	stub := &bootstrapUserServiceStub{lookupErr: apprepo.ErrUserNotFound}
	h := NewAuthHandler(&config.Config{
		Auth: &config.AuthConfig{
			RegistrationMode: config.AuthRegistrationModeInviteOnly,
		},
	}, stub, nil, nil, nil)

	w := doBootstrap(t, newBootstrapTestRouter(h), map[string]string{
		"username": "admin",
		"email":    "ADMIN@example.com",
		"password": "secret123",
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("bootstrap create status = %d, body=%s", w.Code, w.Body.String())
	}
	if stub.registerReq == nil {
		t.Fatal("bootstrap did not create the configured account")
	}
	if stub.registerReq.TenantProvisioning != types.TenantProvisioningCreatePersonal {
		t.Fatalf("tenant provisioning = %q, want create_personal", stub.registerReq.TenantProvisioning)
	}
	if stub.updatedUser == nil || !stub.updatedUser.IsSystemAdmin {
		t.Fatalf("updated user = %+v, want system administrator", stub.updatedUser)
	}
	if !stub.loginInvoked {
		t.Fatal("bootstrap did not return a login response")
	}
}

func TestBootstrapSystemAdminPromotesExistingAccountAfterPasswordCheck(t *testing.T) {
	t.Setenv(bootstrapSystemAdminEmailEnv, "admin@example.com")
	user := &types.User{ID: "u-existing", Email: "admin@example.com", IsActive: true}
	stub := &bootstrapUserServiceStub{user: user}
	h := NewAuthHandler(&config.Config{}, stub, nil, nil, nil)

	w := doBootstrap(t, newBootstrapTestRouter(h), map[string]string{
		"username": "ignored-for-existing-user",
		"email":    "admin@example.com",
		"password": "secret123",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("bootstrap promotion status = %d, body=%s", w.Code, w.Body.String())
	}
	if stub.registerReq != nil {
		t.Fatal("existing account must not be registered again")
	}
	if stub.updatedUser != user || !user.IsSystemAdmin {
		t.Fatalf("existing user after promotion = %+v", user)
	}
}

func TestBootstrapSystemAdminDoesNotPromoteInactiveAccount(t *testing.T) {
	t.Setenv(bootstrapSystemAdminEmailEnv, "admin@example.com")
	user := &types.User{ID: "u-inactive", Email: "admin@example.com", IsActive: false}
	stub := &bootstrapUserServiceStub{user: user}
	h := NewAuthHandler(&config.Config{}, stub, nil, nil, nil)

	w := doBootstrap(t, newBootstrapTestRouter(h), map[string]string{
		"username": "admin",
		"email":    "admin@example.com",
		"password": "secret123",
	})
	if w.Code != http.StatusForbidden {
		t.Fatalf("inactive account status = %d, body=%s", w.Code, w.Body.String())
	}
	if stub.updatedUser != nil || user.IsSystemAdmin {
		t.Fatalf("inactive account was promoted: %+v", user)
	}
}

func TestBootstrapSystemAdminRejectsWrongEmailAndExistingAdmin(t *testing.T) {
	t.Setenv(bootstrapSystemAdminEmailEnv, "admin@example.com")
	stub := &bootstrapUserServiceStub{lookupErr: apprepo.ErrUserNotFound}
	h := NewAuthHandler(&config.Config{}, stub, nil, nil, nil)
	r := newBootstrapTestRouter(h)

	w := doBootstrap(t, r, map[string]string{
		"username": "other",
		"email":    "other@example.com",
		"password": "secret123",
	})
	if w.Code != http.StatusForbidden {
		t.Fatalf("wrong email status = %d, body=%s", w.Code, w.Body.String())
	}
	if stub.registerReq != nil {
		t.Fatal("wrong email must not reach registration")
	}

	stub.adminTotal = 1
	w = doBootstrap(t, r, map[string]string{
		"username": "admin",
		"email":    "admin@example.com",
		"password": "secret123",
	})
	if w.Code != http.StatusConflict {
		t.Fatalf("existing admin status = %d, body=%s", w.Code, w.Body.String())
	}
}

func TestGetAuthConfigReportsBootstrapAvailability(t *testing.T) {
	t.Setenv(bootstrapSystemAdminEmailEnv, "admin@example.com")
	stub := &bootstrapUserServiceStub{lookupErr: errors.New("user not found")}
	h := NewAuthHandler(&config.Config{}, stub, nil, nil, nil)
	r := newBootstrapTestRouter(h)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/auth/config", nil)
	r.ServeHTTP(w, req)
	var payload map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode auth config: %v", err)
	}
	if payload["bootstrap_available"] != true {
		t.Fatalf("bootstrap_available = %#v, want true", payload["bootstrap_available"])
	}

	stub.adminTotal = 1
	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/auth/config", nil)
	r.ServeHTTP(w, req)
	payload = map[string]any{}
	if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode auth config after admin: %v", err)
	}
	if payload["bootstrap_available"] != false {
		t.Fatalf("bootstrap_available with existing admin = %#v, want false", payload["bootstrap_available"])
	}
}
