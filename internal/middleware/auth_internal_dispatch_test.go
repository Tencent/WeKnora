package middleware

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
)

// dispatchUserService serves users by ID and fails the test if the request
// is authenticated by token instead.
type dispatchUserService struct {
	interfaces.UserService
	t     *testing.T
	users map[string]*types.User
}

func (s *dispatchUserService) GetUserByID(_ context.Context, id string) (*types.User, error) {
	if u, ok := s.users[id]; ok {
		return u, nil
	}
	return nil, errors.New("not found")
}

func (s *dispatchUserService) ValidateToken(context.Context, string) (*types.User, uint64, error) {
	s.t.Fatalf("an internal dispatch must not fall through to token authentication")
	return nil, 0, nil
}

func runInternalDispatch(
	ctx context.Context, t *testing.T, users map[string]*types.User, members *fakeMemberService, header http.Header,
) (int, types.Caller) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	var caller types.Caller
	r.Use(Auth(
		&fakeTenantService{tenant: &types.Tenant{ID: 7}},
		&dispatchUserService{t: t, users: users},
		members, nil, cfgWithRBAC(true),
	))
	r.GET("/api/v1/knowledge/:id", func(c *gin.Context) {
		caller = types.CallerFromContext(c.Request.Context())
		c.Status(http.StatusNoContent)
	})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/knowledge/k1", nil).WithContext(ctx)
	for k, v := range header {
		req.Header[k] = v
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w.Code, caller
}

func TestInternalDispatchRebuildsSessionFromDatabase(t *testing.T) {
	members := newFakeMemberService()
	members.seedActive("u1", 7, types.TenantRoleContributor)
	users := map[string]*types.User{"u1": {ID: "u1", TenantID: 7, IsActive: true}}
	ctx := types.WithInternalDispatch(context.Background(), types.InternalDispatch{UserID: "u1", TenantID: 7})

	code, caller := runInternalDispatch(ctx, t, users, members, http.Header{
		// Credentials on a dispatched request are ignored, never honored.
		"Authorization": {"Bearer forged"},
		"X-Api-Key":     {"sk-forged"},
		"X-Tenant-Id":   {"99"},
	})
	if code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", code)
	}
	want := types.Caller{TenantID: 7, UserID: "u1", Role: types.TenantRoleContributor}
	if caller != want {
		t.Fatalf("caller = %+v, want %+v", caller, want)
	}
}

func TestInternalDispatchRejectsInactiveUser(t *testing.T) {
	members := newFakeMemberService()
	members.seedActive("u1", 7, types.TenantRoleAdmin)
	users := map[string]*types.User{"u1": {ID: "u1", TenantID: 7, IsActive: false}}
	ctx := types.WithInternalDispatch(context.Background(), types.InternalDispatch{UserID: "u1", TenantID: 7})

	if code, _ := runInternalDispatch(ctx, t, users, members, nil); code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", code)
	}
	if code, _ := runInternalDispatch(
		types.WithInternalDispatch(context.Background(), types.InternalDispatch{UserID: "ghost", TenantID: 7}),
		t, users, members, nil,
	); code != http.StatusUnauthorized {
		t.Fatalf("unknown user status = %d, want 401", code)
	}
}

func TestInternalDispatchRequiresMembershipInDispatchTenant(t *testing.T) {
	members := newFakeMemberService()
	members.seedActive("u1", 8, types.TenantRoleAdmin)
	users := map[string]*types.User{"u1": {ID: "u1", TenantID: 8, IsActive: true}}
	ctx := types.WithInternalDispatch(context.Background(), types.InternalDispatch{UserID: "u1", TenantID: 7})

	if code, _ := runInternalDispatch(ctx, t, users, members, nil); code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", code)
	}
}
