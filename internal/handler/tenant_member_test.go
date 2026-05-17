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
	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/middleware"
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
	add             func(ctx context.Context, userID string, tenantID uint64, role types.TenantRole, invitedBy *string) (*types.TenantMember, error)
	listTenant      func(ctx context.Context, tenantID uint64) ([]*types.TenantMember, error)
	listMembersPage func(ctx context.Context, tenantID uint64, query string, page, pageSize int) ([]*types.TenantMember, int64, error)
	updateRole      func(ctx context.Context, userID string, tenantID uint64, newRole types.TenantRole) error
	remove          func(ctx context.Context, userID string, tenantID uint64) error
}

func (s *stubMemberService) ListMembersPage(
	ctx context.Context,
	tenantID uint64,
	query string,
	page, pageSize int,
) ([]*types.TenantMember, int64, error) {
	if s.listMembersPage != nil {
		return s.listMembersPage(ctx, tenantID, query, page, pageSize)
	}
	if s.listTenant != nil {
		members, err := s.listTenant(ctx, tenantID)
		if err != nil {
			return nil, 0, err
		}
		total := int64(len(members))
		if page < 1 {
			page = 1
		}
		if pageSize < 1 {
			pageSize = 20
		}
		off := (page - 1) * pageSize
		if off >= len(members) {
			return []*types.TenantMember{}, total, nil
		}
		end := off + pageSize
		if end > len(members) {
			end = len(members)
		}
		slice := append([]*types.TenantMember(nil), members[off:end]...)
		return slice, total, nil
	}
	return []*types.TenantMember{}, 0, nil
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
	// getByIDs lets tests override the batched lookup; when unset the
	// stub falls back to fanning out to getByID so existing tests stay
	// green without changes.
	getByIDs func(ctx context.Context, ids []string) (map[string]*types.User, error)
}

func (s *stubMemberUserService) GetUserByEmail(ctx context.Context, email string) (*types.User, error) {
	return s.getByEmail(ctx, email)
}

func (s *stubMemberUserService) GetUserByID(ctx context.Context, id string) (*types.User, error) {
	return s.getByID(ctx, id)
}

func (s *stubMemberUserService) GetUsersByIDs(ctx context.Context, ids []string) (map[string]*types.User, error) {
	if s.getByIDs != nil {
		return s.getByIDs(ctx, ids)
	}
	out := make(map[string]*types.User, len(ids))
	for _, id := range ids {
		u, err := s.getByID(ctx, id)
		if err != nil || u == nil {
			continue
		}
		out[u.ID] = u
	}
	return out, nil
}

// newTestMemberHandler builds a TenantMemberHandler with no extra
// dependencies. The cross-tenant URL check moved to
// middleware.RequirePathTenantMatch in PR 4; tests mount that
// middleware via memberTestRouter rather than threading a cfg through
// the handler.
func newTestMemberHandler(ms interfaces.TenantMemberService, us interfaces.UserService) *TenantMemberHandler {
	return NewTenantMemberHandler(ms, us)
}

// memberTestRouter wires the handler with the same errorCapture middleware
// production uses, so c.Error() shows up as a real HTTP status in the
// recorder. It also mounts middleware.RequirePathTenantMatch on the
// /tenants/:id group, mirroring router.RegisterTenantRoutes — that's
// where the URL-vs-active-tenant cross-check now lives, and the
// per-handler tests below assert it through this layer.
func memberTestRouter(h *TenantMemberHandler) *gin.Engine {
	return memberTestRouterWithCfg(h, &config.Config{
		Tenant: &config.TenantConfig{EnableCrossTenantAccess: true},
	})
}

// memberTestRouterWithCfg lets a test choose its own config (e.g.
// disabling the cross-tenant superuser carve-out) so it can assert how
// RequirePathTenantMatch behaves under different cluster flags.
func memberTestRouterWithCfg(h *TenantMemberHandler, cfg *config.Config) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(errorCapture()) // defined in auth_register_invite_only_test.go
	tenantByID := r.Group("/tenants/:id", middleware.RequirePathTenantMatch(cfg))
	tenantByID.GET("/members", h.ListMembers)
	tenantByID.POST("/members", h.AddMember)
	tenantByID.PUT("/members/:user_id", h.UpdateMemberRole)
	tenantByID.DELETE("/members/:user_id", h.RemoveMember)
	tenantByID.POST("/leave", h.LeaveTenant)
	return r
}

