package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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

// stubRegisterUserService is a UserService whose ONLY useful method is
// Register; every other call panics. Using an interface embedding plus a
// targeted override keeps the test focused on the Register handler's
// branching logic without dragging in the entire user service surface.
type stubRegisterUserService struct {
	interfaces.UserService
	register  func(ctx context.Context, req *types.RegisterRequest) (*types.User, error)
	listUsers func(ctx context.Context, offset, limit int) ([]*types.User, bool, error)
}

func (s *stubRegisterUserService) Register(ctx context.Context, req *types.RegisterRequest) (*types.User, error) {
	return s.register(ctx, req)
}

// ListAllUsers drives the fresh-deployment bypass (issue #1866). The
// default is a non-empty deployment so every pre-existing invite_only
// expectation (403) keeps its original meaning; tests targeting the
// bypass install a zero-user (or failing) override.
func (s *stubRegisterUserService) ListAllUsers(ctx context.Context, offset, limit int) ([]*types.User, bool, error) {
	if s.listUsers != nil {
		return s.listUsers(ctx, offset, limit)
	}
	return []*types.User{{ID: "existing"}}, false, nil
}

// errorCapture mirrors gin's default ErrorHandler behaviour for tests:
// when a handler calls c.Error(), we surface it as an HTTP response so the
// recorder reflects the real client-visible status. The production
// middleware does the same thing in middleware/error_handler.go.
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

// validRegisterBody returns a payload that passes parameter validation, so
// each test is exercising the gate logic and not the body parser.
func validRegisterBody() map[string]string {
	return map[string]string{
		"username": "alice",
		"email":    "alice@example.com",
		"password": "supersecret1",
	}
}

func TestRegister_InviteOnlyRejects(t *testing.T) {
	// PR 3 (#1303): when auth.registration_mode=invite_only, Register
	// must respond 403 BEFORE touching the user service. The frontend
	// already hides the sign-up link via /auth/config; this is the
	// server-side enforcement for direct API hits.
	called := false
	us := &stubRegisterUserService{
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

// TestRegister_FreshDeploymentAllowsFirstRegistration is the regression
// for #1866: DISABLE_REGISTRATION=true on a fresh deploy used to be a
// deadlock — invite_only blocked /auth/register while every other
// account-creation path needed an existing account. With zero accounts
// the gate now lets exactly one registration through so the operator can
// complete the documented bootstrap flow (sign up → set the env var →
// restart → promoted by bootstrapSystemAdmin, which still never creates
// users itself).
func TestRegister_FreshDeploymentAllowsFirstRegistration(t *testing.T) {
	called := false
	us := &stubRegisterUserService{
		listUsers: func(context.Context, int, int) ([]*types.User, bool, error) {
			return nil, false, nil // fresh deployment: no accounts yet
		},
		register: func(context.Context, *types.RegisterRequest) (*types.User, error) {
			called = true
			return &types.User{ID: "u1", Email: "alice@example.com"}, nil
		},
	}
	h := NewAuthHandler(&config.Config{
		Auth: &config.AuthConfig{RegistrationMode: config.AuthRegistrationModeInviteOnly},
	}, us, nil, nil, nil)

	w := doRegister(t, newRegisterTestRouter(h), validRegisterBody())
	if w.Code != http.StatusCreated {
		t.Fatalf("fresh deployment must allow the first registration, got %d body=%s", w.Code, w.Body.String())
	}
	if !called {
		t.Fatalf("UserService.Register should have been invoked on a fresh deployment")
	}
}

// The bypass is strictly a zero-users window: as soon as one account
// exists the closed gate rejects exactly as before.
func TestRegister_InviteOnlyStillRejectsWithExistingUsers(t *testing.T) {
	us := &stubRegisterUserService{
		listUsers: func(context.Context, int, int) ([]*types.User, bool, error) {
			return []*types.User{{ID: "existing"}}, false, nil
		},
	}
	h := NewAuthHandler(&config.Config{
		Auth: &config.AuthConfig{RegistrationMode: config.AuthRegistrationModeInviteOnly},
	}, us, nil, nil, nil)

	w := doRegister(t, newRegisterTestRouter(h), validRegisterBody())
	if w.Code != http.StatusForbidden {
		t.Fatalf("invite_only with existing accounts must still return 403, got %d body=%s", w.Code, w.Body.String())
	}
}

// An unknown user count must fail closed — a DB error can never open the
// registration gate.
func TestRegister_UserCountErrorFailsClosed(t *testing.T) {
	us := &stubRegisterUserService{
		listUsers: func(context.Context, int, int) ([]*types.User, bool, error) {
			return nil, false, errors.New("db unreachable")
		},
	}
	h := NewAuthHandler(&config.Config{
		Auth: &config.AuthConfig{RegistrationMode: config.AuthRegistrationModeInviteOnly},
	}, us, nil, nil, nil)

	w := doRegister(t, newRegisterTestRouter(h), validRegisterBody())
	if w.Code != http.StatusForbidden {
		t.Fatalf("user-count failure must fail closed with 403, got %d body=%s", w.Code, w.Body.String())
	}
}

// /auth/config must expose the effective first-registration state in
// lock-step with the Register gate (issue #1866), while registration_mode
// itself stays the operator's configured value.
func TestGetAuthConfigExposesFirstRegistrationOpen(t *testing.T) {
	gin.SetMode(gin.TestMode)
	serve := func(h *AuthHandler) map[string]any {
		r := gin.New()
		r.GET("/auth/config", h.GetAuthConfig)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/auth/config", nil))
		if w.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
		}
		var out map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatalf("unmarshal auth config: %v", err)
		}
		return out
	}
	newHandler := func(listUsers func(context.Context, int, int) ([]*types.User, bool, error)) *AuthHandler {
		return &AuthHandler{
			configInfo: &config.Config{Auth: &config.AuthConfig{
				RegistrationMode: config.AuthRegistrationModeInviteOnly,
			}},
			systemSettingSvc: &registrationModeSettings{mode: config.AuthRegistrationModeInviteOnly},
			userService:      &stubRegisterUserService{listUsers: listUsers},
		}
	}

	out := serve(newHandler(func(context.Context, int, int) ([]*types.User, bool, error) {
		return nil, false, nil
	}))
	if out["first_registration_open"] != true {
		t.Fatalf("fresh deployment must expose first_registration_open=true: %v", out)
	}
	if out["registration_mode"] != config.AuthRegistrationModeInviteOnly {
		t.Fatalf("registration_mode must stay the configured value: %v", out)
	}

	out = serve(newHandler(func(context.Context, int, int) ([]*types.User, bool, error) {
		return []*types.User{{ID: "existing"}}, false, nil
	}))
	if out["first_registration_open"] != false {
		t.Fatalf("deployment with accounts must expose first_registration_open=false: %v", out)
	}
}

