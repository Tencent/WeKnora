package middleware

import (
	"net/http/httptest"
	"testing"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/mcp/headertemplate"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestMCPHeadersCaptureWebIdentityAndImmutableBusinessHeaders(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/api/v1/agent", nil)
	c.Request.Header.Set("X-AAA", "department-A")
	u := &types.User{ID: "alice", Email: "alice@example.com"}
	applyAuthSession(c, authSession{User: u, TenantID: 7, Principal: types.Principal{Type: types.PrincipalWebUser, ID: "alice"}})
	c.Request.Header.Set("X-AAA", "mutated")
	u.Email = "mutated@example.com"
	ctx := logger.CloneContext(types.WithExecutionTenant(c.Request.Context(), 99))
	tmpl, err := headertemplate.Parse("{{user.email}}/{{request.headers.X-AAA}}/{{tenant.id}}")
	require.NoError(t, err)
	got, err := tmpl.Resolve(types.MCPHeaderContextFromContext(ctx))
	require.NoError(t, err)
	require.Equal(t, "alice@example.com/department-A/7", got)
}

func TestMCPHeadersNeverUseAPICompatibilityUserEmail(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/api/v1/agent", nil)
	applyAuthSession(c, authSession{User: &types.User{ID: "owner", Email: "owner@example.com"}, TenantID: 7,
		Principal: types.Principal{Type: types.PrincipalAPIExternalUser, ID: "7:employee"}, ExternalUserID: "employee"})
	tmpl, _ := headertemplate.Parse("{{user.email ?? external.user_id}}")
	got, err := tmpl.Resolve(types.MCPHeaderContextFromContext(c.Request.Context()))
	require.NoError(t, err)
	require.Equal(t, "employee", got)
}
