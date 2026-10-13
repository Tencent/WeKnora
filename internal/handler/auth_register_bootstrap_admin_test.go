package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/application/service"
	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/middleware"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"

	"github.com/gin-gonic/gin"
)

// bootstrapRegisterUserService records the UserService calls Register makes
// while deciding whether a closed deployment may create its first admin.
// GetUserByEmail is case-sensitive, matching the repository.
// GetUserByEmailCaseInsensitive matches the LOWER(email) occupancy check.
type bootstrapRegisterUserService struct {
	interfaces.UserService

	mu            sync.Mutex
	adminTotal    int64
	listErr       error
	listCalls     int
	recheckTotal  int64
	recheckSet    bool
	recheckErr    error
	existing      *types.User
	lookupErr     error
	registerCalls int
	registerErr   error
	registered    *types.RegisterRequest
	registerDelay time.Duration
	inRegister    atomic.Int32
	maxInRegister atomic.Int32
	updated       *types.User
	updateErr     error
}

func (s *bootstrapRegisterUserService) GetUserByEmail(_ context.Context, email string) (*types.User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.existing != nil && s.existing.Email == email {
		return s.existing, nil
	}
	if s.lookupErr != nil {
		return nil, s.lookupErr
	}
	return nil, service.ErrUserNotFound
}

func (s *bootstrapRegisterUserService) GetUserByEmailCaseInsensitive(
	_ context.Context, email string,
) (*types.User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.existing != nil && strings.EqualFold(strings.TrimSpace(s.existing.Email), strings.TrimSpace(email)) {
		return s.existing, nil
	}
	if s.lookupErr != nil {
		return nil, s.lookupErr
	}
	return nil, service.ErrUserNotFound
}

func (s *bootstrapRegisterUserService) ListSystemAdmins(context.Context, int, int) ([]*types.User, int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.listCalls++
	// The second call is the re-check immediately before create. Tests leave
	// recheckSet false so every call keeps the original adminTotal/listErr.
	if s.listCalls > 1 {
		if s.recheckErr != nil {
			return nil, 0, s.recheckErr
		}
		if s.recheckSet {
			return nil, s.recheckTotal, nil
		}
	}
	if s.listErr != nil {
		return nil, 0, s.listErr
	}
	return nil, s.adminTotal, nil
}

func (s *bootstrapRegisterUserService) Register(_ context.Context, req *types.RegisterRequest) (*types.User, error) {
	cur := s.inRegister.Add(1)
	defer s.inRegister.Add(-1)
	for {
		old := s.maxInRegister.Load()
		if cur <= old || s.maxInRegister.CompareAndSwap(old, cur) {
			break
		}
	}
	if s.registerDelay > 0 {
		time.Sleep(s.registerDelay)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.registerCalls++
	if s.registerErr != nil {
		return nil, s.registerErr
	}
	copied := *req
	s.registered = &copied
	user := &types.User{
		ID:            "u-bootstrap",
		Username:      req.Username,
		Email:         req.Email,
		IsActive:      true,
		IsSystemAdmin: req.BootstrapSystemAdmin,
	}
	if user.IsSystemAdmin {
		s.adminTotal++
	}
	s.existing = user
	return user, nil
}

func (s *bootstrapRegisterUserService) UpdateUser(_ context.Context, user *types.User) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.updateErr != nil {
		return s.updateErr
	}
	s.updated = user
	if user != nil && user.IsSystemAdmin {
		s.adminTotal++
	}
	return nil
}

func inviteOnlyHandler(us *bootstrapRegisterUserService) *AuthHandler {
	return NewAuthHandler(&config.Config{
		Auth: &config.AuthConfig{RegistrationMode: config.AuthRegistrationModeInviteOnly},
	}, us, nil, nil, nil)
}

func bootstrapRegisterBody(email string) map[string]string {
	body := validRegisterBody()
	body["email"] = email
	return body
}

