package handler

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
)

type crossTenantAccessUserService struct {
	interfaces.UserService
	users   map[string]*types.User
	changed bool
	err     error
}

func (s *crossTenantAccessUserService) GetUserByEmail(_ context.Context, email string) (*types.User, error) {
	for _, user := range s.users {
		if user.Email == email {
			return user, nil
		}
	}
	return nil, repository.ErrUserNotFound
}

func (s *crossTenantAccessUserService) GrantSystemAdmin(
	_ context.Context, userID string,
) (*types.User, bool, error) {
	if s.err != nil {
		return nil, false, s.err
	}
	user := s.users[userID]
	if user == nil {
		return nil, false, repository.ErrUserNotFound
	}
	changed := !user.IsSystemAdmin
	user.IsSystemAdmin = true
	return user, changed, nil
}

func (s *crossTenantAccessUserService) GrantCrossTenantAccess(
	_ context.Context, userID string,
) (*types.User, bool, error) {
	if s.err != nil {
		return nil, false, s.err
	}
	user := s.users[userID]
	user.CanAccessAllTenants = true
	return user, s.changed, nil
}

func (s *crossTenantAccessUserService) RevokeCrossTenantAccess(
	_ context.Context, userID, actorID string,
) (*types.User, bool, error) {
	if userID == actorID {
		return nil, false, repository.ErrCannotRevokeOwnCrossTenantAccess
	}
	if s.err != nil {
		return nil, false, s.err
	}
	user := s.users[userID]
	user.CanAccessAllTenants = false
	return user, s.changed, nil
}

func (s *crossTenantAccessUserService) ListCrossTenantAccessUsers(
	context.Context, int, int,
) ([]*types.User, int64, error) {
	users := make([]*types.User, 0, len(s.users))
	for _, user := range s.users {
		if user.CanAccessAllTenants {
			users = append(users, user)
		}
	}
	return users, int64(len(users)), s.err
}

func (s *crossTenantAccessUserService) RevokeSystemAdmin(
	context.Context, string, string,
) (*types.User, error) {
	return nil, s.err
}

func crossTenantAccessHandlerRouter(h *SystemHandler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		ctx := context.WithValue(c.Request.Context(), types.UserIDContextKey, "actor")
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	})
	router.POST("/grant", h.GrantCrossTenantAccess)
	router.POST("/promote", h.PromoteUserToSystemAdmin)
	router.POST("/revoke", h.RevokeCrossTenantAccess)
	router.POST("/revoke-admin", h.RevokeSystemAdmin)
	router.GET("/list", h.ListCrossTenantAccessUsers)
	return router
}

func TestPromoteSystemAdminUsesDedicatedGrantAndAudits(t *testing.T) {
	target := &types.User{ID: "target", Email: "target@example.com", Username: "target"}
	users := &crossTenantAccessUserService{users: map[string]*types.User{"target": target}}
	audit := &capturingAuditService{}
	router := crossTenantAccessHandlerRouter(&SystemHandler{userSvc: users, auditSvc: audit})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/promote", bytes.NewBufferString(`{"email":"target@example.com"}`))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK || !target.IsSystemAdmin {
		t.Fatalf("promote status=%d user=%+v body=%s", w.Code, target, w.Body.String())
	}
	if len(audit.entries) != 1 || audit.entries[0].Action != types.AuditActionSystemAdminPromoted {
		t.Fatalf("promote audit entries=%+v", audit.entries)
	}
}

func TestGrantCrossTenantAccessUpdatesUserAndAudits(t *testing.T) {
	target := &types.User{ID: "target", Email: "target@example.com", Username: "target"}
	users := &crossTenantAccessUserService{
		users:   map[string]*types.User{"target": target},
		changed: true,
	}
	audit := &capturingAuditService{}
	router := crossTenantAccessHandlerRouter(&SystemHandler{userSvc: users, auditSvc: audit})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/grant", bytes.NewBufferString(`{"email":"target@example.com"}`))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK || !target.CanAccessAllTenants {
		t.Fatalf("grant status=%d user=%+v body=%s", w.Code, target, w.Body.String())
	}
	if len(audit.entries) != 1 || audit.entries[0].Action != types.AuditActionCrossTenantAccessGranted {
		t.Fatalf("grant audit entries=%+v", audit.entries)
	}
}

func TestRevokeCrossTenantAccessRejectsSelf(t *testing.T) {
	users := &crossTenantAccessUserService{users: map[string]*types.User{}}
	router := crossTenantAccessHandlerRouter(&SystemHandler{userSvc: users})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/revoke", bytes.NewBufferString(`{"user_id":"actor"}`))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("self revoke status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestPrivilegeRevocationReportsLastCrossTenantAccessManager(t *testing.T) {
	users := &crossTenantAccessUserService{
		users: map[string]*types.User{"target": {ID: "target"}},
		err:   repository.ErrLastCrossTenantAccessManager,
	}
	router := crossTenantAccessHandlerRouter(&SystemHandler{userSvc: users})

	for _, path := range []string{"/revoke", "/revoke-admin"} {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, path, bytes.NewBufferString(`{"user_id":"target"}`))
		req.Header.Set("Content-Type", "application/json")
		router.ServeHTTP(w, req)

		if w.Code != http.StatusBadRequest ||
			!bytes.Contains(w.Body.Bytes(), []byte("last system administrator with cross-tenant access")) {
			t.Fatalf("%s status=%d body=%s", path, w.Code, w.Body.String())
		}
	}
}

func TestListCrossTenantAccessUsersReturnsOnlyEnabledUsers(t *testing.T) {
	users := &crossTenantAccessUserService{users: map[string]*types.User{
		"enabled":  {ID: "enabled", CanAccessAllTenants: true},
		"disabled": {ID: "disabled"},
	}}
	router := crossTenantAccessHandlerRouter(&SystemHandler{userSvc: users})

	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/list?limit=200", nil))
	if w.Code != http.StatusOK || !bytes.Contains(w.Body.Bytes(), []byte(`"total":1`)) {
		t.Fatalf("list status=%d body=%s", w.Code, w.Body.String())
	}
}
