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

	"github.com/Tencent/WeKnora/internal/application/service"
	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/middleware"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
)

// 本文件覆盖 01-spec AC-1/AC-2/AC-3/AC-4/AC-5/AC-7/AC-8 的两个新接口：
//   POST /tenants/:id/members/create
//   POST /tenants/:id/members/:user_id/reset-password
//
// 断言口径与 02-plan 第 4 节矩阵一致；真正的角色矩阵纯函数见
// internal/application/service/member_role_matrix_test.go。
// 这里重点验证 handler 的：权限/错误码映射、越权不落数据、密码不出现在响应、
// 系统管理员按 owner、跨租户 URL 拒绝、重置后审计通道被调用。

type accountTestUserService struct {
	interfaces.UserService
	adminCreateUser    func(ctx context.Context, req *types.AdminCreateUserRequest, provisioning types.TenantProvisioningMode) (*types.User, string, error)
	adminResetPassword func(ctx context.Context, userID string, newPassword string) error
	deleteUser         func(ctx context.Context, id string) error
	getByEmail         func(ctx context.Context, email string) (*types.User, error)

	createCalls   int
	resetCalls    int
	deleteCalls   int
	createdReq    *types.AdminCreateUserRequest
	createdModes  []types.TenantProvisioningMode
	resetTargetID string
	resetPassword string
}

func (s *accountTestUserService) AdminCreateUser(
	ctx context.Context, req *types.AdminCreateUserRequest, provisioning types.TenantProvisioningMode,
) (*types.User, string, error) {
	s.createCalls++
	s.createdReq = req
	s.createdModes = append(s.createdModes, provisioning)
	if s.adminCreateUser != nil {
		return s.adminCreateUser(ctx, req, provisioning)
	}
	return &types.User{ID: "u-new", Username: req.Username, Email: req.Email}, "", nil
}

func (s *accountTestUserService) AdminResetPassword(ctx context.Context, userID string, newPassword string) error {
	s.resetCalls++
	s.resetTargetID = userID
	s.resetPassword = newPassword
	if s.adminResetPassword != nil {
		return s.adminResetPassword(ctx, userID, newPassword)
	}
	return nil
}

func (s *accountTestUserService) DeleteUser(ctx context.Context, id string) error {
	s.deleteCalls++
	if s.deleteUser != nil {
		return s.deleteUser(ctx, id)
	}
	return nil
}

func (s *accountTestUserService) GetUserByEmail(ctx context.Context, email string) (*types.User, error) {
	if s.getByEmail != nil {
		return s.getByEmail(ctx, email)
	}
	return nil, errors.New("not configured")
}

type accountTestMemberService struct {
	interfaces.TenantMemberService
	add           func(ctx context.Context, userID string, tenantID uint64, role types.TenantRole, invitedBy *string) (*types.TenantMember, error)
	getMembership func(ctx context.Context, userID string, tenantID uint64) (*types.TenantMember, error)

	addCalls           int
	getMembershipCalls int
	addedRole          types.TenantRole
	addedUserID        string
	addedInvitedBy     *string

	auditCalls      int
	auditTenantID   uint64
	auditTargetUser string
	auditTargetRole types.TenantRole
}

func (s *accountTestMemberService) AddMember(
	ctx context.Context, userID string, tenantID uint64, role types.TenantRole, invitedBy *string,
) (*types.TenantMember, error) {
	s.addCalls++
	s.addedUserID = userID
	s.addedRole = role
	s.addedInvitedBy = invitedBy
	if s.add != nil {
		return s.add(ctx, userID, tenantID, role, invitedBy)
	}
	return &types.TenantMember{
		UserID: userID, TenantID: tenantID, Role: role, Status: types.TenantMemberStatusActive,
	}, nil
}

func (s *accountTestMemberService) GetMembership(
	ctx context.Context, userID string, tenantID uint64,
) (*types.TenantMember, error) {
	s.getMembershipCalls++
	if s.getMembership != nil {
		return s.getMembership(ctx, userID, tenantID)
	}
	return nil, nil
}