func TestRegister_DisabledRegistrationAllowsConfiguredBootstrapAdmin(t *testing.T) {
	// Issue #1866: with registration closed, the configured bootstrap email
	// must be able to sign up once and become the system administrator.
	// cmd/server/bootstrap.go only promotes an account that already exists,
	// and Register rejects every public sign-up before that account can exist.
	t.Setenv(bootstrapSystemAdminEmailEnv, "  admin@example.com  ")
	us := &bootstrapRegisterUserService{lookupErr: service.ErrUserNotFound}
	h := inviteOnlyHandler(us)

	w := doRegister(t, newRegisterTestRouter(h), bootstrapRegisterBody("  ADMIN@example.com "))
	if w.Code != http.StatusCreated {
		t.Fatalf("bootstrap email must be created, got %d body=%s", w.Code, w.Body.String())
	}
	if us.registerCalls != 1 || us.registered == nil {
		t.Fatalf("Register calls = %d, req = %+v", us.registerCalls, us.registered)
	}
	// Stored email is the trimmed env value so restart-time promotion
	// (exact match after TrimSpace) finds the same row.
	if us.registered.Email != "admin@example.com" {
		t.Fatalf("stored email = %q, want trimmed configured email", us.registered.Email)
	}
	if us.updated != nil {
		t.Fatalf("system administrator flag must be set on create, UpdateUser=%+v", us.updated)
	}
	if us.registered == nil || !us.registered.BootstrapSystemAdmin {
		t.Fatalf("create request = %+v, want BootstrapSystemAdmin", us.registered)
	}
	var resp types.RegisterResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v body=%s", err, w.Body.String())
	}
	if resp.User == nil || !resp.User.IsSystemAdmin || resp.User.Email != "admin@example.com" {
		t.Fatalf("response user = %+v, want promoted admin@example.com", resp.User)
	}

	again := doRegister(t, newRegisterTestRouter(h), bootstrapRegisterBody("admin@example.com"))
	if again.Code != http.StatusForbidden {
		t.Fatalf("second bootstrap signup status = %d, want 403 body=%s", again.Code, again.Body.String())
	}
	if us.registerCalls != 1 {
		t.Fatalf("second signup must not create another user, Register calls = %d", us.registerCalls)
	}
}

func TestRegister_DisabledRegistrationRejectsOtherEmailsWhenBootstrapConfigured(t *testing.T) {
	t.Setenv(bootstrapSystemAdminEmailEnv, "admin@example.com")
	us := &bootstrapRegisterUserService{lookupErr: service.ErrUserNotFound}
	h := inviteOnlyHandler(us)

	w := doRegister(t, newRegisterTestRouter(h), bootstrapRegisterBody("other@example.com"))
	if w.Code != http.StatusForbidden {
		t.Fatalf("other email status = %d, want 403 body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "Public registration is disabled") {
		t.Fatalf("body = %s", w.Body.String())
	}
	if us.registerCalls != 0 || us.updated != nil {
		t.Fatalf("other email must not register or promote: calls=%d updated=%+v", us.registerCalls, us.updated)
	}
}

func TestRegister_BootstrapAdminRejectedWhenUnsetOrBlank(t *testing.T) {
	for _, envVal := range []string{"", "   ", "\t"} {
		t.Run(envVal, func(t *testing.T) {
			t.Setenv(bootstrapSystemAdminEmailEnv, envVal)
			us := &bootstrapRegisterUserService{lookupErr: service.ErrUserNotFound}
			w := doRegister(t, newRegisterTestRouter(inviteOnlyHandler(us)), bootstrapRegisterBody("admin@example.com"))
			if w.Code != http.StatusForbidden {
				t.Fatalf("status = %d, want 403 body=%s", w.Code, w.Body.String())
			}
			if us.registerCalls != 0 {
				t.Fatalf("Register calls = %d, want 0", us.registerCalls)
			}
		})
	}
}

func TestRegister_BootstrapAdminRejectedWhenSystemAdminExists(t *testing.T) {
	t.Setenv(bootstrapSystemAdminEmailEnv, "admin@example.com")
	us := &bootstrapRegisterUserService{
		lookupErr:  service.ErrUserNotFound,
		adminTotal: 1,
	}
	w := doRegister(t, newRegisterTestRouter(inviteOnlyHandler(us)), bootstrapRegisterBody("admin@example.com"))
	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 body=%s", w.Code, w.Body.String())
	}
	if us.registerCalls != 0 || us.updated != nil {
		t.Fatalf("existing admin must block signup: calls=%d updated=%+v", us.registerCalls, us.updated)
	}
}

