package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
)

// tenantDeleteErrorCapture renders the full AppError struct (not just the
// message) so tests can assert the typed error code, mirroring
// tenantPolicyErrorCapture.
func tenantDeleteErrorCapture() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Next()
		if len(c.Errors) == 0 {
			return
		}
		if appErr, ok := c.Errors.Last().Err.(*apperrors.AppError); ok {
			c.JSON(appErr.HTTPCode, gin.H{"error": appErr})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": c.Errors.Last().Err.Error()})
	}
}

type tenantDeleteUserService struct {
	interfaces.UserService
	user *types.User
}

func (s *tenantDeleteUserService) GetCurrentUser(context.Context) (*types.User, error) {
	return s.user, nil
}

type tenantDeleteMemberService struct {
	interfaces.TenantMemberService
	members []*types.TenantMember
}

func (s *tenantDeleteMemberService) ListByUser(context.Context, string) ([]*types.TenantMember, error) {
	return s.members, nil
}

type tenantDeleteTenantService struct {
	interfaces.TenantService
	deleteCalls int
}

func (s *tenantDeleteTenantService) DeleteTenant(context.Context, uint64) error {
	s.deleteCalls++
	return nil
}

func newTenantDeleteRouter(h *TenantHandler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(tenantDeleteErrorCapture())
	r.DELETE("/tenants/:id", h.DeleteTenant)
	return r
}

func doDeleteTenant(t *testing.T, r *gin.Engine, id string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodDelete, "/tenants/"+id, nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestDeleteTenantRejectsLastWorkspace(t *testing.T) {
	svc := &tenantDeleteTenantService{}
	h := &TenantHandler{
		service:     svc,
		userService: &tenantDeleteUserService{user: &types.User{ID: "owner"}},
		memberService: &tenantDeleteMemberService{members: []*types.TenantMember{
			{UserID: "owner", TenantID: 7, Role: types.TenantRoleOwner, Status: types.TenantMemberStatusActive},
		}},
	}
	r := newTenantDeleteRouter(h)
	w := doDeleteTenant(t, r, "7")

	if w.Code != http.StatusForbidden {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if svc.deleteCalls != 0 {
		t.Fatalf("DeleteTenant called %d times, want 0", svc.deleteCalls)
	}
	if !strings.Contains(w.Body.String(), `"code":2006`) {
		t.Fatalf("response missing typed code 2006: %s", w.Body.String())
	}
}

func TestDeleteTenantAllowsWhenAnotherMembershipRemains(t *testing.T) {
	svc := &tenantDeleteTenantService{}
	h := &TenantHandler{
		service:     svc,
		userService: &tenantDeleteUserService{user: &types.User{ID: "owner"}},
		memberService: &tenantDeleteMemberService{members: []*types.TenantMember{
			{UserID: "owner", TenantID: 7, Status: types.TenantMemberStatusActive},
			{UserID: "owner", TenantID: 9, Status: types.TenantMemberStatusActive},
		}},
	}
	r := newTenantDeleteRouter(h)
	w := doDeleteTenant(t, r, "7")

	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if svc.deleteCalls != 1 {
		t.Fatalf("DeleteTenant called %d times, want 1", svc.deleteCalls)
	}
}

func TestDeleteTenantAllowsCrossTenantSuperuser(t *testing.T) {
	svc := &tenantDeleteTenantService{}
	h := &TenantHandler{
		service: svc,
		userService: &tenantDeleteUserService{user: &types.User{
			ID:                  "super-user",
			CanAccessAllTenants: true,
		}},
		// memberService stays nil: catalog managers bypass the membership gate
		// entirely, so it must not be dereferenced.
	}
	r := newTenantDeleteRouter(h)
	w := doDeleteTenant(t, r, "7")

	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if svc.deleteCalls != 1 {
		t.Fatalf("DeleteTenant called %d times, want 1", svc.deleteCalls)
	}
}
