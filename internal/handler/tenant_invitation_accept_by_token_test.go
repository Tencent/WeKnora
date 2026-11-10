package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/service"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
)

// acceptByTokenInvitationSvc only implements AcceptByToken; the embedded
// interface panics on any other call so the test stays focused.
type acceptByTokenInvitationSvc struct {
	interfaces.TenantInvitationService
	member    *types.TenantMember
	acceptErr error
}

func (s *acceptByTokenInvitationSvc) AcceptByToken(_ context.Context, _ string, userID string) (*types.TenantMember, error) {
	if s.acceptErr != nil {
		return nil, s.acceptErr
	}
	if s.member != nil {
		return s.member, nil
	}
	return &types.TenantMember{TenantID: 42, Role: types.TenantRoleViewer, Status: types.TenantMemberStatusActive}, nil
}

// acceptByTokenUserSvc returns a user with the configured home tenant so
// the handler's tenantless-adoption branch can be exercised.
type acceptByTokenUserSvc struct {
	interfaces.UserService
	homeTenant    uint64
	updatedTenant uint64
	updateCalled  bool
}

func (s *acceptByTokenUserSvc) GetUserByID(_ context.Context, _ string) (*types.User, error) {
	return &types.User{ID: "u-test", TenantID: s.homeTenant, IsActive: true}, nil
}

func (s *acceptByTokenUserSvc) UpdateUser(_ context.Context, user *types.User) error {
	s.updateCalled = true
	s.updatedTenant = user.TenantID
	return nil
}

type acceptByTokenTenantSvc struct {
	interfaces.TenantService
}

func (s *acceptByTokenTenantSvc) GetTenantByID(_ context.Context, id uint64) (*types.Tenant, error) {
	return &types.Tenant{ID: id, Name: "Invited Workspace"}, nil
}

func newAcceptByTokenTestRouter(h *TenantInvitationHandler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		// Production auth middleware injects the caller via the request
		// context (context.WithValue), which is what UserIDFromContext
		// reads — NOT gin's c.Keys. Mirror that here.
		c.Request = c.Request.WithContext(context.WithValue(c.Request.Context(), types.UserIDContextKey, "u-test"))
		c.Next()
	}, errorCapture())
	r.POST("/me/invitations/accept-by-token", h.AcceptMyInvitationByToken)
	return r
}

func TestAcceptMyInvitationByTokenSuccess(t *testing.T) {
	users := &acceptByTokenUserSvc{homeTenant: 0} // tenantless -> adopts invited tenant
	h := &TenantInvitationHandler{
		invitationService: &acceptByTokenInvitationSvc{},
		userService:       users,
		tenantService:     &acceptByTokenTenantSvc{},
	}
	r := newAcceptByTokenTestRouter(h)

	body := []byte(`{"token":"invite-token"}`)
	req := httptest.NewRequest(http.MethodPost, "/me/invitations/accept-by-token", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if !users.updateCalled || users.updatedTenant != 42 {
		t.Fatalf("tenantless user should adopt tenant 42, updateCalled=%v updatedTenant=%d",
			users.updateCalled, users.updatedTenant)
	}
	var resp struct {
		Success bool `json:"success"`
		Data    struct {
			Membership struct {
				TenantID uint64 `json:"tenant_id"`
				Role     string `json:"role"`
			} `json:"membership"`
			TenantName string `json:"tenant_name"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v body=%s", err, w.Body.String())
	}
	if !resp.Success || resp.Data.Membership.TenantID != 42 || resp.Data.TenantName != "Invited Workspace" {
		t.Fatalf("unexpected response: %+v", resp)
	}
}

func TestAcceptMyInvitationByTokenDoesNotMutateUserWithHomeTenant(t *testing.T) {
	users := &acceptByTokenUserSvc{homeTenant: 7} // already has a home tenant
	h := &TenantInvitationHandler{
		invitationService: &acceptByTokenInvitationSvc{},
		userService:       users,
		tenantService:     &acceptByTokenTenantSvc{},
	}
	r := newAcceptByTokenTestRouter(h)

	body := []byte(`{"token":"invite-token"}`)
	req := httptest.NewRequest(http.MethodPost, "/me/invitations/accept-by-token", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if users.updateCalled {
		t.Fatalf("user with home tenant must not be rewritten; updateCalled=%v", users.updateCalled)
	}
}

func TestAcceptMyInvitationByTokenInvalidTokenIsGone(t *testing.T) {
	h := &TenantInvitationHandler{
		invitationService: &acceptByTokenInvitationSvc{acceptErr: service.ErrInvitationTokenInvalid},
		userService:       &acceptByTokenUserSvc{},
		tenantService:     &acceptByTokenTenantSvc{},
	}
	r := newAcceptByTokenTestRouter(h)

	body := []byte(`{"token":"stale-token"}`)
	req := httptest.NewRequest(http.MethodPost, "/me/invitations/accept-by-token", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusGone {
		t.Fatalf("status=%d body=%s, want 410 for invalid token", w.Code, w.Body.String())
	}
}

func TestAcceptMyInvitationByTokenMissingTokenIsBadRequest(t *testing.T) {
	h := &TenantInvitationHandler{
		invitationService: &acceptByTokenInvitationSvc{},
		userService:       &acceptByTokenUserSvc{},
		tenantService:     &acceptByTokenTenantSvc{},
	}
	r := newAcceptByTokenTestRouter(h)

	body := []byte(`{}`)
	req := httptest.NewRequest(http.MethodPost, "/me/invitations/accept-by-token", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s, want 400 for missing token", w.Code, w.Body.String())
	}
}

// AcceptByToken is idempotent: an existing membership is returned
// untouched. The handler treats that member the same as a freshly
// created one (200 + membership), so a user clicking the same link
// twice from two devices never sees a role downgrade or an error.
func TestAcceptMyInvitationByTokenIdempotent(t *testing.T) {
	users := &acceptByTokenUserSvc{homeTenant: 42}
	h := &TenantInvitationHandler{
		invitationService: &acceptByTokenInvitationSvc{
			member: &types.TenantMember{TenantID: 42, Role: types.TenantRoleContributor, Status: types.TenantMemberStatusActive},
		},
		userService:   users,
		tenantService: &acceptByTokenTenantSvc{},
	}
	r := newAcceptByTokenTestRouter(h)

	body := []byte(`{"token":"invite-token"}`)
	req := httptest.NewRequest(http.MethodPost, "/me/invitations/accept-by-token", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		Data struct {
			Membership struct {
				Role string `json:"role"`
			} `json:"membership"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Data.Membership.Role != string(types.TenantRoleContributor) {
		t.Fatalf("role=%s, want contributor (existing membership preserved)", resp.Data.Membership.Role)
	}
	// homeTenant is already 42, so the adoption branch must not fire.
	if users.updateCalled {
		t.Fatalf("idempotent re-accept should not rewrite user; updateCalled=%v", users.updateCalled)
	}
}

func TestAcceptMyInvitationByTokenUnexpectedErrorIs500(t *testing.T) {
	h := &TenantInvitationHandler{
		invitationService: &acceptByTokenInvitationSvc{acceptErr: errors.New("boom")},
		userService:       &acceptByTokenUserSvc{},
		tenantService:     &acceptByTokenTenantSvc{},
	}
	r := newAcceptByTokenTestRouter(h)

	body := []byte(`{"token":"invite-token"}`)
	req := httptest.NewRequest(http.MethodPost, "/me/invitations/accept-by-token", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d body=%s, want 500 for unexpected error", w.Code, w.Body.String())
	}
}