func TestRegister_BootstrapAdminRejectedWhenAdminLookupFails(t *testing.T) {
	t.Setenv(bootstrapSystemAdminEmailEnv, "admin@example.com")
	us := &bootstrapRegisterUserService{
		lookupErr: service.ErrUserNotFound,
		listErr:   context.DeadlineExceeded,
	}
	w := doRegister(t, newRegisterTestRouter(inviteOnlyHandler(us)), bootstrapRegisterBody("admin@example.com"))
	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 body=%s", w.Code, w.Body.String())
	}
	if us.registerCalls != 0 {
		t.Fatalf("failed admin lookup must fail closed, Register calls = %d", us.registerCalls)
	}
}

func TestRegister_BootstrapAdminDoesNotReplaceExistingAccount(t *testing.T) {
	// An account whose email matches LOWER(email) is not a new sign-up.
	// Inactive rows are included. The existing row is not updated; startup
	// promotion remains an exact match on the trimmed env value.
	for _, tc := range []struct {
		name    string
		active  bool
		env     string
		stored  string
		request string
	}{
		{
			name: "inactive canonical", active: false,
			env: "admin@example.com", stored: "admin@example.com", request: "admin@example.com",
		},
		{
			name: "active canonical", active: true,
			env: "admin@example.com", stored: "admin@example.com", request: "admin@example.com",
		},
		{
			name: "active request casing", active: true,
			env: "admin@example.com", stored: "Admin@Example.com", request: "Admin@Example.com",
		},
		// Stored casing matches neither the trimmed env value nor the request.
		{
			name: "inactive third casing", active: false,
			env: "admin@example.com", stored: "Admin@example.com", request: "ADMIN@example.com",
		},
		{
			name: "active third casing", active: true,
			env: "admin@example.com", stored: "Admin@example.com", request: "admin@example.com",
		},
		// Invite registration stores a lowercased email. A mixed-case env value
		// must still see that row.
		{
			name: "inactive lowercased invite", active: false,
			env: "Admin@example.com", stored: "admin@example.com", request: "Admin@example.com",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(bootstrapSystemAdminEmailEnv, tc.env)
			existing := &types.User{ID: "u-existing", Email: tc.stored, IsActive: tc.active}
			us := &bootstrapRegisterUserService{existing: existing}
			w := doRegister(t, newRegisterTestRouter(inviteOnlyHandler(us)), bootstrapRegisterBody(tc.request))
			if w.Code != http.StatusForbidden {
				t.Fatalf("status = %d, want 403 body=%s", w.Code, w.Body.String())
			}
			if us.registerCalls != 0 || us.updated != nil || existing.IsSystemAdmin {
				t.Fatalf("existing account was mutated: calls=%d updated=%+v admin=%v",
					us.registerCalls, us.updated, existing.IsSystemAdmin)
			}
		})
	}
}

func TestRegister_BootstrapAdminLookupErrorFailsClosed(t *testing.T) {
	t.Setenv(bootstrapSystemAdminEmailEnv, "admin@example.com")
	us := &bootstrapRegisterUserService{lookupErr: context.DeadlineExceeded}
	w := doRegister(t, newRegisterTestRouter(inviteOnlyHandler(us)), bootstrapRegisterBody("admin@example.com"))
	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 body=%s", w.Code, w.Body.String())
	}
	if us.registerCalls != 0 {
		t.Fatalf("lookup error must fail closed, Register calls = %d", us.registerCalls)
	}
}