// defaultTestTenantID is what every test request is "active in" unless
// the test overrides it via doJSONWithTenant. Tenant 1 is also what the
// per-test data fixtures hard-code, so call sites stay short.
const defaultTestTenantID uint64 = 1

// memberCtxOpts lets a test override what the auth middleware would have
// stuffed into the request context. The zero value matches the common
// case ("authenticated, active in tenant 1, no superuser flag").
type memberCtxOpts struct {
	callerID    string
	tenantID    uint64
	user        *types.User
	skipTenant  bool // when true, do NOT set TenantIDContextKey at all
}

// withMemberCtx installs the auth-middleware-equivalent values on req's
// context. We intentionally set the tenant ID here so the handler's
// resolveTenantIDFromPath cross-check has something to compare against;
// every endpoint trusts that pairing to reject cross-tenant escalation.
func withMemberCtx(req *http.Request, opts memberCtxOpts) *http.Request {
	ctx := req.Context()
	if opts.callerID != "" {
		ctx = context.WithValue(ctx, types.UserIDContextKey, opts.callerID)
	}
	if !opts.skipTenant {
		tid := opts.tenantID
		if tid == 0 {
			tid = defaultTestTenantID
		}
		ctx = context.WithValue(ctx, types.TenantIDContextKey, tid)
	}
	if opts.user != nil {
		ctx = context.WithValue(ctx, types.UserContextKey, opts.user)
	}
	return req.WithContext(ctx)
}

func doJSON(t *testing.T, r *gin.Engine, method, path string, body any, callerID string) *httptest.ResponseRecorder {
	t.Helper()
	return doJSONWithCtx(t, r, method, path, body, memberCtxOpts{callerID: callerID})
}

func doJSONWithCtx(t *testing.T, r *gin.Engine, method, path string, body any, opts memberCtxOpts) *httptest.ResponseRecorder {
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
	req = withMemberCtx(req, opts)
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
	h := newTestMemberHandler(ms, us)

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
	h := newTestMemberHandler(ms, us)

	w := doJSON(t, memberTestRouter(h), http.MethodGet, "/tenants/1/members", nil, "u-owner")
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 even when user lookup fails, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), `"user_id":"u-ghost"`) {
		t.Fatalf("dangling membership must remain in response: %s", w.Body.String())
	}
}

func TestTenantMember_ListMembers_RejectsBadTenantID(t *testing.T) {
	h := newTestMemberHandler(&stubMemberService{}, &stubMemberUserService{})
	w := doJSON(t, memberTestRouter(h), http.MethodGet, "/tenants/abc/members", nil, "u1")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("non-numeric tenant id must 400, got %d", w.Code)
	}
}

func TestTenantMember_ListMembers_RejectsInvalidPage(t *testing.T) {
	h := newTestMemberHandler(&stubMemberService{}, &stubMemberUserService{})
	w := doJSON(t, memberTestRouter(h), http.MethodGet, "/tenants/1/members?page=0", nil, "u1")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("page=0 must 400, got %d body=%s", w.Code, w.Body.String())
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
	h := newTestMemberHandler(ms, us)

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
	h := newTestMemberHandler(&stubMemberService{}, us)

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
	h := newTestMemberHandler(ms, us)

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
	h := newTestMemberHandler(&stubMemberService{}, us)

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
	h := newTestMemberHandler(ms, &stubMemberUserService{})

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
	h := newTestMemberHandler(ms, &stubMemberUserService{})

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
	h := newTestMemberHandler(ms, &stubMemberUserService{})

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
	h := newTestMemberHandler(ms, &stubMemberUserService{})

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
	h := newTestMemberHandler(ms, &stubMemberUserService{})

	w := doJSON(t, memberTestRouter(h), http.MethodDelete, "/tenants/1/members/u-only-owner", nil, "u-only-owner")
	if w.Code != http.StatusConflict {
		t.Fatalf("last-owner remove must 409, got %d", w.Code)
	}
}

