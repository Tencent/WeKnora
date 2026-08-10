package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	apprepo "github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/application/service"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
)

// autoAcceptSettingSvc returns a fixed answer for GetBool so the
// tenant.auto_accept_invitation switch can be flipped per-test.
type autoAcceptSettingSvc struct {
	interfaces.SystemSettingService
	enabled bool
}

func (s *autoAcceptSettingSvc) GetBool(_ context.Context, _ string, _ string, _ bool) bool {
	return s.enabled
}

// autoAcceptMemberSvc records whether AddMember was reached (the
// auto-join branch must call it instead of invitationService.Create).
type autoAcceptMemberSvc struct {
	interfaces.TenantMemberService
	member    *types.TenantMember
	addErr    error
	addCalled bool
}

func (s *autoAcceptMemberSvc) AddMember(_ context.Context, userID string, _ uint64, role types.TenantRole, _ *string) (*types.TenantMember, error) {
	s.addCalled = true
	if s.addErr != nil {
		return nil, s.addErr
	}
	if s.member != nil {
		return s.member, nil
	}
	return &types.TenantMember{
		UserID: userID,
		Role:   role,
		Status: types.TenantMemberStatusActive,
	}, nil
}

// autoAcceptUserSvc resolves GetUserByEmail for the 404 / happy paths.
type autoAcceptUserSvc struct {
	interfaces.UserService
	user *types.User
	uerr error
}

func (s *autoAcceptUserSvc) GetUserByEmail(_ context.Context, _ string) (*types.User, error) {
	return s.user, s.uerr
}

func (s *autoAcceptUserSvc) GetUserByID(_ context.Context, _ string) (*types.User, error) {
	return nil, nil
}

// autoAcceptInvitationSvc records whether the classic pending-invitation
// flow was reached (it must NOT run when auto-accept is on).
type autoAcceptInvitationSvc struct {
	interfaces.TenantInvitationService
	created bool
}

func (s *autoAcceptInvitationSvc) Create(_ context.Context, tenantID uint64, userID string, role types.TenantRole, _ *string, _ string) (*types.TenantInvitation, error) {
	s.created = true
	return &types.TenantInvitation{
		ID:            1,
		TenantID:      tenantID,
		InviteeUserID: userID,
		Role:          role,
		Status:        types.TenantInvitationStatusPending,
	}, nil
}

func newAutoAcceptTestRouter(h *TenantInvitationHandler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Request = c.Request.WithContext(context.WithValue(c.Request.Context(), types.UserIDContextKey, "u-owner"))
	}, errorCapture())
	r.POST("/tenants/:id/invitations", h.CreateInvitation)
	return r
}

func postAutoAcceptInvitation(t *testing.T, r *gin.Engine, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/tenants/7/invitations", bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestCreateInvitation_AutoAcceptEnabled_AddsMemberDirectly(t *testing.T) {
	users := &autoAcceptUserSvc{user: &types.User{ID: "u-bob", Email: "bob@x.com", Username: "bob"}}
	members := &autoAcceptMemberSvc{}
	invites := &autoAcceptInvitationSvc{}
	h := &TenantInvitationHandler{
		invitationService: invites,
		userService:       users,
		memberService:     members,
		systemSettingSvc:  &autoAcceptSettingSvc{enabled: true},
	}
	r := newAutoAcceptTestRouter(h)

	w := postAutoAcceptInvitation(t, r, `{"email":"bob@x.com","role":"contributor"}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if !members.addCalled {
		t.Fatal("auto-accept should call memberService.AddMember")
	}
	if invites.created {
		t.Fatal("auto-accept must not create a pending invitation row")
	}
	var resp struct {
		Success bool `json:"success"`
		Data    struct {
			UserID string `json:"user_id"`
			Role   string `json:"role"`
			Status string `json:"status"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v body=%s", err, w.Body.String())
	}
	if !resp.Success || resp.Data.UserID != "u-bob" {
		t.Fatalf("expected member-shaped response, got %+v", resp)
	}
	if resp.Data.Status != string(types.TenantMemberStatusActive) {
		t.Fatalf("member should be active, got %q", resp.Data.Status)
	}
}