func TestRegister_BootstrapAdminConcurrentSignupsAllowOne(t *testing.T) {
	t.Setenv(bootstrapSystemAdminEmailEnv, "admin@example.com")
	us := &bootstrapRegisterUserService{
		lookupErr:     service.ErrUserNotFound,
		registerDelay: 150 * time.Millisecond,
	}
	router := newRegisterTestRouter(inviteOnlyHandler(us))

	var wg sync.WaitGroup
	codes := make([]int, 2)
	start := make(chan struct{})
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			w := doRegister(t, router, bootstrapRegisterBody("admin@example.com"))
			codes[i] = w.Code
		}(i)
	}
	close(start)
	wg.Wait()

	var created, forbidden int
	for _, code := range codes {
		switch code {
		case http.StatusCreated:
			created++
		case http.StatusForbidden:
			forbidden++
		default:
			t.Fatalf("unexpected status %d (codes=%v)", code, codes)
		}
	}
	if created != 1 || forbidden != 1 {
		t.Fatalf("codes=%v, want one 201 and one 403", codes)
	}
	if us.registerCalls != 1 {
		t.Fatalf("Register calls = %d, want 1 (max in Register = %d)", us.registerCalls, us.maxInRegister.Load())
	}
	if us.maxInRegister.Load() != 1 {
		t.Fatalf("overlapping Register calls = %d, want 1", us.maxInRegister.Load())
	}
	if us.adminTotal != 1 || us.registered == nil || !us.registered.BootstrapSystemAdmin || us.updated != nil {
		t.Fatalf("adminTotal=%d registered=%+v updated=%+v", us.adminTotal, us.registered, us.updated)
	}
}

func TestRegister_SelfServeDoesNotPromoteBootstrapEmail(t *testing.T) {
	t.Setenv(bootstrapSystemAdminEmailEnv, "admin@example.com")
	us := &bootstrapRegisterUserService{lookupErr: service.ErrUserNotFound}
	h := NewAuthHandler(&config.Config{
		Auth: &config.AuthConfig{RegistrationMode: config.AuthRegistrationModeSelfServe},
	}, us, nil, nil, nil)

	w := doRegister(t, newRegisterTestRouter(h), bootstrapRegisterBody("ADMIN@example.com"))
	if w.Code != http.StatusCreated {
		t.Fatalf("self_serve status = %d, want 201 body=%s", w.Code, w.Body.String())
	}
	if us.registered == nil || us.registered.Email != "ADMIN@example.com" {
		t.Fatalf("self_serve must keep the submitted email, got %+v", us.registered)
	}
	if us.updated != nil {
		t.Fatalf("self_serve must not promote, updated=%+v", us.updated)
	}
}

func TestRegister_InviteRegisterModeAllowsOnlyBootstrapEmail(t *testing.T) {
	t.Setenv(bootstrapSystemAdminEmailEnv, "admin@example.com")
	us := &bootstrapRegisterUserService{lookupErr: service.ErrUserNotFound}
	h := NewAuthHandler(&config.Config{
		Auth: &config.AuthConfig{RegistrationMode: config.AuthRegistrationModeInviteRegister},
	}, us, nil, nil, nil)
	router := newRegisterTestRouter(h)

	denied := doRegister(t, router, bootstrapRegisterBody("other@example.com"))
	if denied.Code != http.StatusForbidden || us.registerCalls != 0 {
		t.Fatalf("other email status=%d calls=%d body=%s", denied.Code, us.registerCalls, denied.Body.String())
	}
	allowed := doRegister(t, router, bootstrapRegisterBody("admin@example.com"))
	if allowed.Code != http.StatusCreated {
		t.Fatalf("bootstrap email status = %d, want 201 body=%s", allowed.Code, allowed.Body.String())
	}
	if us.registered == nil || !us.registered.BootstrapSystemAdmin || us.updated != nil {
		t.Fatalf("registered=%+v updated=%+v, want flag on create and no UpdateUser", us.registered, us.updated)
	}
}