// ---------- LeaveTenant ----------

func TestTenantMember_LeaveTenant_HappyPath(t *testing.T) {
	// LeaveTenant must invoke RemoveMember with the caller's own user
	// id, regardless of any path :user_id segment. The route doesn't
	// have a :user_id; the caller's id comes from the auth context.
	ms := &stubMemberService{
		remove: func(_ context.Context, userID string, tenantID uint64) error {
			if userID != "u-self" || tenantID != 1 {
				t.Fatalf("LeaveTenant must use caller id; got user=%s tenant=%d", userID, tenantID)
			}
			return nil
		},
	}
	h := newTestMemberHandler(ms, &stubMemberUserService{})

	w := doJSON(t, memberTestRouter(h), http.MethodPost, "/tenants/1/leave", nil, "u-self")
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestTenantMember_LeaveTenant_LastOwnerMaps409(t *testing.T) {
	// The whole point of having a separate leave endpoint is that
	// non-Owners can quit, but the same last-Owner invariant still
	// applies — an Owner that's the only one left must transfer
	// ownership before they can leave.
	ms := &stubMemberService{
		remove: func(_ context.Context, _ string, _ uint64) error {
			return service.ErrLastOwner
		},
	}
	h := newTestMemberHandler(ms, &stubMemberUserService{})

	w := doJSON(t, memberTestRouter(h), http.MethodPost, "/tenants/1/leave", nil, "u-only-owner")
	if w.Code != http.StatusConflict {
		t.Fatalf("last-owner self-leave must 409, got %d", w.Code)
	}
}

func TestTenantMember_LeaveTenant_MissingCallerReturns401(t *testing.T) {
	// Defensive: if the auth middleware ever fails to attach a user id,
	// LeaveTenant must NOT silently call RemoveMember(""), it should
	// surface 401 so the caller knows their session is broken.
	called := false
	ms := &stubMemberService{
		remove: func(_ context.Context, _ string, _ uint64) error {
			called = true
			return nil
		},
	}
	h := newTestMemberHandler(ms, &stubMemberUserService{})

	w := doJSON(t, memberTestRouter(h), http.MethodPost, "/tenants/1/leave", nil, "")
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("missing caller must 401, got %d", w.Code)
	}
	if called {
		t.Fatalf("RemoveMember must not be called without a caller id")
	}
}

// ---------- Cross-tenant guard ----------

// A user whose active tenant context is N must NOT be able to drive
// operations on tenant M just by changing the URL. This is the
// regression test for the H1 finding in #1320's review: the handler
// used to trust :id and pass it straight to the service, so an Owner
// of tenant 1 could POST /tenants/5/members and have the service
// happily create rows for a tenant they had no claim to.

func TestTenantMember_RejectsCrossTenantURL_List(t *testing.T) {
	called := false
	ms := &stubMemberService{
		listTenant: func(_ context.Context, _ uint64) ([]*types.TenantMember, error) {
			called = true
			return nil, nil
		},
	}
	h := newTestMemberHandler(ms, &stubMemberUserService{})

	// Active tenant 1, URL targets tenant 5.
	w := doJSONWithCtx(t, memberTestRouter(h), http.MethodGet, "/tenants/5/members", nil,
		memberCtxOpts{callerID: "u1", tenantID: 1})
	if w.Code != http.StatusForbidden {
		t.Fatalf("cross-tenant URL must 403, got %d body=%s", w.Code, w.Body.String())
	}
	if called {
		t.Fatalf("service must NOT be reached when :id != active tenant")
	}
}

func TestTenantMember_RejectsCrossTenantURL_Add(t *testing.T) {
	called := false
	ms := &stubMemberService{
		add: func(_ context.Context, _ string, _ uint64, _ types.TenantRole, _ *string) (*types.TenantMember, error) {
			called = true
			return nil, nil
		},
	}
	us := &stubMemberUserService{
		getByEmail: func(_ context.Context, _ string) (*types.User, error) {
			t.Fatalf("user lookup must not run when :id is rejected upfront")
			return nil, nil
		},
	}
	h := newTestMemberHandler(ms, us)
	body := map[string]any{"email": "bob@x.com", "role": "contributor"}
	w := doJSONWithCtx(t, memberTestRouter(h), http.MethodPost, "/tenants/5/members", body,
		memberCtxOpts{callerID: "u1", tenantID: 1})
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 on cross-tenant add, got %d body=%s", w.Code, w.Body.String())
	}
	if called {
		t.Fatalf("AddMember must NOT be reached")
	}
}