func TestRegister_SelfServeAllowsRegistration(t *testing.T) {
	// Default registration_mode keeps PR 1 behaviour intact: the gate
	// is dormant and the request reaches the user service. We don't
	// exercise the real service here — just confirm the gate let it
	// through by observing the stub being invoked.
	called := false
	us := &stubRegisterUserService{
		register: func(_ context.Context, req *types.RegisterRequest) (*types.User, error) {
			called = true
			if req.TenantProvisioning != types.TenantProvisioningCreatePersonal {
				t.Fatalf("default provisioning = %q, want create_personal", req.TenantProvisioning)
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
	if w.Code != http.StatusCreated {
		t.Fatalf("nil Auth config must fall back to allow, got %d body=%s", w.Code, w.Body.String())
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

// Settings are resolved on every request, for the UI and both registration APIs.
type registrationModeSettings struct {
	interfaces.SystemSettingService
	mode string
}

func (s *registrationModeSettings) GetString(_ context.Context, key, _, fallback string) string {
	if key == "auth.registration_mode" {
		return s.mode
	}
	return fallback
}

func (s *registrationModeSettings) GetBool(_ context.Context, _, _ string, fallback bool) bool {
	return fallback
}

func TestRegistrationModeChangesApplyImmediately(t *testing.T) {
	settings := &registrationModeSettings{}
	h := &AuthHandler{
		configInfo: &config.Config{
			Auth: &config.AuthConfig{RegistrationMode: config.AuthRegistrationModeInviteOnly},
		},
		systemSettingSvc: settings,
		userService:      &invitedRegistrationUserService{},
		tenantService:    &invitedRegistrationTenantService{},
		invitationSvc:    &invitedRegistrationInvitationService{},
	}
	r := newRegisterTestRouter(h)
	r.POST("/auth/register-by-invite", h.RegisterByInvite)
	r.GET("/auth/config", h.GetAuthConfig)
	for _, mode := range []string{
		config.AuthRegistrationModeInviteRegister,
		config.AuthRegistrationModeSelfServe,
		config.AuthRegistrationModeInviteOnly,
	} {
		settings.mode = mode
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/auth/config", nil))
		var cfg struct {
			RegistrationMode string `json:"registration_mode"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &cfg); err != nil || cfg.RegistrationMode != mode {
			t.Fatalf("config must expose live mode %q: %s", mode, w.Body.String())
		}
		for _, path := range []string{"/auth/register", "/auth/register-by-invite"} {
			body := validRegisterBody()
			body["token"] = "valid-token"
			data, _ := json.Marshal(body)
			req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(data))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			want := http.StatusForbidden
			if mode == config.AuthRegistrationModeSelfServe ||
				(mode == config.AuthRegistrationModeInviteRegister && path == "/auth/register-by-invite") {
				want = http.StatusCreated
			}
			if w.Code != want {
				t.Fatalf("mode=%s path=%s status=%d want=%d body=%s", mode, path, w.Code, want, w.Body.String())
			}
		}
	}
}