func TestRegister_BootstrapAdminStillValidatesPassword(t *testing.T) {
	t.Setenv(bootstrapSystemAdminEmailEnv, "admin@example.com")
	us := &bootstrapRegisterUserService{lookupErr: service.ErrUserNotFound}
	body := bootstrapRegisterBody("admin@example.com")
	// Passes the binding min of 6 and fails the 8-character password policy.
	body["password"] = "short1"
	w := doRegister(t, newRegisterTestRouter(inviteOnlyHandler(us)), body)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 body=%s", w.Code, w.Body.String())
	}
	if us.registerCalls != 0 || us.updated != nil {
		t.Fatalf("invalid password must not create or promote: calls=%d updated=%+v", us.registerCalls, us.updated)
	}
}

func TestRegister_BootstrapAdminIsCreatedAsSystemAdmin(t *testing.T) {
	// Promotion is the insert. A later UpdateUser failure must not be
	// required, and must not be able to leave a non-admin account behind.
	t.Setenv(bootstrapSystemAdminEmailEnv, "admin@example.com")
	us := &bootstrapRegisterUserService{
		lookupErr: service.ErrUserNotFound,
		updateErr: context.DeadlineExceeded,
	}
	w := doRegister(t, newRegisterTestRouter(inviteOnlyHandler(us)), bootstrapRegisterBody("admin@example.com"))
	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 body=%s", w.Code, w.Body.String())
	}
	if us.registerCalls != 1 || us.updated != nil {
		t.Fatalf("calls=%d updated=%+v, want one create and no UpdateUser", us.registerCalls, us.updated)
	}
	if us.registered == nil || !us.registered.BootstrapSystemAdmin {
		t.Fatalf("create request = %+v, want BootstrapSystemAdmin", us.registered)
	}
	var resp types.RegisterResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v body=%s", err, w.Body.String())
	}
	if resp.User == nil || !resp.User.IsSystemAdmin {
		t.Fatalf("response user = %+v, want system administrator", resp.User)
	}
}

func TestRegister_DisabledRegistrationRejectsMalformedBody(t *testing.T) {
	t.Setenv(bootstrapSystemAdminEmailEnv, "admin@example.com")
	us := &bootstrapRegisterUserService{lookupErr: service.ErrUserNotFound}
	router := newRegisterTestRouter(inviteOnlyHandler(us))
	req := httptest.NewRequest(http.MethodPost, "/auth/register", bytes.NewBufferString("{"))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("malformed body status = %d, want 403 body=%s", w.Code, w.Body.String())
	}
	if us.registerCalls != 0 {
		t.Fatalf("malformed body must not register, calls=%d", us.registerCalls)
	}
}

func TestRegister_DBSelfServeDoesNotUseBootstrapException(t *testing.T) {
	// A system setting of self_serve wins over an invite_only config value.
	// The bootstrap exception must not promote that ordinary registration.
	t.Setenv(bootstrapSystemAdminEmailEnv, "admin@example.com")
	us := &bootstrapRegisterUserService{lookupErr: service.ErrUserNotFound}
	h := &AuthHandler{
		configInfo: &config.Config{
			Auth: &config.AuthConfig{RegistrationMode: config.AuthRegistrationModeInviteOnly},
		},
		systemSettingSvc: &registrationModeSettings{mode: config.AuthRegistrationModeSelfServe},
		userService:      us,
	}
	w := doRegister(t, newRegisterTestRouter(h), bootstrapRegisterBody("admin@example.com"))
	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 body=%s", w.Code, w.Body.String())
	}
	if us.updated != nil {
		t.Fatalf("live self_serve must not promote, updated=%+v", us.updated)
	}
}

func closedRegistrationRouter(h *AuthHandler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(middleware.ErrorHandler())
	r.POST("/auth/register", h.Register)
	return r
}

type closedRegisterErrorBody struct {
	Success bool `json:"success"`
	Error   struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Details any    `json:"details"`
	} `json:"error"`
}

func decodeClosedRegisterError(t *testing.T, w *httptest.ResponseRecorder) closedRegisterErrorBody {
	t.Helper()
	var body closedRegisterErrorBody
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error body: %v body=%s", err, w.Body.String())
	}
	return body
}

