package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	apprepo "github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/application/service"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
)

// stubMemberService is a TenantMemberService whose individual methods can
// be overridden per-test. Embedding the interface keeps the fixture
// minimal — any method not set in a given test will nil-panic if it's
// reached, which is exactly what we want for "the test should not have
// gotten here" assertions.
type stubMemberService struct {
	interfaces.TenantMemberService
	add        func(ctx context.Context, userID string, tenantID uint64, role types.TenantRole, invitedBy *string) (*types.TenantMember, error)
	listTenant func(ctx context.Context, tenantID uint64) ([]*types.TenantMember, error)
	updateRole func(ctx context.Context, userID string, tenantID uint64, newRole types.TenantRole) error
	remove     func(ctx context.Context, userID string, tenantID uint64) error
}

func (s *stubMemberService) AddMember(ctx context.Context, userID string, tenantID uint64, role types.TenantRole, invitedBy *string) (*types.TenantMember, error) {
	return s.add(ctx, userID, tenantID, role, invitedBy)
}

func (s *stubMemberService) ListByTenant(ctx context.Context, tenantID uint64) ([]*types.TenantMember, error) {
	return s.listTenant(ctx, tenantID)
}

func (s *stubMemberService) UpdateRole(ctx context.Context, userID string, tenantID uint64, newRole types.TenantRole) error {
	return s.updateRole(ctx, userID, tenantID, newRole)
}

func (s *stubMemberService) RemoveMember(ctx context.Context, userID string, tenantID uint64) error {
	return s.remove(ctx, userID, tenantID)
}

// stubMemberUserService satisfies just the two UserService methods the
// handler reaches: GetUserByEmail (AddMember translation) and
// GetUserByID (ListMembers hydration).
type stubMemberUserService struct {
	interfaces.UserService
	getByEmail func(ctx context.Context, email string) (*types.User, error)
	getByID    func(ctx context.Context, id string) (*types.User, error)
}

func (s *stubMemberUserService) GetUserByEmail(ctx context.Context, email string) (*types.User, error) {
	return s.getByEmail(ctx, email)
}

func (s *stubMemberUserService) GetUserByID(ctx context.Context, id string) (*types.User, error) {
	return s.getByID(ctx, id)
}

// memberTestRouter wires the handler with the same errorCapture middleware
// production uses, so c.Error() shows up as a real HTTP status in the
// recorder.
func memberTestRouter(h *TenantMemberHandler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(errorCapture()) // defined in auth_register_invite_only_test.go
	r.GET("/tenants/:id/members", h.ListMembers)
	r.POST("/tenants/:id/members", h.AddMember)
	r.PUT("/tenants/:id/members/:user_id", h.UpdateMemberRole)
	r.DELETE("/tenants/:id/members/:user_id", h.RemoveMember)
	return r
}

// withCallerUser injects the authenticated caller's user ID into the
// request context just like middleware/auth.go does. Several handler
// branches (notably AddMember's invited_by attribution) read this.
func withCallerUser(req *http.Request, callerUserID string) *http.Request {
	ctx := context.WithValue(req.Context(), types.UserIDContextKey, callerUserID)
	return req.WithContext(ctx)
}

