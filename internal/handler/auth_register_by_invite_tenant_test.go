package handler

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
)

type invitedRegistrationUserService struct {
	interfaces.UserService
	registeredMode types.TenantProvisioningMode
	updatedTenant  uint64
	updateCalls    []uint64
}

func (s *invitedRegistrationUserService) GetUserByEmail(context.Context, string) (*types.User, error) {
	return nil, nil
}

func (s *invitedRegistrationUserService) Register(_ context.Context, req *types.RegisterRequest) (*types.User, error) {
	s.registeredMode = req.TenantProvisioning
	return &types.User{ID: "new-user", Username: req.Username, Email: req.Email, IsActive: true}, nil
}

func (s *invitedRegistrationUserService) UpdateUser(_ context.Context, user *types.User) error {
	s.updatedTenant = user.TenantID
	s.updateCalls = append(s.updateCalls, user.TenantID)
	return nil
}

func (s *invitedRegistrationUserService) GenerateTokens(context.Context, *types.User) (string, string, error) {
	return "access", "refresh", nil
}

type invitedRegistrationInvitationService struct {
	interfaces.TenantInvitationService
	acceptErr error
	lookupErr error
}

func (s *invitedRegistrationInvitationService) LookupByToken(context.Context, string) (*types.TenantInvitation, error) {
	if s.lookupErr != nil {
		return nil, s.lookupErr
	}
	return &types.TenantInvitation{TenantID: 42, Role: types.TenantRoleViewer}, nil
}

func (s *invitedRegistrationInvitationService) AcceptByToken(context.Context, string, string) (*types.TenantMember, error) {
	if s.acceptErr != nil {
		return nil, s.acceptErr
	}
	return &types.TenantMember{TenantID: 42, Role: types.TenantRoleViewer}, nil
}

type invitedRegistrationTenantService struct {
	interfaces.TenantService
}

func TestRegisterByInviteRestoresTenantlessAccountWhenInviteExpiresDuringRegistration(t *testing.T) {
	gin.SetMode(gin.TestMode)
	users := &invitedRegistrationUserService{}
	h := &AuthHandler{
		userService:   users,
		tenantService: &invitedRegistrationTenantService{},
		invitationSvc: &invitedRegistrationInvitationService{acceptErr: errors.New("expired")},
	}
	r := gin.New()
	r.Use(errorCapture())
	r.POST("/auth/register-by-invite", h.RegisterByInvite)

	body := []byte(`{"token":"invite-token","email":"alice@example.com","username":"alice","password":"supersecret1"}`)
	req := httptest.NewRequest(http.MethodPost, "/auth/register-by-invite", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusGone {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if users.updatedTenant != 0 {
		t.Fatalf("updated tenant=%d, want tenantless rollback", users.updatedTenant)
	}
	if len(users.updateCalls) != 2 || users.updateCalls[0] != 42 || users.updateCalls[1] != 0 {
		t.Fatalf("update calls=%v, want [42 0]", users.updateCalls)
	}
}

func (s *invitedRegistrationTenantService) GetTenantByID(context.Context, uint64) (*types.Tenant, error) {
	return &types.Tenant{ID: 42, Name: "Invited Workspace"}, nil
}

func TestRegisterByInviteUsesInvitedTenantWithoutPersonalTenant(t *testing.T) {
	gin.SetMode(gin.TestMode)
	users := &invitedRegistrationUserService{}
	h := &AuthHandler{
		userService:   users,
		tenantService: &invitedRegistrationTenantService{},
		invitationSvc: &invitedRegistrationInvitationService{},
	}
	r := gin.New()
	r.Use(errorCapture())
	r.POST("/auth/register-by-invite", h.RegisterByInvite)

	body := []byte(`{"token":"invite-token","email":"alice@example.com","username":"alice","password":"supersecret1"}`)
	req := httptest.NewRequest(http.MethodPost, "/auth/register-by-invite", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if users.registeredMode != types.TenantProvisioningTenantless {
		t.Fatalf("register mode=%q, want tenantless", users.registeredMode)
	}
	if users.updatedTenant != 42 {
		t.Fatalf("updated tenant=%d, want 42", users.updatedTenant)
	}
}

func TestRegisterByInviteRejectsSimplePasswordWhenComplexEnabled(t *testing.T) {
	gin.SetMode(gin.TestMode)
	users := &invitedRegistrationUserService{}
	h := &AuthHandler{
		userService:      users,
		tenantService:    &invitedRegistrationTenantService{},
		invitationSvc:    &invitedRegistrationInvitationService{},
		systemSettingSvc: &tenantPolicySettingService{enabled: true},
	}
	r := gin.New()
	r.Use(errorCapture())
	r.POST("/auth/register-by-invite", h.RegisterByInvite)

	body := []byte(`{"token":"invite-token","email":"alice@example.com","username":"alice","password":"supersecret1"}`)
	req := httptest.NewRequest(http.MethodPost, "/auth/register-by-invite", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s, want 400", w.Code, w.Body.String())
	}
	if users.registeredMode != "" {
		t.Fatalf("Register was called with mode=%q", users.registeredMode)
	}
}

func TestRegisterByInviteRegistrationModes(t *testing.T) {
	for _, tc := range []struct {
		name      string
		mode      string
		lookupErr error
		want      int
	}{
		{"open", config.AuthRegistrationModeSelfServe, nil, http.StatusCreated},
		{"invited", config.AuthRegistrationModeInviteRegister, nil, http.StatusCreated},
		{"disabled", config.AuthRegistrationModeInviteOnly, nil, http.StatusForbidden},
		{"unknown mode", "invalid", nil, http.StatusForbidden},
		{"invalid invitation", config.AuthRegistrationModeInviteRegister, errors.New("invalid"), http.StatusGone},
		{"expired invitation", config.AuthRegistrationModeInviteRegister, errors.New("expired"), http.StatusGone},
		{"revoked invitation", config.AuthRegistrationModeInviteRegister, errors.New("revoked"), http.StatusGone},
	} {
		t.Run(tc.name, func(t *testing.T) {
			users := &invitedRegistrationUserService{}
			h := &AuthHandler{
				configInfo:    &config.Config{Auth: &config.AuthConfig{RegistrationMode: tc.mode}},
				userService:   users,
				tenantService: &invitedRegistrationTenantService{},
				invitationSvc: &invitedRegistrationInvitationService{lookupErr: tc.lookupErr},
			}
			r := gin.New()
			r.Use(errorCapture())
			r.POST("/auth/register-by-invite", h.RegisterByInvite)
			req := httptest.NewRequest(http.MethodPost, "/auth/register-by-invite", bytes.NewBufferString(
				`{"token":"invite-token","email":"alice@example.com","username":"alice","password":"supersecret1"}`))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			if w.Code != tc.want {
				t.Fatalf("status=%d, want %d body=%s", w.Code, tc.want, w.Body.String())
			}
			if tc.want == http.StatusCreated {
				if users.registeredMode != types.TenantProvisioningTenantless || users.updatedTenant != 42 {
					t.Fatalf("expected account in invited tenant only: mode=%q tenant=%d",
						users.registeredMode, users.updatedTenant)
				}
			} else if users.registeredMode != "" {
				t.Fatal("rejected registration must not create an account")
			}
		})
	}
}