func TestRegister_ClosedRegistrationPasswordAndBindDoNotRevealBootstrapEmail(t *testing.T) {
	// A closed-registration failure that depends on the body must look the
	// same for the configured bootstrap email and for every other email.
	// Otherwise 400 versus 403 confirms the address and that no system
	// administrator exists yet.
	t.Setenv(bootstrapSystemAdminEmailEnv, "admin@example.com")
	us := &bootstrapRegisterUserService{lookupErr: service.ErrUserNotFound}
	router := closedRegistrationRouter(inviteOnlyHandler(us))

	assertSameClosedError := func(t *testing.T, mutate func(map[string]string)) {
		t.Helper()
		bootstrapBody := bootstrapRegisterBody("admin@example.com")
		otherBody := bootstrapRegisterBody("other@example.com")
		mutate(bootstrapBody)
		mutate(otherBody)
		bootstrap := doRegister(t, router, bootstrapBody)
		other := doRegister(t, router, otherBody)
		if bootstrap.Code != other.Code {
			t.Fatalf("status oracle: bootstrap=%d %s other=%d %s",
				bootstrap.Code, bootstrap.Body.String(), other.Code, other.Body.String())
		}
		gotBootstrap := decodeClosedRegisterError(t, bootstrap)
		gotOther := decodeClosedRegisterError(t, other)
		if gotBootstrap.Error.Code != gotOther.Error.Code ||
			gotBootstrap.Error.Message != gotOther.Error.Message ||
			gotBootstrap.Error.Details != nil || gotOther.Error.Details != nil {
			t.Fatalf("body oracle: bootstrap=%s other=%s", bootstrap.Body.String(), other.Body.String())
		}
		if strings.Contains(bootstrap.Body.String(), "admin@example.com") ||
			strings.Contains(other.Body.String(), "admin@example.com") {
			t.Fatalf("error body names the bootstrap email: %s", bootstrap.Body.String())
		}
	}

	t.Run("weak password", func(t *testing.T) {
		assertSameClosedError(t, func(body map[string]string) {
			body["password"] = "short1"
		})
		bootstrap := doRegister(t, router, func() map[string]string {
			body := bootstrapRegisterBody("admin@example.com")
			body["password"] = "short1"
			return body
		}())
		if bootstrap.Code != http.StatusBadRequest {
			t.Fatalf("weak password status = %d, want 400 body=%s", bootstrap.Code, bootstrap.Body.String())
		}
		if !strings.Contains(bootstrap.Body.String(), "password must be 8-32 characters") {
			t.Fatalf("body = %s", bootstrap.Body.String())
		}
	})
	t.Run("binding", func(t *testing.T) {
		assertSameClosedError(t, func(body map[string]string) {
			body["username"] = "a"
		})
		bootstrap := doRegister(t, router, func() map[string]string {
			body := bootstrapRegisterBody("admin@example.com")
			body["username"] = "a"
			return body
		}())
		if bootstrap.Code != http.StatusForbidden {
			t.Fatalf("binding status = %d, want 403 body=%s", bootstrap.Code, bootstrap.Body.String())
		}
	})
	if us.registerCalls != 0 || us.updated != nil {
		t.Fatalf("rejected bodies must not create: calls=%d updated=%+v", us.registerCalls, us.updated)
	}

	// The password 400 must not leave the process mutex held.
	follow := doRegister(t, router, bootstrapRegisterBody("admin@example.com"))
	if follow.Code != http.StatusCreated {
		t.Fatalf("follow-up after password 400 status = %d, want 201 body=%s", follow.Code, follow.Body.String())
	}
}

