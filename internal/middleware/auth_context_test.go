package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gin-gonic/gin"
)

// TestApplyAuthSessionSetsBothSurfaces locks the core invariant of the
// helper: every attached value must be readable from BOTH c.Keys (c.Get)
// and the request context (types.*FromContext). A key present on only one
// surface is the class of bug the helper exists to prevent.
func TestApplyAuthSessionSetsBothSurfaces(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/knowledge-bases", nil)

	user := &types.User{ID: "u1", IsSystemAdmin: true}
	tenant := &types.Tenant{ID: 7}
	principal := types.Principal{Type: types.PrincipalWebUser, ID: "u1"}
	scope := &types.TenantAPIKeyScope{KeyID: 3}
	applyAuthSession(c, authSession{
		User:        user,
		Principal:   principal,
		TenantID:    7,
		Tenant:      tenant,
		Role:        types.TenantRoleAdmin,
		SystemAdmin: true,
		APIKeyScope: scope,
		Extra:       map[types.ContextKey]any{EmbedChannelContextKey: &types.EmbedChannel{ID: "ch"}},
	})

	ctx := c.Request.Context()
	if got, ok := types.TenantIDFromContext(ctx); !ok || got != 7 {
		t.Fatalf("ctx tenant id = %d, ok=%v", got, ok)
	}
	if got, ok := c.Get(types.TenantIDContextKey.String()); !ok || got.(uint64) != 7 {
		t.Fatalf("keys tenant id = %v, ok=%v", got, ok)
	}
	if got, ok := types.TenantInfoFromContext(ctx); !ok || got.ID != 7 {
		t.Fatalf("ctx tenant info = %#v, ok=%v", got, ok)
	}
	if got, ok := types.UserIDFromContext(ctx); !ok || got != "u1" {
		t.Fatalf("ctx user id = %q, ok=%v", got, ok)
	}
	if got, ok := c.Get(types.UserContextKey.String()); !ok || got.(*types.User).ID != "u1" {
		t.Fatalf("keys user = %#v, ok=%v", got, ok)
	}
	if got, ok := types.PrincipalFromContext(ctx); !ok || got != principal {
		t.Fatalf("ctx principal = %#v, ok=%v", got, ok)
	}
	if got := types.TenantRoleFromContext(ctx); got != types.TenantRoleAdmin {
		t.Fatalf("ctx role = %q", got)
	}
	if got, ok := c.Get(types.TenantRoleContextKey.String()); !ok || got.(types.TenantRole) != types.TenantRoleAdmin {
		t.Fatalf("keys role = %v, ok=%v", got, ok)
	}
	if !types.IsSystemAdminFromContext(ctx) {
		t.Fatal("ctx system admin flag lost")
	}
	if got, ok := types.TenantAPIKeyScopeFromContext(ctx); !ok || got.KeyID != 3 {
		t.Fatalf("ctx api key scope = %#v, ok=%v", got, ok)
	}
	if ch, ok := EmbedChannelFromContext(ctx); !ok || ch.ID != "ch" {
		t.Fatalf("ctx embed channel = %#v, ok=%v", ch, ok)
	}
	if got, ok := c.Get(EmbedChannelContextKey.String()); !ok || got.(*types.EmbedChannel).ID != "ch" {
		t.Fatalf("keys embed channel = %v, ok=%v", got, ok)
	}
}

// TestApplyAuthSessionTenantless verifies that a tenantless session attaches
// neither tenant keys nor a role key — RequireRole's fail-closed Viewer
// default depends on the role key being absent, and TENANT_REQUIRED
// handling depends on the tenant key being absent.
func TestApplyAuthSessionTenantless(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", nil)

	attachTenantlessUserContext(c, &types.User{ID: "u2"})

	ctx := c.Request.Context()
	if _, ok := types.TenantIDFromContext(ctx); ok {
		t.Fatal("tenantless session must not carry a tenant id")
	}
	if _, ok := c.Get(types.TenantRoleContextKey.String()); ok {
		t.Fatal("tenantless session must not carry a role key")
	}
	if got, ok := types.UserIDFromContext(ctx); !ok || got != "u2" {
		t.Fatalf("ctx user id = %q, ok=%v", got, ok)
	}
	if types.IsSystemAdminFromContext(ctx) {
		t.Fatal("non-admin user must not be flagged system admin")
	}
}

func TestBearerToken(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cases := []struct {
		header string
		want   string
		ok     bool
	}{
		{"", "", false},
		{"Basic abc", "", false},
		{"Bearer", "", false},
		{"Bearer abc", "abc", true},
	}
	for _, tc := range cases {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
		if tc.header != "" {
			c.Request.Header.Set("Authorization", tc.header)
		}
		got, ok := bearerToken(c)
		if got != tc.want || ok != tc.ok {
			t.Fatalf("bearerToken(%q) = (%q, %v), want (%q, %v)", tc.header, got, ok, tc.want, tc.ok)
		}
	}
}