// AuditMemberPasswordReset 让 stub 满足 interfaces.MemberPasswordResetAuditor，
// 这样 handler 走类型断言时必然命中，测试才能断言审计通道被调用。
func (s *accountTestMemberService) AuditMemberPasswordReset(
	_ context.Context, tenantID uint64, targetUserID string, targetRole types.TenantRole,
) {
	s.auditCalls++
	s.auditTenantID = tenantID
	s.auditTargetUser = targetUserID
	s.auditTargetRole = targetRole
}

const accountTestTenantID uint64 = 1

func accountTestRouter(h *TenantMemberHandler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(errorCapture()) // 复用 auth_register_invite_only_test.go 的助手
	cfg := &config.Config{Tenant: &config.TenantConfig{EnableCrossTenantAccess: true}}
	g := r.Group("/tenants/:id", middleware.RequirePathTenantMatch(cfg))
	g.POST("/members/create", h.CreateMemberAccount)
	g.POST("/members/:user_id/reset-password", h.ResetMemberPassword)
	return r
}

type accountCtxOpts struct {
	userID      string
	tenantID    uint64
	role        types.TenantRole
	systemAdmin bool
	user        *types.User
}

func doAccountJSON(
	t *testing.T, r *gin.Engine, method, path string, body any, opts accountCtxOpts,
) *httptest.ResponseRecorder {
	t.Helper()
	var payload []byte
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		payload = raw
	}
	req := httptest.NewRequest(method, path, bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")

	ctx := req.Context()
	if opts.tenantID == 0 {
		opts.tenantID = accountTestTenantID
	}
	ctx = context.WithValue(ctx, types.TenantIDContextKey, opts.tenantID)
	if opts.userID != "" {
		ctx = context.WithValue(ctx, types.UserIDContextKey, opts.userID)
	}
	if opts.role != "" {
		ctx = context.WithValue(ctx, types.TenantRoleContextKey, opts.role)
	}
	if opts.systemAdmin {
		ctx = context.WithValue(ctx, types.SystemAdminContextKey, true)
	}
	if opts.user != nil {
		ctx = context.WithValue(ctx, types.UserContextKey, opts.user)
	}
	req = req.WithContext(ctx)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func decodeResponseData(t *testing.T, w *httptest.ResponseRecorder) types.TenantMemberResponse {
	t.Helper()
	var resp struct {
		Success bool                       `json:"success"`
		Data    types.TenantMemberResponse `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v body=%s", err, w.Body.String())
	}
	if !resp.Success {
		t.Fatalf("expected success=true, body=%s", w.Body.String())
	}
	return resp.Data
}

func validCreateBody(role types.TenantRole) map[string]any {
	return map[string]any{
		"username": "New Member",
		"email":    "new-member@allstar.local",
		"password": "TestPass123!",
		"role":     role,
	}
}

// ---------- CreateMemberAccount：AC-1 / AC-4 ----------

func TestCreateMemberAccount_OwnerCanCreateAllNonOwnerRoles(t *testing.T) {
	for _, role := range []types.TenantRole{
		types.TenantRoleAdmin, types.TenantRoleContributor, types.TenantRoleViewer,
	} {
		t.Run(string(role), func(t *testing.T) {
			ms := &accountTestMemberService{}
			us := &accountTestUserService{}
			h := NewTenantMemberHandler(ms, us)

			w := doAccountJSON(t, accountTestRouter(h), http.MethodPost, "/tenants/1/members/create",
				validCreateBody(role), accountCtxOpts{userID: "u-owner", role: types.TenantRoleOwner})
			if w.Code != http.StatusCreated {
				t.Fatalf("owner creating %s: expected 201, got %d body=%s", role, w.Code, w.Body.String())
			}
			if us.createCalls != 1 {
				t.Fatalf("AdminCreateUser must be called once, got %d", us.createCalls)
			}
			if len(us.createdModes) != 1 || us.createdModes[0] != types.TenantProvisioningTenantless {
				t.Fatalf("must create tenantless user, got %v", us.createdModes)
			}
			if ms.addCalls != 1 || ms.addedRole != role {
				t.Fatalf("AddMember must be called with role %s, got calls=%d role=%s", role, ms.addCalls, ms.addedRole)
			}
			if ms.addedInvitedBy == nil || *ms.addedInvitedBy != "u-owner" {
				t.Fatalf("invited_by must be caller, got %v", ms.addedInvitedBy)
			}
			data := decodeResponseData(t, w)
			if data.Role != role {
				t.Fatalf("response role = %s, want %s", data.Role, role)
			}
			// AC-4/AC-7：响应绝不带密码。
			if strings.Contains(w.Body.String(), "TestPass123!") || strings.Contains(w.Body.String(), "password") {
				t.Fatalf("response must not leak password: %s", w.Body.String())
			}
		})
	}
}

func TestCreateMemberAccount_AdminCanOnlyCreateViewer(t *testing.T) {
	// admin -> viewer 允许
	ms := &accountTestMemberService{}
	us := &accountTestUserService{}
	h := NewTenantMemberHandler(ms, us)
	w := doAccountJSON(t, accountTestRouter(h), http.MethodPost, "/tenants/1/members/create",
		validCreateBody(types.TenantRoleViewer), accountCtxOpts{userID: "u-admin", role: types.TenantRoleAdmin})
	if w.Code != http.StatusCreated {
		t.Fatalf("admin creating viewer: expected 201, got %d body=%s", w.Code, w.Body.String())
	}

	// admin -> admin / contributor 越权（AC-1）
	for _, role := range []types.TenantRole{types.TenantRoleAdmin, types.TenantRoleContributor} {
		t.Run("deny-"+string(role), func(t *testing.T) {
			ms := &accountTestMemberService{}
			us := &accountTestUserService{}
			h := NewTenantMemberHandler(ms, us)
			w := doAccountJSON(t, accountTestRouter(h), http.MethodPost, "/tenants/1/members/create",
				validCreateBody(role), accountCtxOpts{userID: "u-admin", role: types.TenantRoleAdmin})
			if w.Code != http.StatusForbidden {
				t.Fatalf("admin creating %s must 403, got %d body=%s", role, w.Code, w.Body.String())
			}
			// AC-1：越权不产生任何数据 —— 用户创建与入组都不能被调用。
			if us.createCalls != 0 || ms.addCalls != 0 {
				t.Fatalf("denied request must not create data: create=%d add=%d", us.createCalls, ms.addCalls)
			}
		})
	}
}

func TestCreateMemberAccount_NobodyCanCreateOwner(t *testing.T) {
	for _, actor := range []types.TenantRole{types.TenantRoleOwner, types.TenantRoleAdmin} {
		t.Run(string(actor), func(t *testing.T) {
			ms := &accountTestMemberService{}
			us := &accountTestUserService{}
			h := NewTenantMemberHandler(ms, us)
			w := doAccountJSON(t, accountTestRouter(h), http.MethodPost, "/tenants/1/members/create",
				validCreateBody(types.TenantRoleOwner), accountCtxOpts{userID: "u-actor", role: actor})
			if w.Code != http.StatusBadRequest {
				t.Fatalf("%s creating owner must 400, got %d body=%s", actor, w.Code, w.Body.String())
			}
			if us.createCalls != 0 || ms.addCalls != 0 {
				t.Fatalf("owner creation must not reach services: create=%d add=%d", us.createCalls, ms.addCalls)
			}
		})
	}
}

func TestCreateMemberAccount_LowerRolesForbidden(t *testing.T) {
	for _, actor := range []types.TenantRole{types.TenantRoleContributor, types.TenantRoleViewer} {
		t.Run(string(actor), func(t *testing.T) {
			ms := &accountTestMemberService{}
			us := &accountTestUserService{}
			h := NewTenantMemberHandler(ms, us)
			w := doAccountJSON(t, accountTestRouter(h), http.MethodPost, "/tenants/1/members/create",
				validCreateBody(types.TenantRoleViewer), accountCtxOpts{userID: "u-low", role: actor})
			if w.Code != http.StatusForbidden {
				t.Fatalf("%s creating viewer must 403, got %d body=%s", actor, w.Code, w.Body.String())
			}
			if us.createCalls != 0 || ms.addCalls != 0 {
				t.Fatalf("denied request must not create data")
			}
		})
	}
}

func TestCreateMemberAccount_SystemAdminActsAsOwner(t *testing.T) {
	// 系统管理员即使当前空间角色是 viewer，也按 owner 处理（02-plan D4）。
	ms := &accountTestMemberService{}
	us := &accountTestUserService{}
	h := NewTenantMemberHandler(ms, us)
	w := doAccountJSON(t, accountTestRouter(h), http.MethodPost, "/tenants/1/members/create",
		validCreateBody(types.TenantRoleAdmin),
		accountCtxOpts{userID: "u-sys", role: types.TenantRoleViewer, systemAdmin: true})
	if w.Code != http.StatusCreated {
		t.Fatalf("system admin must act as owner, got %d body=%s", w.Code, w.Body.String())
	}
}

// ---------- CreateMemberAccount：AC-5 / AC-7 错误映射 ----------

func TestCreateMemberAccount_EmailAlreadyRegisteredMaps409(t *testing.T) {
	ms := &accountTestMemberService{}
	us := &accountTestUserService{
		adminCreateUser: func(context.Context, *types.AdminCreateUserRequest, types.TenantProvisioningMode) (*types.User, string, error) {
			return nil, "", service.ErrUserEmailExists
		},
	}
	h := NewTenantMemberHandler(ms, us)
	w := doAccountJSON(t, accountTestRouter(h), http.MethodPost, "/tenants/1/members/create",
		validCreateBody(types.TenantRoleViewer), accountCtxOpts{userID: "u-owner", role: types.TenantRoleOwner})
	if w.Code != http.StatusConflict {
		t.Fatalf("duplicate email must 409, got %d body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(strings.ToLower(w.Body.String()), "email") || !strings.Contains(w.Body.String(), "already registered") {
		t.Fatalf("409 must tell the caller the email is registered: %s", w.Body.String())
	}
	if ms.addCalls != 0 {
		t.Fatalf("conflict must not add membership")
	}
}

func TestCreateMemberAccount_IdentityConflictPrefersEmailMessage(t *testing.T) {
	// 邮箱已注册、用户名不同：AdminCreateUser 返回笼统 identity conflict，
	// handler 回查邮箱后必须给出「邮箱已注册」，避免前端误报「用户名占用」（AC-5）。
	ms := &accountTestMemberService{}
	us := &accountTestUserService{
		adminCreateUser: func(context.Context, *types.AdminCreateUserRequest, types.TenantProvisioningMode) (*types.User, string, error) {
			return nil, "", service.ErrUserIdentityConflict
		},
		getByEmail: func(_ context.Context, email string) (*types.User, error) {
			return &types.User{ID: "u-existing", Email: email}, nil
		},
	}
	h := NewTenantMemberHandler(ms, us)
	w := doAccountJSON(t, accountTestRouter(h), http.MethodPost, "/tenants/1/members/create",
		validCreateBody(types.TenantRoleViewer), accountCtxOpts{userID: "u-owner", role: types.TenantRoleOwner})
	if w.Code != http.StatusConflict {
		t.Fatalf("identity conflict must 409, got %d body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "email is already registered") {
		t.Fatalf("must prefer email message, got %s", w.Body.String())
	}
}

func TestCreateMemberAccount_IdentityConflictFallsBackToUsernameMessage(t *testing.T) {
	ms := &accountTestMemberService{}
	us := &accountTestUserService{
		adminCreateUser: func(context.Context, *types.AdminCreateUserRequest, types.TenantProvisioningMode) (*types.User, string, error) {
			return nil, "", service.ErrUserIdentityConflict
		},
		getByEmail: func(context.Context, string) (*types.User, error) {
			return nil, errors.New("not found")
		},
	}
	h := NewTenantMemberHandler(ms, us)
	w := doAccountJSON(t, accountTestRouter(h), http.MethodPost, "/tenants/1/members/create",
		validCreateBody(types.TenantRoleViewer), accountCtxOpts{userID: "u-owner", role: types.TenantRoleOwner})
	if w.Code != http.StatusConflict {
		t.Fatalf("identity conflict must 409, got %d body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "username is already taken") {
		t.Fatalf("must report username conflict, got %s", w.Body.String())
	}
}

func TestCreateMemberAccount_PasswordPolicyMaps400(t *testing.T) {
	ms := &accountTestMemberService{}
	us := &accountTestUserService{
		adminCreateUser: func(context.Context, *types.AdminCreateUserRequest, types.TenantProvisioningMode) (*types.User, string, error) {
			return nil, "", service.ErrPasswordPolicy
		},
	}
	h := NewTenantMemberHandler(ms, us)
	w := doAccountJSON(t, accountTestRouter(h), http.MethodPost, "/tenants/1/members/create",
		validCreateBody(types.TenantRoleViewer), accountCtxOpts{userID: "u-owner", role: types.TenantRoleOwner})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("password policy must 400, got %d body=%s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "TestPass123!") {
		t.Fatalf("error response must not echo the password: %s", w.Body.String())
	}
}

func TestCreateMemberAccount_AddMemberFailureRollsBackUser(t *testing.T) {
	// D5：建号成功但入组失败时，必须尽力软删刚建的用户。
	ms := &accountTestMemberService{
		add: func(context.Context, string, uint64, types.TenantRole, *string) (*types.TenantMember, error) {
			return nil, errors.New("membership write failed")
		},
	}
	us := &accountTestUserService{}
	h := NewTenantMemberHandler(ms, us)
	w := doAccountJSON(t, accountTestRouter(h), http.MethodPost, "/tenants/1/members/create",
		validCreateBody(types.TenantRoleViewer), accountCtxOpts{userID: "u-owner", role: types.TenantRoleOwner})
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("unexpected add failure must 500, got %d body=%s", w.Code, w.Body.String())
	}
	if us.deleteCalls != 1 {
		t.Fatalf("newly created user must be rolled back, deleteCalls=%d", us.deleteCalls)
	}
}

func TestCreateMemberAccount_InvalidInputRejectedBeforeServices(t *testing.T) {
	cases := []struct {
		name string
		body map[string]any
	}{
		{"invalid-role", map[string]any{"username": "New Member", "email": "x@allstar.local", "password": "TestPass123!", "role": "wizard"}},
		{"short-username", map[string]any{"username": "a", "email": "x@allstar.local", "password": "TestPass123!", "role": "viewer"}},
		{"long-username", map[string]any{"username": strings.Repeat("a", 51), "email": "x@allstar.local", "password": "TestPass123!", "role": "viewer"}},
		{"bad-email", map[string]any{"username": "New Member", "email": "not-an-email", "password": "TestPass123!", "role": "viewer"}},
		{"missing-password", map[string]any{"username": "New Member", "email": "x@allstar.local", "role": "viewer"}},
		{"missing-role", map[string]any{"username": "New Member", "email": "x@allstar.local", "password": "TestPass123!"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ms := &accountTestMemberService{}
			us := &accountTestUserService{}
			h := NewTenantMemberHandler(ms, us)
			w := doAccountJSON(t, accountTestRouter(h), http.MethodPost, "/tenants/1/members/create",
				tc.body, accountCtxOpts{userID: "u-owner", role: types.TenantRoleOwner})
			if w.Code != http.StatusBadRequest {
				t.Fatalf("%s must 400, got %d body=%s", tc.name, w.Code, w.Body.String())
			}
			if us.createCalls != 0 || ms.addCalls != 0 {
				t.Fatalf("%s must not reach services", tc.name)
			}
		})
	}
}

// ---------- AC-3：跨租户隔离 ----------

func TestCreateMemberAccount_CrossTenantRejected(t *testing.T) {
	ms := &accountTestMemberService{}
	us := &accountTestUserService{}
	h := NewTenantMemberHandler(ms, us)
	// 活动空间 1，URL 指向 5：中间件必须 403，服务不可达。
	w := doAccountJSON(t, accountTestRouter(h), http.MethodPost, "/tenants/5/members/create",
		validCreateBody(types.TenantRoleViewer),
		accountCtxOpts{userID: "u-owner", tenantID: 1, role: types.TenantRoleOwner})
	if w.Code != http.StatusForbidden {
		t.Fatalf("cross-tenant create must 403, got %d body=%s", w.Code, w.Body.String())
	}
	if us.createCalls != 0 || ms.addCalls != 0 {
		t.Fatalf("cross-tenant request must not reach services")
	}
}

// ---------- ResetMemberPassword：AC-2 / AC-8 ----------

func validResetBody() map[string]any {
	return map[string]any{"new_password": "NewPass123!"}
}

func TestResetMemberPassword_OwnerMatrix(t *testing.T) {
	for _, targetRole := range []types.TenantRole{
		types.TenantRoleAdmin, types.TenantRoleContributor, types.TenantRoleViewer,
	} {
		t.Run(string(targetRole), func(t *testing.T) {
			ms := &accountTestMemberService{
				getMembership: func(_ context.Context, userID string, tenantID uint64) (*types.TenantMember, error) {
					return &types.TenantMember{UserID: userID, TenantID: tenantID, Role: targetRole}, nil
				},
			}
			us := &accountTestUserService{}
			h := NewTenantMemberHandler(ms, us)
			w := doAccountJSON(t, accountTestRouter(h), http.MethodPost, "/tenants/1/members/u-target/reset-password",
				validResetBody(), accountCtxOpts{userID: "u-owner", role: types.TenantRoleOwner})
			if w.Code != http.StatusOK {
				t.Fatalf("owner resetting %s: expected 200, got %d body=%s", targetRole, w.Code, w.Body.String())
			}
			if us.resetCalls != 1 || us.resetTargetID != "u-target" || us.resetPassword != "NewPass123!" {
				t.Fatalf("AdminResetPassword not called correctly: calls=%d target=%s", us.resetCalls, us.resetTargetID)
			}
			// AC-2 / AC-8：成功审计必须被触发，且目标角色正确。
			if ms.auditCalls != 1 || ms.auditTargetUser != "u-target" || ms.auditTargetRole != targetRole {
				t.Fatalf("audit not emitted correctly: calls=%d target=%s role=%s",
					ms.auditCalls, ms.auditTargetUser, ms.auditTargetRole)
			}
			if strings.Contains(w.Body.String(), "NewPass123!") || strings.Contains(w.Body.String(), "password") {
				t.Fatalf("reset response must not leak password: %s", w.Body.String())
			}
		})
	}
}

func TestResetMemberPassword_AdminMatrix(t *testing.T) {
	for _, targetRole := range []types.TenantRole{
		types.TenantRoleContributor, types.TenantRoleViewer,
	} {
		t.Run("allow-"+string(targetRole), func(t *testing.T) {
			ms := &accountTestMemberService{
				getMembership: func(_ context.Context, userID string, tenantID uint64) (*types.TenantMember, error) {
					return &types.TenantMember{UserID: userID, TenantID: tenantID, Role: targetRole}, nil
				},
			}
			us := &accountTestUserService{}
			h := NewTenantMemberHandler(ms, us)
			w := doAccountJSON(t, accountTestRouter(h), http.MethodPost, "/tenants/1/members/u-target/reset-password",
				validResetBody(), accountCtxOpts{userID: "u-admin", role: types.TenantRoleAdmin})
			if w.Code != http.StatusOK {
				t.Fatalf("admin resetting %s: expected 200, got %d body=%s", targetRole, w.Code, w.Body.String())
			}
		})
	}

	for _, targetRole := range []types.TenantRole{
		types.TenantRoleAdmin, types.TenantRoleOwner,
	} {
		t.Run("deny-"+string(targetRole), func(t *testing.T) {
			ms := &accountTestMemberService{
				getMembership: func(_ context.Context, userID string, tenantID uint64) (*types.TenantMember, error) {
					return &types.TenantMember{UserID: userID, TenantID: tenantID, Role: targetRole}, nil
				},
			}
			us := &accountTestUserService{}
			h := NewTenantMemberHandler(ms, us)
			w := doAccountJSON(t, accountTestRouter(h), http.MethodPost, "/tenants/1/members/u-target/reset-password",
				validResetBody(), accountCtxOpts{userID: "u-admin", role: types.TenantRoleAdmin})
			if w.Code != http.StatusForbidden {
				t.Fatalf("admin resetting %s must 403, got %d body=%s", targetRole, w.Code, w.Body.String())
			}
			if us.resetCalls != 0 {
				t.Fatalf("denied reset must not change any password")
			}
			if ms.auditCalls != 0 {
				t.Fatalf("denied reset must not write a success audit")
			}
		})
	}
}

func TestResetMemberPassword_LowerRolesForbidden(t *testing.T) {
	for _, actor := range []types.TenantRole{types.TenantRoleContributor, types.TenantRoleViewer} {
		t.Run(string(actor), func(t *testing.T) {
			ms := &accountTestMemberService{
				getMembership: func(_ context.Context, userID string, tenantID uint64) (*types.TenantMember, error) {
					return &types.TenantMember{UserID: userID, TenantID: tenantID, Role: types.TenantRoleViewer}, nil
				},
			}
			us := &accountTestUserService{}
			h := NewTenantMemberHandler(ms, us)
			w := doAccountJSON(t, accountTestRouter(h), http.MethodPost, "/tenants/1/members/u-target/reset-password",
				validResetBody(), accountCtxOpts{userID: "u-low", role: actor})
			if w.Code != http.StatusForbidden {
				t.Fatalf("%s resetting viewer must 403, got %d body=%s", actor, w.Code, w.Body.String())
			}
			if us.resetCalls != 0 {
				t.Fatalf("denied reset must not change any password")
			}
		})
	}
}

func TestResetMemberPassword_SelfReturns400(t *testing.T) {
	// AC-2：不能重置本人，本人走修改密码接口。判定发生在查成员之前。
	ms := &accountTestMemberService{}
	us := &accountTestUserService{}
	h := NewTenantMemberHandler(ms, us)
	w := doAccountJSON(t, accountTestRouter(h), http.MethodPost, "/tenants/1/members/u-self/reset-password",
		validResetBody(), accountCtxOpts{userID: "u-self", role: types.TenantRoleOwner})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("self reset must 400, got %d body=%s", w.Code, w.Body.String())
	}
	if ms.getMembershipCalls != 0 || us.resetCalls != 0 {
		t.Fatalf("self reset must not look up membership or reset: lookup=%d reset=%d",
			ms.getMembershipCalls, us.resetCalls)
	}
}

func TestResetMemberPassword_NonMemberReturns404(t *testing.T) {
	ms := &accountTestMemberService{
		getMembership: func(context.Context, string, uint64) (*types.TenantMember, error) {
			return nil, nil
		},
	}
	us := &accountTestUserService{}
	h := NewTenantMemberHandler(ms, us)
	w := doAccountJSON(t, accountTestRouter(h), http.MethodPost, "/tenants/1/members/u-outsider/reset-password",
		validResetBody(), accountCtxOpts{userID: "u-owner", role: types.TenantRoleOwner})
	if w.Code != http.StatusNotFound {
		t.Fatalf("non-member target must 404, got %d body=%s", w.Code, w.Body.String())
	}
	if us.resetCalls != 0 {
		t.Fatalf("non-member target must not reset any password")
	}
}

func TestResetMemberPassword_GetMembershipErrorMaps500(t *testing.T) {
	ms := &accountTestMemberService{
		getMembership: func(context.Context, string, uint64) (*types.TenantMember, error) {
			return nil, errors.New("db down")
		},
	}
	h := NewTenantMemberHandler(ms, &accountTestUserService{})
	w := doAccountJSON(t, accountTestRouter(h), http.MethodPost, "/tenants/1/members/u-target/reset-password",
		validResetBody(), accountCtxOpts{userID: "u-owner", role: types.TenantRoleOwner})
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("membership lookup error must 500, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestResetMemberPassword_PasswordPolicyMaps400(t *testing.T) {
	ms := &accountTestMemberService{
		getMembership: func(_ context.Context, userID string, tenantID uint64) (*types.TenantMember, error) {
			return &types.TenantMember{UserID: userID, TenantID: tenantID, Role: types.TenantRoleViewer}, nil
		},
	}
	us := &accountTestUserService{
		adminResetPassword: func(context.Context, string, string) error {
			return service.ErrPasswordPolicy
		},
	}
	h := NewTenantMemberHandler(ms, us)
	w := doAccountJSON(t, accountTestRouter(h), http.MethodPost, "/tenants/1/members/u-target/reset-password",
		map[string]any{"new_password": "short"}, accountCtxOpts{userID: "u-owner", role: types.TenantRoleOwner})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("password policy must 400, got %d body=%s", w.Code, w.Body.String())
	}
	if ms.auditCalls != 0 {
		t.Fatalf("failed reset must not write a success audit")
	}
	if strings.Contains(w.Body.String(), "short") {
		t.Fatalf("error response must not echo the password: %s", w.Body.String())
	}
}

func TestResetMemberPassword_SystemAdminActsAsOwner(t *testing.T) {
	// 系统管理员即使当前空间角色是 viewer，也能重置 admin 的密码（D4）。
	ms := &accountTestMemberService{
		getMembership: func(_ context.Context, userID string, tenantID uint64) (*types.TenantMember, error) {
			return &types.TenantMember{UserID: userID, TenantID: tenantID, Role: types.TenantRoleAdmin}, nil
		},
	}
	us := &accountTestUserService{}
	h := NewTenantMemberHandler(ms, us)
	w := doAccountJSON(t, accountTestRouter(h), http.MethodPost, "/tenants/1/members/u-target/reset-password",
		validResetBody(),
		accountCtxOpts{userID: "u-sys", role: types.TenantRoleViewer, systemAdmin: true})
	if w.Code != http.StatusOK {
		t.Fatalf("system admin must act as owner, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestResetMemberPassword_CrossTenantRejected(t *testing.T) {
	ms := &accountTestMemberService{
		getMembership: func(context.Context, string, uint64) (*types.TenantMember, error) {
			t.Fatalf("cross-tenant request must not reach membership lookup")
			return nil, nil
		},
	}
	us := &accountTestUserService{}
	h := NewTenantMemberHandler(ms, us)
	w := doAccountJSON(t, accountTestRouter(h), http.MethodPost, "/tenants/5/members/u-target/reset-password",
		validResetBody(),
		accountCtxOpts{userID: "u-owner", tenantID: 1, role: types.TenantRoleOwner})
	if w.Code != http.StatusForbidden {
		t.Fatalf("cross-tenant reset must 403, got %d body=%s", w.Code, w.Body.String())
	}
	if us.resetCalls != 0 {
		t.Fatalf("cross-tenant reset must not change any password")
	}
}

func TestResetMemberPassword_InvalidBodyRejectedBeforeLookup(t *testing.T) {
	for _, body := range []map[string]any{
		{},
		{"new_password": ""},
	} {
		ms := &accountTestMemberService{}
		h := NewTenantMemberHandler(ms, &accountTestUserService{})
		w := doAccountJSON(t, accountTestRouter(h), http.MethodPost, "/tenants/1/members/u-target/reset-password",
			body, accountCtxOpts{userID: "u-owner", role: types.TenantRoleOwner})
		if w.Code != http.StatusBadRequest {
			t.Fatalf("invalid body %v must 400, got %d body=%s", body, w.Code, w.Body.String())
		}
		if ms.getMembershipCalls != 0 {
			t.Fatalf("invalid body must be rejected before membership lookup")
		}
	}
}