func TestTenantMember_RejectsCrossTenantURL_Update(t *testing.T) {
	ms := &stubMemberService{updateRole: func(context.Context, string, uint64, types.TenantRole) error {
		t.Fatalf("UpdateRole must not be reached")
		return nil
	}}
	h := newTestMemberHandler(ms, &stubMemberUserService{})
	body := map[string]any{"role": "admin"}
	w := doJSONWithCtx(t, memberTestRouter(h), http.MethodPut, "/tenants/5/members/u2", body,
		memberCtxOpts{callerID: "u1", tenantID: 1})
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", w.Code)
	}
}

func TestTenantMember_RejectsCrossTenantURL_Remove(t *testing.T) {
	ms := &stubMemberService{remove: func(context.Context, string, uint64) error {
		t.Fatalf("RemoveMember must not be reached")
		return nil
	}}
	h := newTestMemberHandler(ms, &stubMemberUserService{})
	w := doJSONWithCtx(t, memberTestRouter(h), http.MethodDelete, "/tenants/5/members/u2", nil,
		memberCtxOpts{callerID: "u1", tenantID: 1})
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", w.Code)
	}
}

func TestTenantMember_RejectsCrossTenantURL_Leave(t *testing.T) {
	// Even LeaveTenant — although functionally a no-op for non-members
	// — must reject mismatched URLs to keep the handler contract
	// uniform with the other endpoints.
	ms := &stubMemberService{remove: func(context.Context, string, uint64) error {
		t.Fatalf("RemoveMember must not be reached")
		return nil
	}}
	h := newTestMemberHandler(ms, &stubMemberUserService{})
	w := doJSONWithCtx(t, memberTestRouter(h), http.MethodPost, "/tenants/5/leave", nil,
		memberCtxOpts{callerID: "u1", tenantID: 1})
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", w.Code)
	}
}

func TestTenantMember_CrossTenantSuperuserBypassesURLCheck(t *testing.T) {
	// CanAccessAllTenants + EnableCrossTenantAccess is the documented
	// escape hatch (mirrors middleware/rbac.go). Once flipped on, an
	// org-level operator can drive any tenant's member list from any
	// session — including a session whose active tenant != URL.
	called := false
	ms := &stubMemberService{
		listTenant: func(_ context.Context, tenantID uint64) ([]*types.TenantMember, error) {
			called = true
			if tenantID != 5 {
				t.Fatalf("expected tenant 5 to reach service, got %d", tenantID)
			}
			return nil, nil
		},
	}
	us := &stubMemberUserService{
		getByID: func(_ context.Context, _ string) (*types.User, error) { return nil, apprepo.ErrUserNotFound },
	}
	h := newTestMemberHandler(ms, us)
	w := doJSONWithCtx(t, memberTestRouter(h), http.MethodGet, "/tenants/5/members", nil,
		memberCtxOpts{
			callerID: "u-superuser",
			tenantID: 1,
			user:     &types.User{ID: "u-superuser", CanAccessAllTenants: true},
		})
	if w.Code != http.StatusOK {
		t.Fatalf("superuser bypass must reach service, got %d body=%s", w.Code, w.Body.String())
	}
	if !called {
		t.Fatalf("service must be reached for the superuser bypass")
	}
}

