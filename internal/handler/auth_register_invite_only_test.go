package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/config"
	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
)

// stubRegisterUserService is a UserService whose ONLY useful methods are
// Register / CountUsers / ListSystemAdmins / UpdateUser; every other call
// panics via the embedded nil interface.
type stubRegisterUserService struct {
	interfaces.UserService
	userCount    int64
	adminCount   int64
	register     func(ctx context.Context, req *types.RegisterRequest) (*types.User, error)
	updatedAdmin bool
	lastUpdated  *types.User
}

func (s *stubRegisterUserService) Register(ctx context.Context, req *types.RegisterRequest) (*types.User, error) {
	return s.register(ctx, req)
}

func (s *stubRegisterUserService) CountUsers(context.Context) (int64, error) {
	return s.userCount, nil
}

func (s *stubRegisterUserService) ListSystemAdmins(
	context.Context, int, int,
) ([]*types.User, int64, error) {
	return nil, s.adminCount, nil
}

func (s *stubRegisterUserService) UpdateUser(_ context.Context, user *types.User) error {
	s.updatedAdmin = user != nil && user.IsSystemAdmin
	s.lastUpdated = user
	return nil
}

func errorCapture() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Next()
		if len(c.Errors) == 0 {
			return
		}
		err := c.Errors.Last().Err
		if appErr, ok := err.(*apperrors.AppError); ok {
			c.JSON(appErr.HTTPCode, gin.H{"error": appErr.Message})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
	}
}

func newRegisterTestRouter(h *AuthHandler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(errorCapture())
	r.POST("/auth/register", h.Register)
	r.GET("/auth/config", h.GetAuthConfig)
	return r
}

func doRegister(t *testing.T, r *gin.Engine, body any) *httptest.ResponseRecorder {
	t.Helper()
	buf, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/auth/register", bytes.NewReader(buf))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func validRegisterBody() map[string]string {
	return map[string]string{
		"username": "alice",
		"email":    "alice@example.com",
		"password": "supersecret1",
	}
}

func TestRegister_InviteOnlyRejects(t *testing.T) {
	called := false
	us := &stubRegisterUserService{
		userCount: 1, // not empty install
		register: func(context.Context, *types.RegisterRequest) (*types.User, error) {
			called = true
			return &types.User{ID: "u1"}, nil
		},
	}
	h := NewAuthHandler(&config.Config{
		Auth: &config.AuthConfig{RegistrationMode: config.AuthRegistrationModeInviteOnly},
	}, us, nil, nil, nil)

	w := doRegister(t, newRegisterTestRouter(h), validRegisterBody())
	if w.Code != http.StatusForbidden {
		t.Fatalf("invite_only must return 403, got %d body=%s", w.Code, w.Body.String())
	}
	if called {
		t.Fatalf("UserService.Register must not be called when invite_only blocks the request")
	}
}

func TestRegister_InviteOnlyAllowsEmptyInstallBootstrap(t *testing.T) {
	called := false
	us := &stubRegisterUserService{
		userCount:  0,
		adminCount: 0,
		register: func(_ context.Context, req *types.RegisterRequest) (*types.User, error) {
			called = true
			if req.TenantProvisioning != types.TenantProvisioningCreatePersonal {
				t.Fatalf("bootstrap provisioning = %q, want create_personal", req.TenantProvisioning)
			}
			return &types.User{ID: "u1", Email: "alice@example.com"}, nil
		},
	}
	h := NewAuthHandler(&config.Config{
		Auth: &config.AuthConfig{
			RegistrationMode:  config.AuthRegistrationModeInviteOnly,
			DefaultTenantMode: config.AuthDefaultTenantModeTenantless,
		},
	}, us, nil, nil, nil)

	w := doRegister(t, newRegisterTestRouter(h), validRegisterBody())
	if w.Code != http.StatusCreated {
		t.Fatalf("empty install must allow bootstrap register, got %d body=%s", w.Code, w.Body.String())
	}
	if !called {
		t.Fatal("UserService.Register should have been invoked")
	}
	if !us.updatedAdmin || us.lastUpdated == nil || !us.lastUpdated.IsSystemAdmin {
		t.Fatal("bootstrap first user must be promoted to system admin")
	}
}

func TestGetAuthConfig_EmptyInstallReportsSelfServe(t *testing.T) {
	us := &stubRegisterUserService{userCount: 0}
	h := NewAuthHandler(&config.Config{
		Auth: &config.AuthConfig{RegistrationMode: config.AuthRegistrationModeInviteOnly},
	}, us, nil, nil, nil)
	r := newRegisterTestRouter(h)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/auth/config", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["registration_mode"] != config.AuthRegistrationModeSelfServe {
		t.Fatalf("empty install config mode=%v, want self_serve", payload["registration_mode"])
	}
}

func TestRegister_SelfServeAllowsRegistration(t *testing.T) {
	called := false
	us := &stubRegisterUserService{
		userCount: 1,
		register: func(_ context.Context, req *types.RegisterRequest) (*types.User, error) {
			called = true
			if req.TenantProvisioning != types.TenantProvisioningTenantless {
				t.Fatalf("default provisioning = %q, want tenantless", req.TenantProvisioning)
			}
			return &types.User{ID: "u1", Email: "alice@example.com"}, nil
		},
	}
	h := NewAuthHandler(&config.Config{
		Auth: &config.AuthConfig{RegistrationMode: config.AuthRegistrationModeSelfServe},
	}, us, nil, nil, nil)

	w := doRegister(t, newRegisterTestRouter(h), validRegisterBody())
	if w.Code != http.StatusCreated {
		t.Fatalf("self_serve must allow registration, got %d body=%s", w.Code, w.Body.String())
	}
	if !called {
		t.Fatalf("UserService.Register should have been invoked")
	}
}