func TestRegister_ClosedRegistrationOversizedBodyIsSameForbidden(t *testing.T) {
	t.Setenv(bootstrapSystemAdminEmailEnv, "admin@example.com")
	us := &bootstrapRegisterUserService{lookupErr: service.ErrUserNotFound}
	router := closedRegistrationRouter(inviteOnlyHandler(us))
	pad := strings.Repeat("a", 256*1024)
	for _, email := range []string{"admin@example.com", "other@example.com"} {
		body := bootstrapRegisterBody(email)
		body["pad"] = pad
		w := doRegister(t, router, body)
		if w.Code != http.StatusForbidden {
			t.Fatalf("%s oversized status = %d, want 403 body=%s", email, w.Code, w.Body.String())
		}
		got := decodeClosedRegisterError(t, w)
		if got.Error.Message != "Public registration is disabled" || got.Error.Details != nil {
			t.Fatalf("%s oversized body = %s", email, w.Body.String())
		}
	}
	if us.registerCalls != 0 {
		t.Fatalf("oversized body must not register, calls=%d", us.registerCalls)
	}
}

func TestRegister_BootstrapRecheckAdminCountBlocksCreate(t *testing.T) {
	t.Setenv(bootstrapSystemAdminEmailEnv, "admin@example.com")
	us := &bootstrapRegisterUserService{
		lookupErr:    service.ErrUserNotFound,
		recheckSet:   true,
		recheckTotal: 1,
	}
	router := newRegisterTestRouter(inviteOnlyHandler(us))
	w := doRegister(t, router, bootstrapRegisterBody("admin@example.com"))
	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "Public registration is disabled") {
		t.Fatalf("body = %s", w.Body.String())
	}
	if us.registerCalls != 0 || us.updated != nil || us.adminTotal != 0 {
		t.Fatalf("re-check must not leave a user: calls=%d updated=%+v adminTotal=%d",
			us.registerCalls, us.updated, us.adminTotal)
	}
	us.mu.Lock()
	us.recheckSet = false
	us.mu.Unlock()
	follow := doRegister(t, router, bootstrapRegisterBody("admin@example.com"))
	if follow.Code != http.StatusCreated {
		t.Fatalf("follow-up after re-check status = %d, want 201 body=%s", follow.Code, follow.Body.String())
	}
}

func TestRegister_BootstrapRecheckErrorBlocksCreate(t *testing.T) {
	t.Setenv(bootstrapSystemAdminEmailEnv, "admin@example.com")
	us := &bootstrapRegisterUserService{
		lookupErr:  service.ErrUserNotFound,
		recheckErr: context.DeadlineExceeded,
	}
	router := newRegisterTestRouter(inviteOnlyHandler(us))
	w := doRegister(t, router, bootstrapRegisterBody("admin@example.com"))
	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 body=%s", w.Code, w.Body.String())
	}
	if us.registerCalls != 0 || us.updated != nil || us.adminTotal != 0 {
		t.Fatalf("re-check error must not leave a user: calls=%d updated=%+v adminTotal=%d",
			us.registerCalls, us.updated, us.adminTotal)
	}
}

func TestRegister_BootstrapUniqueIndexFailureLeavesNothingAndReleasesMutex(t *testing.T) {
	t.Setenv(bootstrapSystemAdminEmailEnv, "admin@example.com")
	us := &bootstrapRegisterUserService{
		lookupErr:   service.ErrUserNotFound,
		registerErr: errors.New("UNIQUE constraint failed: users.email"),
	}
	router := newRegisterTestRouter(inviteOnlyHandler(us))
	w := doRegister(t, router, bootstrapRegisterBody("admin@example.com"))
	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "Public registration is disabled") ||
		strings.Contains(w.Body.String(), "UNIQUE") {
		t.Fatalf("body = %s", w.Body.String())
	}
	if us.registerCalls != 1 || us.updated != nil || us.adminTotal != 0 || us.existing != nil {
		t.Fatalf("unique-index failure left state: calls=%d updated=%+v admin=%d existing=%+v",
			us.registerCalls, us.updated, us.adminTotal, us.existing)
	}
	us.mu.Lock()
	us.registerErr = nil
	us.mu.Unlock()
	follow := doRegister(t, router, bootstrapRegisterBody("admin@example.com"))
	if follow.Code != http.StatusCreated {
		t.Fatalf("follow-up after create failure status = %d, want 201 body=%s", follow.Code, follow.Body.String())
	}
}