func TestTenantMember_SuperuserBypassRequiresFeatureFlag(t *testing.T) {
	// Just having user.CanAccessAllTenants on the User struct is not
	// enough — the cluster operator must also have flipped
	// cfg.Tenant.EnableCrossTenantAccess. Otherwise a stale token
	// claim couldn't be revoked operationally.
	called := false
	ms := &stubMemberService{
		listTenant: func(_ context.Context, _ uint64) ([]*types.TenantMember, error) {
			called = true
			return nil, nil
		},
	}
	// Build the router with the flag explicitly off — the carve-out
	// now lives in middleware.RequirePathTenantMatch, which the router
	// helper mounts.
	h := NewTenantMemberHandler(ms, &stubMemberUserService{})
	router := memberTestRouterWithCfg(h, &config.Config{
		Tenant: &config.TenantConfig{EnableCrossTenantAccess: false},
	})
	w := doJSONWithCtx(t, router, http.MethodGet, "/tenants/5/members", nil,
		memberCtxOpts{
			callerID: "u-superuser",
			tenantID: 1,
			user:     &types.User{ID: "u-superuser", CanAccessAllTenants: true},
		})
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 when feature flag is off, got %d body=%s", w.Code, w.Body.String())
	}
	if called {
		t.Fatalf("service must not be reached without the feature flag")
	}
}

// ---------- Hydration + invited_by ----------

func TestTenantMember_ListMembers_UsesBatchedUserLookup(t *testing.T) {
	// Regression for the N+1 finding: the handler should call
	// GetUsersByIDs exactly once, NOT GetUserByID per row.
	now := time.Now()
	ms := &stubMemberService{
		listTenant: func(_ context.Context, _ uint64) ([]*types.TenantMember, error) {
			return []*types.TenantMember{
				{UserID: "u1", TenantID: 1, Role: types.TenantRoleOwner, Status: types.TenantMemberStatusActive, JoinedAt: now},
				{UserID: "u2", TenantID: 1, Role: types.TenantRoleAdmin, Status: types.TenantMemberStatusActive, JoinedAt: now},
				{UserID: "u3", TenantID: 1, Role: types.TenantRoleViewer, Status: types.TenantMemberStatusActive, JoinedAt: now},
			}, nil
		},
	}
	batchCalls := 0
	singleCalls := 0
	us := &stubMemberUserService{
		getByIDs: func(_ context.Context, ids []string) (map[string]*types.User, error) {
			batchCalls++
			out := map[string]*types.User{}
			for _, id := range ids {
				out[id] = &types.User{ID: id, Username: id, Email: id + "@x.com"}
			}
			return out, nil
		},
		getByID: func(_ context.Context, _ string) (*types.User, error) {
			singleCalls++
			return nil, apprepo.ErrUserNotFound
		},
	}
	h := newTestMemberHandler(ms, us)
	w := doJSON(t, memberTestRouter(h), http.MethodGet, "/tenants/1/members", nil, "u-owner")
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	if batchCalls != 1 {
		t.Fatalf("GetUsersByIDs should be called exactly once, got %d", batchCalls)
	}
	if singleCalls != 0 {
		t.Fatalf("the handler must not fall back to per-row GetUserByID, got %d calls", singleCalls)
	}
}

func TestTenantMember_AddMember_SyntheticCallerLeavesInvitedByNull(t *testing.T) {
	// X-API-Key path attaches a synthetic "system-<tenantID>" user.
	// Recording that as invited_by would permanently break any future
	// join-with-users UX; the handler must skip it.
	captured := struct{ invited *string }{}
	ms := &stubMemberService{
		add: func(_ context.Context, _ string, _ uint64, _ types.TenantRole, invitedBy *string) (*types.TenantMember, error) {
			captured.invited = invitedBy
			return &types.TenantMember{UserID: "u-bob", TenantID: 1, Role: types.TenantRoleContributor, Status: types.TenantMemberStatusActive, JoinedAt: time.Now()}, nil
		},
	}
	us := &stubMemberUserService{
		getByEmail: func(_ context.Context, _ string) (*types.User, error) {
			return &types.User{ID: "u-bob", Email: "bob@x.com"}, nil
		},
	}
	h := newTestMemberHandler(ms, us)
	body := map[string]any{"email": "bob@x.com", "role": "contributor"}
	w := doJSON(t, memberTestRouter(h), http.MethodPost, "/tenants/1/members", body, "system-1")
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d body=%s", w.Code, w.Body.String())
	}
	if captured.invited != nil {
		t.Fatalf("invited_by must be nil for synthetic caller; got %q", *captured.invited)
	}
}
