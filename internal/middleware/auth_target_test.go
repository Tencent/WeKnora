package middleware

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
)

type targetTenantService struct{ interfaces.TenantService }

func (targetTenantService) GetTenantByID(_ context.Context, id uint64) (*types.Tenant, error) {
	return &types.Tenant{ID: id}, nil
}

func TestResolveTargetTenant_RevalidatesForeignJWTWhenFlagOff(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("GET", "/protected", nil)
	user := &types.User{ID: "super", TenantID: 1, CanAccessAllTenants: true}

	_, _, _, ok := resolveTargetTenant(
		c,
		targetTenantService{},
		newFakeMemberService(),
		&config.Config{Tenant: &config.TenantConfig{EnableCrossTenantAccess: false}},
		user,
		99,
	)
	if ok {
		t.Fatal("foreign JWT tenant must be rejected after the cross-tenant flag is disabled")
	}
	if !c.IsAborted() {
		t.Fatal("rejected foreign JWT tenant must abort the request")
	}
}

func TestResolveTargetTenant_AllowsForeignJWTWhenFlagAndAttributeAreEnabled(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("GET", "/protected", nil)
	user := &types.User{ID: "super", TenantID: 1, CanAccessAllTenants: true}

	tenantID, _, crossTenant, ok := resolveTargetTenant(
		c,
		targetTenantService{},
		newFakeMemberService(),
		&config.Config{Tenant: &config.TenantConfig{EnableCrossTenantAccess: true}},
		user,
		99,
	)
	if !ok || tenantID != 99 || !crossTenant {
		t.Fatalf("got tenant=%d cross=%v ok=%v, want tenant=99 cross=true ok=true", tenantID, crossTenant, ok)
	}
}