func TestRegister_TenantlessProvisioningFromConfig(t *testing.T) {
	us := &stubRegisterUserService{
		userCount: 1,
		register: func(_ context.Context, req *types.RegisterRequest) (*types.User, error) {
			if req.TenantProvisioning != types.TenantProvisioningTenantless {
				t.Fatalf("provisioning = %q, want tenantless", req.TenantProvisioning)
			}
			return &types.User{ID: "u1", Email: "alice@example.com"}, nil
		},
	}
	h := NewAuthHandler(&config.Config{
		Auth: &config.AuthConfig{
			RegistrationMode:  config.AuthRegistrationModeSelfServe,
			DefaultTenantMode: config.AuthDefaultTenantModeTenantless,
		},
	}, us, nil, nil, nil)

	w := doRegister(t, newRegisterTestRouter(h), validRegisterBody())
	if w.Code != http.StatusCreated {
		t.Fatalf("tenantless self-serve registration got %d body=%s", w.Code, w.Body.String())
	}
}

func TestGetAuthConfigExposesComplexPassword(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := &AuthHandler{
		configInfo: &config.Config{Auth: &config.AuthConfig{
			RegistrationMode: config.AuthRegistrationModeSelfServe,
		}},
		systemSettingSvc: &tenantPolicySettingService{enabled: true},
	}
	r := gin.New()
	r.GET("/auth/config", h.GetAuthConfig)
	req := httptest.NewRequest(http.MethodGet, "/auth/config", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"complex_password_enabled":true`) {
		t.Fatalf("body=%s, want complex_password_enabled true", w.Body.String())
	}
}

func TestRegister_NilAuthConfigDoesNotPanic(t *testing.T) {
	// Defensive: a nil Auth section means the operator hasn't set the
	// registration mode at all, which must not crash and must keep the
	// legacy "registration enabled" behaviour. Mirrors the nil guard in
	// the handler so a config-loading bug doesn't take the server down.
	us := &stubRegisterUserService{
		register: func(_ context.Context, _ *types.RegisterRequest) (*types.User, error) {
			return &types.User{ID: "u1", Email: "alice@example.com"}, nil
		},
	}
	h := NewAuthHandler(&config.Config{}, us, nil, nil, nil)

	w := doRegister(t, newRegisterTestRouter(h), validRegisterBody())
	// TreeRAG defaults nil Auth to invite_only; empty user store still allows
	// bootstrap registration (same path as self_serve when no users exist).
	if w.Code != http.StatusCreated && w.Code != http.StatusForbidden {
		t.Fatalf("nil Auth config must not panic, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestRegister_NilAuthConfigBlocksWhenUsersExist(t *testing.T) {
	// Nil Auth falls back to invite_only; with existing users, public
	// registration must be rejected.
	called := false
	us := &stubRegisterUserService{
		userCount: 1,
		register: func(_ context.Context, _ *types.RegisterRequest) (*types.User, error) {
			called = true
			return &types.User{ID: "u1"}, nil
		},
	}
	h := NewAuthHandler(&config.Config{}, us, nil, nil, nil)

	w := doRegister(t, newRegisterTestRouter(h), validRegisterBody())
	if w.Code != http.StatusForbidden {
		t.Fatalf("nil Auth with existing users must be invite_only, got %d body=%s", w.Code, w.Body.String())
	}
	if called {
		t.Fatal("Register must not be called")
	}
}

// TestRegister_PreservesPasswordBytes is the regression for #2521.
// SanitizeForLog is for log output only; applying it to credentials
// rewrites tabs/newlines/control chars before hashing, so a password
// that registers successfully cannot be used at login.
func TestRegister_PreservesPasswordBytes(t *testing.T) {
	// Password contains a tab and a newline — both are rewritten by
	// SanitizeForLog (tab → space, newline → space). The handler must
	// pass the exact original string to UserService.Register.
	const originalPassword = "abc\t123!\nX"

	var gotPassword string
	us := &stubRegisterUserService{
		register: func(_ context.Context, req *types.RegisterRequest) (*types.User, error) {
			gotPassword = req.Password
			return &types.User{ID: "u1", Email: "alice@example.com"}, nil
		},
	}
	h := NewAuthHandler(&config.Config{
		Auth: &config.AuthConfig{RegistrationMode: config.AuthRegistrationModeSelfServe},
	}, us, nil, nil, nil)

	body := validRegisterBody()
	body["password"] = originalPassword
	w := doRegister(t, newRegisterTestRouter(h), body)
	if w.Code != http.StatusCreated {
		t.Fatalf("registration with control chars in password must succeed, got %d body=%s",
			w.Code, w.Body.String())
	}
	if gotPassword != originalPassword {
		t.Fatalf("password mutated before UserService.Register:\n  got  %q\n  want %q",
			gotPassword, originalPassword)
	}
}