func TestCreateInvitation_AutoAcceptDisabled_UsesInvitationFlow(t *testing.T) {
	users := &autoAcceptUserSvc{user: &types.User{ID: "u-bob", Email: "bob@x.com", Username: "bob"}}
	members := &autoAcceptMemberSvc{}
	invites := &autoAcceptInvitationSvc{}
	h := &TenantInvitationHandler{
		invitationService: invites,
		userService:       users,
		memberService:     members,
		systemSettingSvc:  &autoAcceptSettingSvc{enabled: false},
	}
	r := newAutoAcceptTestRouter(h)

	w := postAutoAcceptInvitation(t, r, `{"email":"bob@x.com","role":"viewer"}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if members.addCalled {
		t.Fatal("auto-accept disabled must NOT call AddMember")
	}
	if !invites.created {
		t.Fatal("disabled switch should fall through to the pending-invitation flow")
	}
	var resp struct {
		Success bool `json:"success"`
		Data    struct {
			ID     uint64 `json:"id"`
			Status string `json:"status"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v body=%s", err, w.Body.String())
	}
	if resp.Data.Status != string(types.TenantInvitationStatusPending) {
		t.Fatalf("expected pending invitation, got %q", resp.Data.Status)
	}
}

func TestCreateInvitation_AutoAccept_AlreadyMemberReturns409(t *testing.T) {
	users := &autoAcceptUserSvc{user: &types.User{ID: "u-bob", Email: "bob@x.com"}}
	members := &autoAcceptMemberSvc{addErr: service.ErrMembershipAlreadyExists}
	invites := &autoAcceptInvitationSvc{}
	h := &TenantInvitationHandler{
		invitationService: invites,
		userService:       users,
		memberService:     members,
		systemSettingSvc:  &autoAcceptSettingSvc{enabled: true},
	}
	r := newAutoAcceptTestRouter(h)

	w := postAutoAcceptInvitation(t, r, `{"email":"bob@x.com","role":"viewer"}`)
	if w.Code != http.StatusConflict {
		t.Fatalf("status=%d body=%s, want 409", w.Code, w.Body.String())
	}
}

func TestCreateInvitation_AutoAccept_UnknownEmailReturns404(t *testing.T) {
	users := &autoAcceptUserSvc{uerr: apprepo.ErrUserNotFound}
	members := &autoAcceptMemberSvc{}
	invites := &autoAcceptInvitationSvc{}
	h := &TenantInvitationHandler{
		invitationService: invites,
		userService:       users,
		memberService:     members,
		systemSettingSvc:  &autoAcceptSettingSvc{enabled: true},
	}
	r := newAutoAcceptTestRouter(h)

	w := postAutoAcceptInvitation(t, r, `{"email":"ghost@x.com","role":"viewer"}`)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status=%d body=%s, want 404", w.Code, w.Body.String())
	}
}

func TestCreateInvitation_AutoAccept_APICannotAssignOwnerReturns403(t *testing.T) {
	users := &autoAcceptUserSvc{user: &types.User{ID: "u-bob", Email: "bob@x.com"}}
	members := &autoAcceptMemberSvc{addErr: service.ErrAPIKeyCannotAssignOwner}
	invites := &autoAcceptInvitationSvc{}
	h := &TenantInvitationHandler{
		invitationService: invites,
		userService:       users,
		memberService:     members,
		systemSettingSvc:  &autoAcceptSettingSvc{enabled: true},
	}
	r := newAutoAcceptTestRouter(h)

	w := postAutoAcceptInvitation(t, r, `{"email":"bob@x.com","role":"owner"}`)
	if w.Code != http.StatusForbidden {
		t.Fatalf("status=%d body=%s, want 403", w.Code, w.Body.String())
	}
}

func TestCreateInvitation_AutoAcceptEnabled_NilMemberServiceReturns500(t *testing.T) {
	users := &autoAcceptUserSvc{user: &types.User{ID: "u-bob", Email: "bob@x.com"}}
	invites := &autoAcceptInvitationSvc{}
	h := &TenantInvitationHandler{
		invitationService: invites,
		userService:       users,
		systemSettingSvc:  &autoAcceptSettingSvc{enabled: true},
	}
	r := newAutoAcceptTestRouter(h)

	w := postAutoAcceptInvitation(t, r, `{"email":"bob@x.com","role":"viewer"}`)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d body=%s, want 500", w.Code, w.Body.String())
	}
	if invites.created {
		t.Fatal("memberService nil must not silently fall back to invitation flow")
	}
}