func doJSON(t *testing.T, r *gin.Engine, method, path string, body any, callerID string) *httptest.ResponseRecorder {
	t.Helper()
	var reader *bytes.Reader
	if body != nil {
		buf, _ := json.Marshal(body)
		reader = bytes.NewReader(buf)
	} else {
		reader = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, reader)
	req.Header.Set("Content-Type", "application/json")
	if callerID != "" {
		req = withCallerUser(req, callerID)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// ---------- ListMembers ----------

func TestTenantMember_ListMembers_HappyPath(t *testing.T) {
	now := time.Now()
	ms := &stubMemberService{
		listTenant: func(_ context.Context, tenantID uint64) ([]*types.TenantMember, error) {
			if tenantID != 1 {
				t.Fatalf("tenantID parsed wrong: got %d", tenantID)
			}
			return []*types.TenantMember{
				{UserID: "u-owner", TenantID: 1, Role: types.TenantRoleOwner, Status: types.TenantMemberStatusActive, JoinedAt: now},
				{UserID: "u-c", TenantID: 1, Role: types.TenantRoleContributor, Status: types.TenantMemberStatusActive, JoinedAt: now},
			}, nil
		},
	}
	us := &stubMemberUserService{
		getByID: func(_ context.Context, id string) (*types.User, error) {
			return &types.User{ID: id, Username: id, Email: id + "@x.com"}, nil
		},
	}
	h := NewTenantMemberHandler(ms, us)

	w := doJSON(t, memberTestRouter(h), http.MethodGet, "/tenants/1/members", nil, "u-owner")
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		Success bool `json:"success"`
		Data    struct {
			Members []types.TenantMemberResponse `json:"members"`
			Total   int                          `json:"total"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Data.Total != 2 || len(resp.Data.Members) != 2 {
		t.Fatalf("expected 2 members, got total=%d len=%d", resp.Data.Total, len(resp.Data.Members))
	}
	// Hydration must have populated email so the UI can render avatars.
	if resp.Data.Members[0].Email == "" {
		t.Fatalf("expected hydrated email, got empty")
	}
}

func TestTenantMember_ListMembers_TolerantToDeletedUsers(t *testing.T) {
	// A dangling membership (user account deleted) must still appear in
	// the listing so the Owner can clean it up. The service returned the
	// row; the user lookup error is silently swallowed.
	ms := &stubMemberService{
		listTenant: func(_ context.Context, _ uint64) ([]*types.TenantMember, error) {
			return []*types.TenantMember{
				{UserID: "u-ghost", TenantID: 1, Role: types.TenantRoleViewer, Status: types.TenantMemberStatusActive},
			}, nil
		},
	}
	us := &stubMemberUserService{
		getByID: func(_ context.Context, _ string) (*types.User, error) {
			return nil, apprepo.ErrUserNotFound
		},
	}
	h := NewTenantMemberHandler(ms, us)

	w := doJSON(t, memberTestRouter(h), http.MethodGet, "/tenants/1/members", nil, "u-owner")
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 even when user lookup fails, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), `"user_id":"u-ghost"`) {
		t.Fatalf("dangling membership must remain in response: %s", w.Body.String())
	}
}

func TestTenantMember_ListMembers_RejectsBadTenantID(t *testing.T) {
	h := NewTenantMemberHandler(&stubMemberService{}, &stubMemberUserService{})
	w := doJSON(t, memberTestRouter(h), http.MethodGet, "/tenants/abc/members", nil, "u1")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("non-numeric tenant id must 400, got %d", w.Code)
	}
}

// ---------- AddMember ----------

func TestTenantMember_AddMember_HappyPath(t *testing.T) {
	caller := "u-owner"
	now := time.Now()
	ms := &stubMemberService{
		add: func(_ context.Context, userID string, tenantID uint64, role types.TenantRole, invitedBy *string) (*types.TenantMember, error) {
			if invitedBy == nil || *invitedBy != caller {
				t.Fatalf("invited_by must be the caller, got %v", invitedBy)
			}
			return &types.TenantMember{UserID: userID, TenantID: tenantID, Role: role, Status: types.TenantMemberStatusActive, JoinedAt: now, InvitedBy: invitedBy}, nil
		},
	}
	us := &stubMemberUserService{
		getByEmail: func(_ context.Context, email string) (*types.User, error) {
			return &types.User{ID: "u-bob", Email: email, Username: "bob"}, nil
		},
	}
	h := NewTenantMemberHandler(ms, us)

	body := map[string]any{"email": "bob@x.com", "role": "contributor"}
	w := doJSON(t, memberTestRouter(h), http.MethodPost, "/tenants/1/members", body, caller)
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestTenantMember_AddMember_UnknownEmailReturns404(t *testing.T) {
	// PR 3 requires the invitee to already have an account; mapping
	// ErrUserNotFound to 404 lets the UI prompt "ask them to sign up
	// first" rather than the generic "something failed".
	us := &stubMemberUserService{
		getByEmail: func(_ context.Context, _ string) (*types.User, error) {
			return nil, apprepo.ErrUserNotFound
		},
	}
	h := NewTenantMemberHandler(&stubMemberService{}, us)

	body := map[string]any{"email": "ghost@x.com", "role": "viewer"}
	w := doJSON(t, memberTestRouter(h), http.MethodPost, "/tenants/1/members", body, "u-owner")
	if w.Code != http.StatusNotFound {
		t.Fatalf("unknown email must surface as 404, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestTenantMember_AddMember_DuplicateMaps409(t *testing.T) {
	ms := &stubMemberService{
		add: func(_ context.Context, _ string, _ uint64, _ types.TenantRole, _ *string) (*types.TenantMember, error) {
			return nil, service.ErrMembershipAlreadyExists
		},
	}
	us := &stubMemberUserService{
		getByEmail: func(_ context.Context, _ string) (*types.User, error) {
			return &types.User{ID: "u-bob", Email: "bob@x.com"}, nil
		},
	}
	h := NewTenantMemberHandler(ms, us)

	body := map[string]any{"email": "bob@x.com", "role": "contributor"}
	w := doJSON(t, memberTestRouter(h), http.MethodPost, "/tenants/1/members", body, "u-owner")
	if w.Code != http.StatusConflict {
		t.Fatalf("duplicate must surface as 409, got %d", w.Code)
	}
}

func TestTenantMember_AddMember_InvalidRoleRejectedUpfront(t *testing.T) {
	// Reject obviously bogus roles before paying for the user lookup.
	called := false
	us := &stubMemberUserService{
		getByEmail: func(_ context.Context, _ string) (*types.User, error) {
			called = true
			return &types.User{ID: "u-bob"}, nil
		},
	}
	h := NewTenantMemberHandler(&stubMemberService{}, us)

	body := map[string]any{"email": "bob@x.com", "role": "wizard"}
	w := doJSON(t, memberTestRouter(h), http.MethodPost, "/tenants/1/members", body, "u-owner")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("invalid role must 400, got %d", w.Code)
	}
	if called {
		t.Fatalf("user lookup must not run for invalid role")
	}
}

// ---------- UpdateMemberRole ----------

func TestTenantMember_UpdateRole_HappyPath(t *testing.T) {
	ms := &stubMemberService{
		updateRole: func(_ context.Context, userID string, tenantID uint64, newRole types.TenantRole) error {
			if userID != "u-bob" || tenantID != 1 || newRole != types.TenantRoleAdmin {
				t.Fatalf("unexpected args: user=%s tenant=%d role=%s", userID, tenantID, newRole)
			}
			return nil
		},
	}
	h := NewTenantMemberHandler(ms, &stubMemberUserService{})

	body := map[string]any{"role": "admin"}
	w := doJSON(t, memberTestRouter(h), http.MethodPut, "/tenants/1/members/u-bob", body, "u-owner")
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestTenantMember_UpdateRole_LastOwnerMaps409(t *testing.T) {
	// Service-layer invariant: the last Owner cannot be demoted. Mapping
	// ErrLastOwner to 409 lets the UI render the message inline rather
	// than as a generic failure.
	ms := &stubMemberService{
		updateRole: func(_ context.Context, _ string, _ uint64, _ types.TenantRole) error {
			return service.ErrLastOwner
		},
	}
	h := NewTenantMemberHandler(ms, &stubMemberUserService{})

	body := map[string]any{"role": "viewer"}
	w := doJSON(t, memberTestRouter(h), http.MethodPut, "/tenants/1/members/u-only-owner", body, "u-only-owner")
	if w.Code != http.StatusConflict {
		t.Fatalf("last owner demote must 409, got %d", w.Code)
	}
}

func TestTenantMember_UpdateRole_UnknownMembershipMaps404(t *testing.T) {
	ms := &stubMemberService{
		updateRole: func(_ context.Context, _ string, _ uint64, _ types.TenantRole) error {
			return service.ErrMembershipNotFound
		},
	}
	h := NewTenantMemberHandler(ms, &stubMemberUserService{})

	body := map[string]any{"role": "admin"}
	w := doJSON(t, memberTestRouter(h), http.MethodPut, "/tenants/1/members/u-ghost", body, "u-owner")
	if w.Code != http.StatusNotFound {
		t.Fatalf("missing membership must 404, got %d", w.Code)
	}
}

// ---------- RemoveMember ----------

func TestTenantMember_RemoveMember_HappyPath(t *testing.T) {
	ms := &stubMemberService{
		remove: func(_ context.Context, userID string, tenantID uint64) error {
			if userID != "u-bob" || tenantID != 1 {
				t.Fatalf("unexpected args: user=%s tenant=%d", userID, tenantID)
			}
			return nil
		},
	}
	h := NewTenantMemberHandler(ms, &stubMemberUserService{})

	w := doJSON(t, memberTestRouter(h), http.MethodDelete, "/tenants/1/members/u-bob", nil, "u-owner")
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestTenantMember_RemoveMember_LastOwnerMaps409(t *testing.T) {
	ms := &stubMemberService{
		remove: func(_ context.Context, _ string, _ uint64) error {
			return service.ErrLastOwner
		},
	}
	h := NewTenantMemberHandler(ms, &stubMemberUserService{})

	w := doJSON(t, memberTestRouter(h), http.MethodDelete, "/tenants/1/members/u-only-owner", nil, "u-only-owner")
	if w.Code != http.StatusConflict {
		t.Fatalf("last-owner remove must 409, got %d", w.Code)
	}
}
