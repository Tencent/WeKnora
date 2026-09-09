package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// Exercise the HTTP gate followed by the same scope check used by handlers
// resolving batch/body targets. A grant on one KB must not authorize another.
func TestAPIKeyGateEnforcesPerKBOperations(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		name       string
		capability types.APIKeyCapability
		ids        string
		allowed    bool
	}{
		{"read A", types.APIKeyCapabilityRetrieve, `["a"]`, true},
		{"read B", types.APIKeyCapabilityRetrieve, `["b"]`, true},
		{"write A", types.APIKeyCapabilityIngest, `["a"]`, false},
		{"write B", types.APIKeyCapabilityIngest, `["b"]`, true},
		{"mixed write", types.APIKeyCapabilityIngest, `["a","b"]`, false},
		{"manage B", types.APIKeyCapabilityManageKnowledgeBases, `["b"]`, false},
		{"manage C", types.APIKeyCapabilityManageKnowledgeBases, `["c"]`, true},
		{"unknown read", types.APIKeyCapabilityRetrieve, `["outside"]`, false},
		{"chat A", types.APIKeyCapabilityChat, `["a"]`, true},
		{"sync A", types.APIKeyCapabilityManageDataSources, `["a"]`, false},
		{"sync B", types.APIKeyCapabilityManageDataSources, `["b"]`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			scope := types.TenantAPIKeyScope{
				Capabilities: types.StringArray{
					"retrieve",
					"ingest",
					"manage_kbs",
					"chat",
					"manage_datasources",
				},
				KnowledgeBasePermissions: types.APIKeyKBPermissions{
					"a": types.APIKeyKBRead,
					"b": types.APIKeyKBWrite,
					"c": types.APIKeyKBManage,
				},
			}
			a := NewAPIKeyRouteAuthorizer()
			a.Register(
				http.MethodPost,
				"/operation",
				APIKeyRoutePolicy{RequireFullAccess: true}.WithCapability(tc.capability),
			)
			r := gin.New()
			r.Use(func(c *gin.Context) {
				c.Request = c.Request.WithContext(types.WithTenantAPIKeyScope(c.Request.Context(), scope))
			}, a.Middleware())
			r.POST("/operation", func(c *gin.Context) {
				var ids []string
				require.NoError(t, c.ShouldBindJSON(&ids))
				if types.AuthorizeTenantAPIKeyKnowledgeBases(c.Request.Context(), ids...) != nil {
					c.AbortWithStatus(http.StatusForbidden)
					return
				}
				c.Status(http.StatusOK)
			})
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/operation", strings.NewReader(tc.ids)))
			require.Equal(t, tc.allowed, w.Code == http.StatusOK)
		})
	}
}

func TestAPIKeyGatePermissionUsesOnlyGrantedRouteAlternatives(t *testing.T) {
	policy := APIKeyRoutePolicy{RequireFullAccess: true}.
		WithCapability(types.APIKeyCapabilityRetrieve).
		WithCapability(types.APIKeyCapabilityManageKnowledgeBases)
	require.Equal(
		t,
		types.APIKeyKBManage,
		policy.knowledgeBasePermission(types.TenantAPIKeyScope{Capabilities: types.StringArray{"manage_kbs"}}),
	)
	require.Equal(
		t,
		types.APIKeyKBRead,
		policy.knowledgeBasePermission(
			types.TenantAPIKeyScope{Capabilities: types.StringArray{"retrieve", "manage_kbs"}},
		),
	)
	key := &types.TenantAPIKeyScope{
		Capabilities:             types.StringArray{"retrieve"},
		KnowledgeBasePermissions: types.APIKeyKBPermissions{"kb-1": types.APIKeyKBManage},
	}
	require.False(
		t,
		runGate(t, newTestAuthorizer(), key, http.MethodPost, "/api/v1/knowledge-bases/:id/knowledge/file"),
	)
}

func TestGranularKeyEnforcesRevokedShareDuringRBACRollout(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Params = gin.Params{{Key: "id", Value: "shared"}}
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	ctx := types.WithCaller(req.Context(), types.Caller{TenantID: 42, Role: types.TenantRoleViewer})
	ctx = types.WithTenantAPIKeyScope(ctx, types.TenantAPIKeyScope{
		KnowledgeBasePermissions: types.APIKeyKBPermissions{"shared": types.APIKeyKBRead},
	})
	c.Request = req.WithContext(ctx)
	guard := RequireKBAccess(KBIDFromParam("id"), types.OrgRoleViewer,
		&stubKBLookup{kbs: map[string]*types.KnowledgeBase{"shared": {ID: "shared", TenantID: 99}}},
		&stubKBShareForGuard{}, nil, cfgRBAC(false))
	guard(c)
	require.True(t, c.IsAborted())
	require.NotEmpty(t, c.Errors)
}
